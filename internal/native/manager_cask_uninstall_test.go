package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallCaskLockedRemovesReceiptTargets(t *testing.T) {
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

	versionDir := filepath.Join(paths.Caskroom, "cursor", "2.5.17")
	appPath := filepath.Join(paths.Applications, "Cursor.app")
	binPath := filepath.Join(paths.Bin, "cursor")

	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatalf("mkdir app path: %v", err)
	}
	if err := os.MkdirAll(paths.Bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write bin file: %v", err)
	}
	payload := filepath.Join(versionDir, "payload.txt")
	if err := os.WriteFile(payload, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := writeCaskReceipt(versionDir, "cursor", "2.5.17", appPath, []string{binPath}); err != nil {
		t.Fatalf("write receipt: %v", err)
	}

	rec, err := manager.uninstallCaskLocked("cursor")
	if err != nil {
		t.Fatalf("uninstallCaskLocked: %v", err)
	}
	if rec.Name != "cursor" {
		t.Fatalf("record name = %q", rec.Name)
	}
	if rec.Files == 0 {
		t.Fatal("expected removed file count > 0")
	}

	if _, err := os.Stat(filepath.Join(paths.Caskroom, "cursor")); !os.IsNotExist(err) {
		t.Fatalf("expected cask root removed, got err=%v", err)
	}
	if _, err := os.Stat(appPath); !os.IsNotExist(err) {
		t.Fatalf("expected app removed, got err=%v", err)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("expected binary removed, got err=%v", err)
	}
}

func TestUninstallWithAutoremoveSupportsCaskTargets(t *testing.T) {
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

	versionDir := filepath.Join(paths.Caskroom, "cursor", "2.5.17")
	appPath := filepath.Join(paths.Applications, "Cursor.app")
	binPath := filepath.Join(paths.Bin, "cursor")

	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatalf("mkdir app path: %v", err)
	}
	if err := os.MkdirAll(paths.Bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write bin file: %v", err)
	}
	if err := writeCaskReceipt(versionDir, "cursor", "2.5.17", appPath, []string{binPath}); err != nil {
		t.Fatalf("write receipt: %v", err)
	}

	summary, err := manager.UninstallWithAutoremove(context.Background(), []string{"cursor"})
	if err != nil {
		t.Fatalf("UninstallWithAutoremove: %v", err)
	}
	if len(summary.Removed) != 1 || summary.Removed[0].Name != "cursor" {
		t.Fatalf("removed = %#v", summary.Removed)
	}
	if len(summary.AutoRemove) != 0 {
		t.Fatalf("auto remove should be empty for cask-only uninstall, got %#v", summary.AutoRemove)
	}
}

func TestUninstallCaskLockedRemovesHomeApplicationsFallback(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	paths := Paths{
		Prefix:       filepath.Join(tmp, "ub"),
		Cellar:       filepath.Join(tmp, "ub", "Cellar"),
		Caskroom:     filepath.Join(tmp, "ub", "Caskroom"),
		Cache:        filepath.Join(tmp, "ub", "cache"),
		Bin:          filepath.Join(tmp, "ub", "bin"),
		Sbin:         filepath.Join(tmp, "ub", "sbin"),
		Applications: filepath.Join(tmp, "ub", "Applications"),
	}
	manager := &Manager{Paths: paths}

	versionDir := filepath.Join(paths.Caskroom, "cursor", "2.5.17")
	receiptAppPath := filepath.Join(paths.Applications, "Cursor.app")
	homeAppPath := filepath.Join(home, "Applications", "Cursor.app")

	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir version dir: %v", err)
	}
	if err := os.MkdirAll(homeAppPath, 0o755); err != nil {
		t.Fatalf("mkdir home app path: %v", err)
	}
	payload := filepath.Join(versionDir, "payload.txt")
	if err := os.WriteFile(payload, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := writeCaskReceipt(versionDir, "cursor", "2.5.17", receiptAppPath, nil); err != nil {
		t.Fatalf("write receipt: %v", err)
	}

	if _, err := manager.uninstallCaskLocked("cursor"); err != nil {
		t.Fatalf("uninstallCaskLocked: %v", err)
	}

	if _, err := os.Stat(homeAppPath); !os.IsNotExist(err) {
		t.Fatalf("expected app removed from home Applications, got err=%v", err)
	}
}
