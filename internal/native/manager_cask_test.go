package native

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsNotFoundError(t *testing.T) {
	err := fmt.Errorf("download failed: unexpected status 404")
	if !isNotFoundError(err) {
		t.Fatal("expected true for 404 error")
	}
	if isNotFoundError(nil) {
		t.Fatal("expected false for nil error")
	}
}

func TestIsZipArchive(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "a.src")
	if err := os.WriteFile(zipPath, []byte{'P', 'K', 0x03, 0x04, 0x00}, 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	tarPath := filepath.Join(tmp, "b.src")
	if err := os.WriteFile(tarPath, []byte{0x1f, 0x8b, 0x08, 0x00}, 0o644); err != nil {
		t.Fatalf("write tar: %v", err)
	}

	isZip, err := isZipArchive(zipPath)
	if err != nil {
		t.Fatalf("isZipArchive(zip): %v", err)
	}
	if !isZip {
		t.Fatal("expected zip header to be detected")
	}

	isZip, err = isZipArchive(tarPath)
	if err != nil {
		t.Fatalf("isZipArchive(tar): %v", err)
	}
	if isZip {
		t.Fatal("expected non-zip header to be false")
	}
}

func TestFindFileInTree(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "Cursor.app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	found, err := findFileInTree(root, "Cursor.app")
	if err != nil {
		t.Fatalf("findFileInTree: %v", err)
	}
	if found != nested {
		t.Fatalf("found = %q, want %q", found, nested)
	}
}

func TestFindFileInTreeNotFound(t *testing.T) {
	root := t.TempDir()
	if _, err := findFileInTree(root, "missing.app"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestIsNotFoundErrorFalseOnOtherStatus(t *testing.T) {
	err := fmt.Errorf("download failed: unexpected status 500")
	if isNotFoundError(err) {
		t.Fatal("expected false for non-404 error")
	}
}
