package main

import (
	"reflect"
	"testing"

	"ub/internal/native"
)

func TestUninstallSummaryLines_NoAutoremove(t *testing.T) {
	summary := native.UninstallSummary{
		Removed: []native.UninstallRecord{
			{Name: "ffmpeg", Path: "/Users/jaden/ub/Cellar/ffmpeg/8.0.1_4", Files: 284, SizeHuman: "53.3MB"},
		},
	}
	got := uninstallSummaryLines(summary)
	want := []string{
		"Uninstalling /Users/jaden/ub/Cellar/ffmpeg/8.0.1_4... (284 files, 53.3MB)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uninstallSummaryLines() mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestUninstallSummaryLines_WithAutoremove(t *testing.T) {
	summary := native.UninstallSummary{
		Removed: []native.UninstallRecord{
			{Name: "ffmpeg", Path: "/Users/jaden/ub/Cellar/ffmpeg/8.0.1_4", Files: 284, SizeHuman: "53.3MB"},
		},
		AutoRemove: []native.UninstallRecord{
			{Name: "lame", Path: "/Users/jaden/ub/Cellar/lame/3.100", Files: 28, SizeHuman: "2.3MB"},
			{Name: "opus", Path: "/Users/jaden/ub/Cellar/opus/1.6.1", Files: 16, SizeHuman: "1.1MB"},
		},
	}
	got := uninstallSummaryLines(summary)
	want := []string{
		"Uninstalling /Users/jaden/ub/Cellar/ffmpeg/8.0.1_4... (284 files, 53.3MB)",
		"==> Autoremoving 2 unneeded formulae:",
		"lame",
		"opus",
		"Uninstalling /Users/jaden/ub/Cellar/lame/3.100... (28 files, 2.3MB)",
		"Uninstalling /Users/jaden/ub/Cellar/opus/1.6.1... (16 files, 1.1MB)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uninstallSummaryLines() mismatch\n got: %#v\nwant: %#v", got, want)
	}
}
