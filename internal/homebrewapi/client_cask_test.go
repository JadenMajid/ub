package homebrewapi

import (
	"encoding/json"
	"testing"
)

func TestCaskAppArtifact(t *testing.T) {
	c := Cask{
		Artifacts: []map[string]json.RawMessage{
			{"app": json.RawMessage(`[
				"Cursor.app"
			]`)},
		},
	}

	if got := c.AppArtifact(); got != "Cursor.app" {
		t.Fatalf("AppArtifact() = %q, want %q", got, "Cursor.app")
	}
}

func TestCaskBinaryArtifacts(t *testing.T) {
	c := Cask{
		Artifacts: []map[string]json.RawMessage{
			{"binary": json.RawMessage(`[
				"$APPDIR/Cursor.app/Contents/Resources/app/bin/code",
				{"target":"cursor"}
			]`)},
		},
	}

	bins := c.BinaryArtifacts()
	if len(bins) != 1 {
		t.Fatalf("BinaryArtifacts() len = %d, want 1", len(bins))
	}
	if bins[0].Source != "$APPDIR/Cursor.app/Contents/Resources/app/bin/code" {
		t.Fatalf("source = %q", bins[0].Source)
	}
	if bins[0].Target != "cursor" {
		t.Fatalf("target = %q", bins[0].Target)
	}
}

func TestCaskAppArtifactMissing(t *testing.T) {
	c := Cask{Artifacts: []map[string]json.RawMessage{{"binary": json.RawMessage(`[
		"$APPDIR/Foo.app/Contents/MacOS/foo"
	]`)}}}
	if got := c.AppArtifact(); got != "" {
		t.Fatalf("AppArtifact() = %q, want empty", got)
	}
}

func TestCaskBinaryArtifactsWithoutTarget(t *testing.T) {
	c := Cask{
		Artifacts: []map[string]json.RawMessage{
			{"binary": json.RawMessage(`[
				"$APPDIR/Cursor.app/Contents/Resources/app/bin/code"
			]`)},
		},
	}

	bins := c.BinaryArtifacts()
	if len(bins) != 1 {
		t.Fatalf("BinaryArtifacts() len = %d, want 1", len(bins))
	}
	if bins[0].Target != "" {
		t.Fatalf("target = %q, want empty", bins[0].Target)
	}
}
