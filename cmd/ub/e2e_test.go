package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ub/internal/native"
)

func TestE2E_ResetRemovesCaskAndCache(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("UB_BASE_DIR", tmp)

	paths := native.DefaultPaths()
	if err := os.MkdirAll(paths.Caskroom, 0o755); err != nil {
		t.Fatalf("mkdir caskroom: %v", err)
	}
	if err := os.MkdirAll(paths.Applications, 0o755); err != nil {
		t.Fatalf("mkdir applications: %v", err)
	}
	if err := os.MkdirAll(paths.Bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.Cache, "api"), 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	versionDir := filepath.Join(paths.Caskroom, "cursor", "1.0.0")
	appPath := filepath.Join(paths.Applications, "Cursor.app")
	binPath := filepath.Join(paths.Bin, "cursor")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatalf("mkdir app path: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "payload.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	receipt := map[string]any{
		"token":           "cursor",
		"version":         "1.0.0",
		"app_path":        appPath,
		"linked_binaries": []string{binPath},
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "INSTALL_RECEIPT.json"), receiptBytes, 0o644); err != nil {
		t.Fatalf("write receipt: %v", err)
	}

	cacheFile := filepath.Join(paths.Cache, "api", "formula.src")
	if err := os.WriteFile(cacheFile, []byte("cached"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	if _, err := captureStdout(func() error { return run([]string{"reset"}) }); err != nil {
		t.Fatalf("run reset: %v", err)
	}

	if _, err := os.Stat(filepath.Join(paths.Caskroom, "cursor")); !os.IsNotExist(err) {
		t.Fatalf("expected cask removed, got err=%v", err)
	}
	if _, err := os.Stat(appPath); !os.IsNotExist(err) {
		t.Fatalf("expected app removed, got err=%v", err)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("expected binary removed, got err=%v", err)
	}
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Fatalf("expected cache file removed, got err=%v", err)
	}
}

func TestE2E_ListAndPrefix(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("UB_BASE_DIR", tmp)

	paths := native.DefaultPaths()
	if err := os.MkdirAll(filepath.Join(paths.Cellar, "ffmpeg", "8.0.1"), 0o755); err != nil {
		t.Fatalf("mkdir ffmpeg: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(paths.Cellar, "hello", "2.12.2"), 0o755); err != nil {
		t.Fatalf("mkdir hello: %v", err)
	}

	listOut, err := captureStdout(func() error { return run([]string{"list"}) })
	if err != nil {
		t.Fatalf("run list: %v", err)
	}
	if !strings.Contains(listOut, "ffmpeg") || !strings.Contains(listOut, "hello") {
		t.Fatalf("list output missing formulas: %q", listOut)
	}

	prefixOut, err := captureStdout(func() error { return run([]string{"prefix", "hello"}) })
	if err != nil {
		t.Fatalf("run prefix: %v", err)
	}
	want := filepath.Join(paths.Cellar, "hello", "2.12.2")
	if strings.TrimSpace(prefixOut) != want {
		t.Fatalf("prefix output = %q, want %q", strings.TrimSpace(prefixOut), want)
	}
}

func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	runErr := fn()
	_ = w.Close()
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		return "", readErr
	}
	return string(data), runErr
}
