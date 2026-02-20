package homebrewapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ub/internal/fetch"
)

const (
	baseURL         = "https://formulae.brew.sh/api"
	formulaListPath = "/formula.json"
)

type Client struct {
	fetcher *fetch.Cache
	repoDir string
	repoMu  sync.Mutex
	repoSynced bool
}

func New(cacheDir, repoDir string) *Client {
	return &Client{fetcher: fetch.NewCache(filepath.Join(cacheDir, "api")), repoDir: repoDir}
}

type FormulaSummary struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Desc     string `json:"desc"`
}

type BottleFile struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type Formula struct {
	Name         string   `json:"name"`
	FullName     string   `json:"full_name"`
	Desc         string   `json:"desc"`
	Homepage     string   `json:"homepage"`
	Dependencies []string `json:"dependencies"`
	Versions     struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	Bottle struct {
		Stable struct {
			Files map[string]BottleFile `json:"files"`
		} `json:"stable"`
	} `json:"bottle"`
}

type CaskBinaryArtifact struct {
	Source string
	Target string
}

type Cask struct {
	Token     string                       `json:"token"`
	Name      []string                     `json:"name"`
	Desc      string                       `json:"desc"`
	Homepage  string                       `json:"homepage"`
	URL       string                       `json:"url"`
	Version   string                       `json:"version"`
	SHA256    string                       `json:"sha256"`
	Artifacts []map[string]json.RawMessage `json:"artifacts"`
}

func (c Cask) AppArtifact() string {
	for _, artifact := range c.Artifacts {
		raw, ok := artifact["app"]
		if !ok {
			continue
		}
		var payload []json.RawMessage
		if err := json.Unmarshal(raw, &payload); err != nil || len(payload) == 0 {
			continue
		}
		var app string
		if err := json.Unmarshal(payload[0], &app); err != nil {
			continue
		}
		if strings.TrimSpace(app) != "" {
			return app
		}
	}
	return ""
}

func (c Cask) BinaryArtifacts() []CaskBinaryArtifact {
	out := make([]CaskBinaryArtifact, 0)
	for _, artifact := range c.Artifacts {
		raw, ok := artifact["binary"]
		if !ok {
			continue
		}
		var payload []json.RawMessage
		if err := json.Unmarshal(raw, &payload); err != nil || len(payload) == 0 {
			continue
		}
		var source string
		if err := json.Unmarshal(payload[0], &source); err != nil || strings.TrimSpace(source) == "" {
			continue
		}
		entry := CaskBinaryArtifact{Source: source}
		if len(payload) > 1 {
			var opts struct {
				Target string `json:"target"`
			}
			if err := json.Unmarshal(payload[1], &opts); err == nil {
				entry.Target = strings.TrimSpace(opts.Target)
			}
		}
		out = append(out, entry)
	}
	return out
}

func (c *Client) FormulaList(ctx context.Context) ([]FormulaSummary, error) {
	if err := c.ensureLocalRepository(ctx); err != nil {
		return nil, err
	}
	url := baseURL + formulaListPath
	file, err := c.fetcher.Fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read formula list: %w", err)
	}

	var list []FormulaSummary
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse formula list: %w", err)
	}
	return list, nil
}

func (c *Client) FormulaByName(ctx context.Context, name string) (Formula, error) {
	if err := c.ensureLocalRepository(ctx); err != nil {
		return Formula{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Formula{}, fmt.Errorf("formula name is required")
	}
	url := fmt.Sprintf("%s/formula/%s.json", baseURL, name)
	file, err := c.fetcher.Fetch(ctx, url)
	if err != nil {
		return Formula{}, err
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return Formula{}, fmt.Errorf("read formula %q metadata: %w", name, err)
	}

	var f Formula
	if err := json.Unmarshal(data, &f); err != nil {
		return Formula{}, fmt.Errorf("parse formula %q metadata: %w", name, err)
	}
	if f.Name == "" {
		return Formula{}, fmt.Errorf("formula %q metadata is missing name", name)
	}
	return f, nil
}

func (c *Client) CaskByName(ctx context.Context, name string) (Cask, error) {
	if err := c.ensureLocalRepository(ctx); err != nil {
		return Cask{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Cask{}, fmt.Errorf("cask name is required")
	}
	url := fmt.Sprintf("%s/cask/%s.json", baseURL, name)
	file, err := c.fetcher.Fetch(ctx, url)
	if err != nil {
		return Cask{}, err
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return Cask{}, fmt.Errorf("read cask %q metadata: %w", name, err)
	}

	var cask Cask
	if err := json.Unmarshal(data, &cask); err != nil {
		return Cask{}, fmt.Errorf("parse cask %q metadata: %w", name, err)
	}
	if strings.TrimSpace(cask.Token) == "" {
		return Cask{}, fmt.Errorf("cask %q metadata is missing token", name)
	}
	return cask, nil
}

func (c *Client) ensureLocalRepository(ctx context.Context) error {
	c.repoMu.Lock()
	if c.repoSynced {
		c.repoMu.Unlock()
		return nil
	}
	c.repoMu.Unlock()

	if strings.TrimSpace(c.repoDir) == "" {
		return nil
	}
	if err := os.MkdirAll(c.repoDir, 0o755); err != nil {
		return fmt.Errorf("create local repository dir: %w", err)
	}

	files := []string{"cask.jws.json", "formula.jws.json"}
	for _, fileName := range files {
		url := baseURL + "/" + fileName
		source, err := c.fetcher.Fetch(ctx, url)
		if err != nil {
			return err
		}
		target := filepath.Join(c.repoDir, fileName)
		if err := copyFile(source, target); err != nil {
			return err
		}
		if info, err := os.Stat(source); err == nil {
			fmt.Printf("✔︎ JSON API %-56s Downloaded %8s/%8s\n", fileName, formatSize(info.Size()), formatSize(info.Size()))
		}
	}

	c.repoMu.Lock()
	c.repoSynced = true
	c.repoMu.Unlock()
	return nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source %q: %w", source, err)
	}
	defer in.Close()

	tmp := target + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create target %q: %w", target, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy file to %q: %w", target, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close target %q: %w", target, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish target %q: %w", target, err)
	}
	return nil
}

func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	if bytes >= gb {
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	}
	if bytes >= mb {
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	}
	if bytes >= kb {
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	}
	return fmt.Sprintf("%dB", bytes)
}
