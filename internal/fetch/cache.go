package fetch

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Cache struct {
	Dir string

	mu            sync.Mutex
	locks         map[string]*sync.Mutex
	lastPruneTime time.Time
}

type Progress struct {
	URL              string
	DownloadedBytes  int64
	TotalBytes       int64
	SpeedBytesPerSec float64
	Cached           bool
	Done             bool
}

func NewCache(dir string) *Cache {
	return &Cache{Dir: dir, locks: map[string]*sync.Mutex{}}
}

func (c *Cache) Fetch(ctx context.Context, url string) (string, error) {
	return c.FetchWithProgress(ctx, url, nil)
}

func (c *Cache) FetchWithProgress(ctx context.Context, url string, onProgress func(Progress)) (string, error) {
	if strings.TrimSpace(url) == "" {
		return "", nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	if err := c.pruneExpired(ctx); err != nil {
		return "", err
	}

	canonical := canonicalizeURL(url)
	key := hash(canonical)
	target := c.cachePathForKey(key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create cache shard dir: %w", err)
	}

	lock := c.getLock(key)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(target); err == nil {
		if onProgress != nil {
			info, statErr := os.Stat(target)
			if statErr == nil {
				onProgress(Progress{URL: url, DownloadedBytes: info.Size(), TotalBytes: info.Size(), Cached: true, Done: true})
			}
		}
		return target, nil
	}

	if err := c.downloadWithRetry(ctx, url, target, onProgress); err != nil {
		return "", err
	}

	return target, nil
}

func (c *Cache) downloadWithRetry(ctx context.Context, url, target string, onProgress func(Progress)) error {
	const maxAttempts = 3
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := c.downloadOnce(ctx, url, target, onProgress); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if attempt == maxAttempts {
			break
		}

		backoff := time.Duration(attempt*attempt) * 200 * time.Millisecond
		jitter := time.Duration(rand.Intn(120)) * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff + jitter):
		}
	}

	return fmt.Errorf("download %q failed after retries: %w", url, lastErr)
}

func (c *Cache) downloadOnce(ctx context.Context, url, target string, onProgress func(Progress)) error {
	bearerToken := ""
	if token, ok, tokenErr := c.fetchGHCRTokenForBlobURL(ctx, url); tokenErr == nil && ok {
		bearerToken = token
	}

	resp, err := c.doDownloadRequest(ctx, url, bearerToken)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("Www-Authenticate")
		_ = resp.Body.Close()
		token, tokenErr := c.fetchBearerToken(ctx, challenge)
		if tokenErr != nil {
			return fmt.Errorf("registry authentication required: %w", tokenErr)
		}

		resp, err = c.doDownloadRequest(ctx, url, token)
		if err != nil {
			return fmt.Errorf("authenticated download request: %w", err)
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	tmp := target + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}

	totalBytes := resp.ContentLength
	start := time.Now()
	var downloaded int64
	buf := make([]byte, 32*1024)

	if onProgress != nil {
		onProgress(Progress{
			URL:              url,
			DownloadedBytes:  0,
			TotalBytes:       totalBytes,
			SpeedBytesPerSec: 0,
			Done:             false,
		})
	}

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return fmt.Errorf("write cache file: %w", writeErr)
			}
			downloaded += int64(n)
		}

		if onProgress != nil {
			now := time.Now()
			elapsed := now.Sub(start).Seconds()
			speed := 0.0
			if elapsed > 0 {
				speed = float64(downloaded) / elapsed
			}
			onProgress(Progress{
				URL:              url,
				DownloadedBytes:  downloaded,
				TotalBytes:       totalBytes,
				SpeedBytesPerSec: speed,
				Done:             readErr == io.EOF,
			})
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("write cache file: %w", readErr)
		}
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close cache file: %w", err)
	}

	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish cache file: %w", err)
	}

	return nil
}

func (c *Cache) fetchGHCRTokenForBlobURL(ctx context.Context, sourceURL string) (token string, ok bool, err error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return "", false, err
	}
	if !strings.EqualFold(u.Host, "ghcr.io") {
		return "", false, nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 5 || parts[0] != "v2" {
		return "", false, nil
	}
	blobIdx := -1
	for idx, part := range parts {
		if part == "blobs" {
			blobIdx = idx
			break
		}
	}
	if blobIdx < 3 {
		return "", false, nil
	}
	repo := strings.Join(parts[1:blobIdx], "/")
	if strings.TrimSpace(repo) == "" {
		return "", false, nil
	}

	scope := "repository:" + repo + ":pull"
	tokenURL := "https://ghcr.io/token?service=ghcr.io&scope=" + url.QueryEscape(scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", true, err
	}
	req.Header.Set("User-Agent", "ub/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", true, fmt.Errorf("ghcr token endpoint returned status %d", resp.StatusCode)
	}

	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", true, err
	}
	if tr.Token != "" {
		return tr.Token, true, nil
	}
	if tr.AccessToken != "" {
		return tr.AccessToken, true, nil
	}
	return "", true, fmt.Errorf("ghcr token response missing token")
}

func (c *Cache) doDownloadRequest(ctx context.Context, sourceURL, bearerToken string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream, application/vnd.oci.image.layer.v1.tar+gzip, */*")
	req.Header.Set("User-Agent", "ub/0.1")
	if strings.TrimSpace(bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	return http.DefaultClient.Do(req)
}

func (c *Cache) fetchBearerToken(ctx context.Context, challenge string) (string, error) {
	realm, service, scope, err := parseBearerChallenge(challenge)
	if err != nil {
		return "", err
	}

	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("invalid token realm %q: %w", realm, err)
	}
	query := tokenURL.Query()
	if service != "" {
		query.Set("service", service)
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	tokenURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("User-Agent", "ub/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if strings.TrimSpace(tr.Token) != "" {
		return tr.Token, nil
	}
	if strings.TrimSpace(tr.AccessToken) != "" {
		return tr.AccessToken, nil
	}

	return "", fmt.Errorf("token response missing token")
}

func parseBearerChallenge(challenge string) (realm, service, scope string, err error) {
	if strings.TrimSpace(challenge) == "" {
		return "", "", "", fmt.Errorf("missing WWW-Authenticate challenge")
	}
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", "", "", fmt.Errorf("unsupported auth challenge %q", challenge)
	}

	params := challenge[len("Bearer "):]
	re := regexp.MustCompile(`([a-zA-Z_]+)="([^"]*)"`)
	matches := re.FindAllStringSubmatch(params, -1)
	values := map[string]string{}
	for _, m := range matches {
		if len(m) == 3 {
			values[strings.ToLower(m[1])] = m[2]
		}
	}

	realm = values["realm"]
	service = values["service"]
	scope = values["scope"]
	if strings.TrimSpace(realm) == "" {
		return "", "", "", fmt.Errorf("auth challenge missing realm")
	}

	return realm, service, scope, nil
}

func (c *Cache) getLock(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lock, ok := c.locks[key]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	c.locks[key] = lock
	return lock
}

func (c *Cache) cachePathForKey(key string) string {
	shard := "xx"
	if len(key) >= 2 {
		shard = key[:2]
	}
	return filepath.Join(c.Dir, "archive-v0", shard, key+".src")
}

func (c *Cache) pruneExpired(ctx context.Context) error {
	const (
		maxAge       = 30 * 24 * time.Hour
		minPruneStep = 6 * time.Hour
	)

	now := time.Now()
	c.mu.Lock()
	if !c.lastPruneTime.IsZero() && now.Sub(c.lastPruneTime) < minPruneStep {
		c.mu.Unlock()
		return nil
	}
	c.lastPruneTime = now
	c.mu.Unlock()

	cutoff := now.Add(-maxAge)
	return filepath.WalkDir(c.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".src" {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

func canonicalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.User = nil
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && strings.HasSuffix(u.Host, ":443")) || (u.Scheme == "http" && strings.HasSuffix(u.Host, ":80")) {
		host, _, splitErr := strings.Cut(u.Host, ":")
		if splitErr {
			u.Host = host
		}
	}
	return u.String()
}

func hash(input string) string {
	sum := seahash64([]byte(input))
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, sum)
	return hex.EncodeToString(buf)
}

const (
	seaHashSeedA = uint64(0x16f11fe89b0d677c)
	seaHashSeedB = uint64(0xb480a793d8e6c86c)
	seaHashSeedC = uint64(0x6fe2e5aaf078ebc9)
	seaHashSeedD = uint64(0x14f994a4c5259381)
	seaHashMul   = uint64(0x6eed0e9da4d94a4f)
)

func seahash64(data []byte) uint64 {
	a, b, c, d := seaHashSeedA, seaHashSeedB, seaHashSeedC, seaHashSeedD

	for offset := 0; offset+8 <= len(data); offset += 8 {
		word := binary.LittleEndian.Uint64(data[offset : offset+8])
		a, b, c, d = b, c, d, seaHashDiffuse(a^word)
	}

	if tail := len(data) % 8; tail != 0 {
		var last [8]byte
		start := len(data) - tail
		copy(last[:], data[start:])
		word := binary.LittleEndian.Uint64(last[:])
		a, b, c, d = b, c, d, seaHashDiffuse(a^word)
	}

	return seaHashDiffuse(a ^ b ^ c ^ d ^ uint64(len(data)))
}

func seaHashDiffuse(x uint64) uint64 {
	x *= seaHashMul
	a := x >> 32
	b := x >> 60
	x ^= a >> b
	x *= seaHashMul
	return x
}
