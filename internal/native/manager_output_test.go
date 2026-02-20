package native

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJoinWithAnd(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{name: "empty", in: nil, want: ""},
		{name: "one", in: []string{"lame"}, want: "lame"},
		{name: "two", in: []string{"lame", "opus"}, want: "lame and opus"},
		{name: "many", in: []string{"lame", "libvpx", "opus"}, want: "lame, libvpx and opus"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinWithAnd(tc.in); got != tc.want {
				t.Fatalf("joinWithAnd() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBottleFilename(t *testing.T) {
	got := bottleFilename("https://example.com/path/ffmpeg--8.0.1.arm64_sonoma.bottle.tar.gz")
	want := "ffmpeg--8.0.1.arm64_sonoma.bottle.tar.gz"
	if got != want {
		t.Fatalf("bottleFilename() = %q, want %q", got, want)
	}
}

func TestHomebrewBottleFilename(t *testing.T) {
	got := homebrewBottleFilename("ffmpeg", "8.0.1", "arm64_sonoma", "https://example.com/blob/sha256:abc")
	want := "ffmpeg--8.0.1.arm64_sonoma.bottle.tar.gz"
	if got != want {
		t.Fatalf("homebrewBottleFilename() = %q, want %q", got, want)
	}
}

func TestFormatSize(t *testing.T) {
	if got := formatSize(792 * 1024); got != "792.0KB" {
		t.Fatalf("formatSize() = %q, want 792.0KB", got)
	}
	if got := formatSize(21 * 1024 * 1024); got != "21.0MB" {
		t.Fatalf("formatSize() = %q, want 21.0MB", got)
	}
}

func TestDirStats(t *testing.T) {
	tmpDir := t.TempDir()
	a := filepath.Join(tmpDir, "a.txt")
	bDir := filepath.Join(tmpDir, "bin")
	b := filepath.Join(bDir, "b.txt")
	if err := os.WriteFile(a, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.MkdirAll(bDir, 0o755); err != nil {
		t.Fatalf("mkdir b dir: %v", err)
	}
	if err := os.WriteFile(b, []byte("world!"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}

	files, size, err := dirStats(tmpDir)
	if err != nil {
		t.Fatalf("dirStats() error: %v", err)
	}
	if files != 2 {
		t.Fatalf("dirStats() files = %d, want 2", files)
	}
	if size != int64(len("hello")+len("world!")) {
		t.Fatalf("dirStats() size = %d, want %d", size, len("hello")+len("world!"))
	}
}

func TestRenderProgressBarKnownTotal(t *testing.T) {
	got := renderProgressBar(50, 100, 0, 10)
	want := "[=====-----]"
	if got != want {
		t.Fatalf("renderProgressBar() = %q, want %q", got, want)
	}
}

func TestRenderProgressBarUnknownTotalMoves(t *testing.T) {
	got := renderProgressBar(0, 0, 2, 6)
	want := "[-->---]"
	if got != want {
		t.Fatalf("renderProgressBar() = %q, want %q", got, want)
	}
}

func TestFormatTransferRate(t *testing.T) {
	if got := formatTransferRate(0); got != "--" {
		t.Fatalf("formatTransferRate(0) = %q, want --", got)
	}
	if got := formatTransferRate(2 * 1024 * 1024); got != "2.0MB/s" {
		t.Fatalf("formatTransferRate(2MB/s) = %q, want 2.0MB/s", got)
	}
}

func TestRemoveTreeWithProgress(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	files := []string{
		filepath.Join(root, "one.txt"),
		filepath.Join(nested, "two.txt"),
	}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file %q: %v", file, err)
		}
	}

	callbackCount := 0
	lastRemoved := -1
	lastTotal := -1
	lastDone := false
	err := removeTreeWithProgress(root, func(removed, total int, done bool) {
		callbackCount++
		lastRemoved = removed
		lastTotal = total
		lastDone = done
	})
	if err != nil {
		t.Fatalf("removeTreeWithProgress() error: %v", err)
	}
	if callbackCount == 0 {
		t.Fatal("expected progress callback")
	}
	if !lastDone {
		t.Fatal("expected final callback done=true")
	}
	if lastTotal != len(files) {
		t.Fatalf("total = %d, want %d", lastTotal, len(files))
	}
	if lastRemoved != len(files) {
		t.Fatalf("removed = %d, want %d", lastRemoved, len(files))
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("expected root removed, stat err = %v", statErr)
	}
}
