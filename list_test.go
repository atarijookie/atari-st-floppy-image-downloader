package main

import (
	"os"
	"testing"

	"github.com/atarijookie/atari-st-floppy-image-downloader/internal/imagelist"
)

// the committed list (published with every release) must be valid
func TestCommittedList(t *testing.T) {
	data, err := os.ReadFile("list/imagelist.json.gz")
	if err != nil {
		t.Skipf("no list: %v", err)
	}
	l, err := imagelist.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Images) == 0 || len(l.Archives) == 0 {
		t.Fatalf("empty list: %d images, %d archives", len(l.Images), len(l.Archives))
	}
}
