package fetch

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseBearerChallenge(t *testing.T) {
	challenge := `Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:homebrew/core/sdl2:pull"`
	realm, service, scope, err := parseBearerChallenge(challenge)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if realm != "https://ghcr.io/token" {
		t.Fatalf("unexpected realm: %s", realm)
	}
	if service != "ghcr.io" {
		t.Fatalf("unexpected service: %s", service)
	}
	if scope != "repository:homebrew/core/sdl2:pull" {
		t.Fatalf("unexpected scope: %s", scope)
	}
}

func TestFetchHandlesBearerAuthChallenge(t *testing.T) {
	temp := t.TempDir()
	cache := NewCache(temp)

	tokenValue := "test-token"
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blob":
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+tokenValue {
				w.Header().Set("Www-Authenticate", `Bearer realm="`+serverURL+`/token",service="ghcr.io",scope="repository:homebrew/core/sdl2:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte("bottle-bytes"))
		case "/token":
			if got := r.URL.Query().Get("service"); got != "ghcr.io" {
				t.Fatalf("unexpected service query: %q", got)
			}
			if got := r.URL.Query().Get("scope"); got != "repository:homebrew/core/sdl2:pull" {
				t.Fatalf("unexpected scope query: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"` + tokenValue + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	path, err := cache.Fetch(context.Background(), server.URL+"/blob")
	if err != nil {
		t.Fatalf("unexpected fetch error: %v", err)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read cached file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "bottle-bytes" {
		t.Fatalf("unexpected cached content: %q", string(data))
	}
}

func TestFetchWithProgressReportsDone(t *testing.T) {
	temp := t.TempDir()
	cache := NewCache(temp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 4096)))
	}))
	defer server.Close()

	var mu sync.Mutex
	callbackCount := 0
	last := Progress{}
	_, err := cache.FetchWithProgress(context.Background(), server.URL, func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		callbackCount++
		last = p
	})
	if err != nil {
		t.Fatalf("unexpected fetch error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if callbackCount == 0 {
		t.Fatal("expected progress callback to be called")
	}
	if callbackCount < 2 {
		t.Fatalf("expected multiple progress callbacks, got %d", callbackCount)
	}
	if !last.Done {
		t.Fatalf("expected final callback to be done, got %#v", last)
	}
	if last.DownloadedBytes == 0 {
		t.Fatalf("expected downloaded bytes to be > 0, got %#v", last)
	}
}

func TestSeaHash64Vectors(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  uint64
	}{
		{name: "empty", input: []byte(""), want: 14492805990617963705},
		{name: "phrase", input: []byte("to be or not to be"), want: 1988685042348123509},
		{name: "long", input: []byte("love is a wonderful terrible thing"), want: 4784284276849692846},
		{name: "bytes", input: []byte{1, 2, 3, 4}, want: 7946236997574049990},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := seahash64(tc.input); got != tc.want {
				t.Fatalf("seahash64() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHashUsesLittleEndianDigestEncoding(t *testing.T) {
	got := hash("to be or not to be")
	want := "75e54a6f823a991b"
	if got != want {
		t.Fatalf("hash() = %q, want %q", got, want)
	}

	decoded, err := hex.DecodeString(got)
	if err != nil {
		t.Fatalf("decode hash: %v", err)
	}
	if len(decoded) != 8 {
		t.Fatalf("expected 8-byte digest, got %d", len(decoded))
	}
}
