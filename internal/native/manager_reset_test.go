package native

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetRemovesCachedFiles(t *testing.T) {
	tmp := t.TempDir()
	paths := Paths{
		BaseDir:      tmp,
		Prefix:       filepath.Join(tmp, "ub"),
		Repo:         filepath.Join(tmp, "unbrew"),
		Cellar:       filepath.Join(tmp, "ub", "Cellar"),
		Caskroom:     filepath.Join(tmp, "ub", "Caskroom"),
		Cache:        filepath.Join(tmp, "ub", "cache"),
		Bin:          filepath.Join(tmp, "ub", "bin"),
		Sbin:         filepath.Join(tmp, "ub", "sbin"),
		Applications: filepath.Join(tmp, "ub", "Applications"),
	}
	manager := &Manager{Paths: paths}
	if err := manager.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}

	cachedBottle := filepath.Join(paths.Cache, "bottles", "hello.src")
	cachedAPI := filepath.Join(paths.Cache, "api", "formula.src")
	if err := os.MkdirAll(filepath.Dir(cachedBottle), 0o755); err != nil {
		t.Fatalf("mkdir bottle cache: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachedAPI), 0o755); err != nil {
		t.Fatalf("mkdir api cache: %v", err)
	}
	if err := os.WriteFile(cachedBottle, []byte("bottle"), 0o644); err != nil {
		t.Fatalf("write bottle cache: %v", err)
	}
	if err := os.WriteFile(cachedAPI, []byte("api"), 0o644); err != nil {
		t.Fatalf("write api cache: %v", err)
	}

	if err := manager.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := os.Stat(cachedBottle); !os.IsNotExist(err) {
		t.Fatalf("expected bottle cache removed, stat err: %v", err)
	}
	if _, err := os.Stat(cachedAPI); !os.IsNotExist(err) {
		t.Fatalf("expected api cache removed, stat err: %v", err)
	}
	if info, err := os.Stat(paths.Cache); err != nil || !info.IsDir() {
		t.Fatalf("expected cache dir recreated, stat err: %v", err)
	}
}

func TestResetRemovesInstalledCasks(t *testing.T) {
	tmp := t.TempDir()
	paths := Paths{
		BaseDir:      tmp,
		Prefix:       filepath.Join(tmp, "ub"),
		Repo:         filepath.Join(tmp, "unbrew"),
		Cellar:       filepath.Join(tmp, "ub", "Cellar"),
		Caskroom:     filepath.Join(tmp, "ub", "Caskroom"),
		Cache:        filepath.Join(tmp, "ub", "cache"),
		Bin:          filepath.Join(tmp, "ub", "bin"),
		Sbin:         filepath.Join(tmp, "ub", "sbin"),
		Applications: filepath.Join(tmp, "ub", "Applications"),
	}
	manager := &Manager{Paths: paths}
	if err := manager.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
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
	if err := writeCaskReceipt(versionDir, "cursor", "1.0.0", appPath, []string{binPath}); err != nil {
		t.Fatalf("write receipt: %v", err)
	}

	if err := manager.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := os.Stat(filepath.Join(paths.Caskroom, "cursor")); !os.IsNotExist(err) {
		t.Fatalf("expected cask removed, stat err: %v", err)
	}
	if _, err := os.Stat(appPath); !os.IsNotExist(err) {
		t.Fatalf("expected app removed, stat err: %v", err)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("expected binary removed, stat err: %v", err)
	}
}
