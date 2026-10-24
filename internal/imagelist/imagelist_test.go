package imagelist

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"testing"
)

const id1 = "1111111111111111111111111111111111111111111111111111111111111111"
const id2 = "2222222222222222222222222222222222222222222222222222222222222222"

func valid() *List {
	return &List{
		Format: 1, Version: "v",
		Sources:  []Source{{ID: "s", Base: "https://example.org/x/"}},
		Images:   []Image{{ID: id1, File: "A_Game.msa", Size: 10}, {ID: id2, File: "B.st", Folder: "menus/Pompey", Size: 10}},
		Archives: []Archive{{Source: "s", Path: "a b/1.zip", Contains: []Entry{{Name: "A.MSA", Image: id1}, {Name: "B.ST", Image: id2}}}},
	}
}

func TestParseGzipAndPlain(t *testing.T) {
	data, _ := json.Marshal(valid())
	if _, err := Parse(data); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(data)
	zw.Close()
	l, err := Parse(buf.Bytes())
	if err != nil || len(l.Images) != 2 {
		t.Fatalf("gzip: %v", err)
	}
	if _, err := Parse([]byte("2026-08-14\nhttp://x/a.zip,0x0000,Game\n")); err == nil { // the old CSV list
		t.Error("CSV accepted")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(l *List){
		"newer format":          func(l *List) { l.Format = 2 },
		"path in file name":     func(l *List) { l.Images[0].File = "../evil.msa" },
		"backslash":             func(l *List) { l.Images[0].File = `a\b.msa` },
		"windows reserved":      func(l *List) { l.Images[0].File = "CON.st" },
		"colon":                 func(l *List) { l.Images[0].File = "C:x.st" },
		"hidden":                func(l *List) { l.Images[0].File = ".x.st" },
		"not an image":          func(l *List) { l.Images[0].File = "x.exe" },
		"same name other case":  func(l *List) { l.Images[1].File, l.Images[1].Folder = "a_game.MSA", "" },
		"folder with ..":        func(l *List) { l.Images[1].Folder = "menus/../.." },
		"absolute folder":       func(l *List) { l.Images[1].Folder = "/etc" },
		"bad id":                func(l *List) { l.Images[0].ID = "abc" },
		"upper case id":         func(l *List) { l.Images[0].ID = strings.Repeat("AB", 32) },
		"repeated id":           func(l *List) { l.Images[1].ID = id1 },
		"unknown source":        func(l *List) { l.Archives[0].Source = "x" },
		"unknown image":         func(l *List) { l.Archives[0].Contains[0].Image = strings.Repeat("3", 64) },
		"archive path ..":       func(l *List) { l.Archives[0].Path = "../x.zip" },
		"absolute archive path": func(l *List) { l.Archives[0].Path = "/x.zip" },
		"bad source URL":        func(l *List) { l.Sources[0].Base = "ftp://x/" },
		"bad archive hash":      func(l *List) { l.Archives[0].SHA256 = "xyz" },
		"image archive with 2":  func(l *List) { l.Archives[0].Type = "image" },
		"zero size":             func(l *List) { l.Images[0].Size = 0 },
	}
	for name, change := range cases {
		l := valid()
		change(l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
}

func TestSourceURLEscapesPath(t *testing.T) {
	s := Source{Base: "https://example.org/x/"}
	if got := s.URL("a b/A#1.zip"); got != "https://example.org/x/a%20b/A%231.zip" {
		t.Errorf("got %s", got)
	}
}
