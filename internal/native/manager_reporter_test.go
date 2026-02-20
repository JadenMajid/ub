package native

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ub/internal/homebrewapi"
)

func TestInstallReporterPlanOutput(t *testing.T) {
	closure := map[string]homebrewapi.Formula{
		"ffmpeg": {Name: "ffmpeg"},
		"lame":   {Name: "lame"},
		"opus":   {Name: "opus"},
	}
	r := newInstallReporter(Paths{}, []string{"ffmpeg"}, closure)

	out := captureStdout(t, func() {
		r.printPlan()
	})

	if !strings.Contains(out, "==> Fetching downloads for: ffmpeg") {
		t.Fatalf("missing fetching line: %q", out)
	}
	if !strings.Contains(out, "==> Installing dependencies for ffmpeg:") {
		t.Fatalf("missing dependencies line: %q", out)
	}
	if !strings.Contains(out, "lame") || !strings.Contains(out, "opus") {
		t.Fatalf("missing dependency names: %q", out)
	}
}

func TestInstallReporterSummaryOutput(t *testing.T) {
	tmp := t.TempDir()
	paths := Paths{Cellar: filepath.Join(tmp, "Cellar")}
	installDir := filepath.Join(paths.Cellar, "ffmpeg", "8.0.1")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	r := newInstallReporter(paths, []string{"ffmpeg"}, map[string]homebrewapi.Formula{"ffmpeg": {Name: "ffmpeg"}})
	out := captureStdout(t, func() {
		r.printPoured("ffmpeg", "8.0.1")
		r.printSummary()
	})

	if !strings.Contains(out, "🍺") {
		t.Fatalf("missing poured line: %q", out)
	}
	if !strings.Contains(out, "==> Summary") {
		t.Fatalf("missing summary header: %q", out)
	}
	if !strings.Contains(out, "- ffmpeg") {
		t.Fatalf("missing summary entry: %q", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return string(data)
}
