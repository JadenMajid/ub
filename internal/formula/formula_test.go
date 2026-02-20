package formula

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveClosure(t *testing.T) {
	tap := t.TempDir()

	if err := os.WriteFile(filepath.Join(tap, "a.json"), []byte(`{
  "name": "a",
  "version": "1.0.0"
}`), 0o644); err != nil {
		t.Fatalf("write a formula: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tap, "b.json"), []byte(`{
  "name": "b",
  "version": "1.0.0",
  "deps": ["a"]
}`), 0o644); err != nil {
		t.Fatalf("write b formula: %v", err)
	}

	all, err := ResolveClosure(tap, []string{"b"})
	if err != nil {
		t.Fatalf("resolve closure: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("expected 2 formulas, got %d", len(all))
	}
}
