package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/atarijookie/atari-st-floppy-image-downloader/internal/imagelist"
)

// index.html (search in a browser, works offline) and games.txt (for grep / Ctrl+F) in the output dir - which game is
// on which image, for the images which are there.

//go:embed index_template.html
var indexTemplate string

type indexEntry struct {
	File      string   `json:"f"`
	Set       string   `json:"s,omitempty"`
	Title     string   `json:"t,omitempty"`
	Condition string   `json:"c,omitempty"`
	Games     []string `json:"g,omitempty"`
	Other     []string `json:"o,omitempty"` // software, docs, ...
	Link      string   `json:"l,omitempty"`
}

func indexEntries(dir string, list *imagelist.List, st *state) []indexEntry {
	var entries []indexEntry
	for i := range list.Images {
		img := &list.Images[i]
		e, ok := st.Images[img.ID]
		if !ok || !exists(filepath.Join(dir, filepath.FromSlash(e.Path))) {
			continue
		}
		ie := indexEntry{File: e.Path, Set: img.Set, Title: img.Title, Condition: img.Condition, Link: img.Links["atarilegend"]}
		for _, c := range img.Content {
			name := c.Name
			if c.Tag != "" {
				name += " " + c.Tag
			}
			if c.Kind == "game" && c.Tag == "" {
				ie.Games = append(ie.Games, name)
			} else {
				ie.Other = append(ie.Other, name)
			}
		}
		entries = append(entries, ie)
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].File) < strings.ToLower(entries[j].File) })
	return entries
}

func writeIndex(dir string, list *imagelist.List, st *state) error {
	entries := indexEntries(dir, list, st)

	data, err := json.Marshal(entries) // escapes <, > and & - safe inside <script>
	if err != nil {
		return err
	}
	page := strings.Replace(indexTemplate, "/*DATA*/[]", string(data), 1)
	page = strings.Replace(page, "{{VERSION}}", htmlEscape(list.Version), -1)
	if err := writeFileAtomic(filepath.Join(dir, "index.html"), []byte(page)); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "games.txt"), []byte(gamesText(list, entries)))
}

// gamesText - one line per game / software on an image, sorted by its name: "name  ->  image (set / title)"
func gamesText(list *imagelist.List, entries []indexEntry) string {
	type line struct{ name, rest string }
	var lines []line
	for _, e := range entries {
		where := e.File
		if e.Title != "" {
			where += "   (" + e.Title + ")"
		}
		names := append(append([]string{}, e.Games...), e.Other...)
		if len(names) == 0 {
			names = []string{"(content unknown)"}
		}
		for _, n := range names {
			lines = append(lines, line{n, where})
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return strings.ToLower(lines[i].name) < strings.ToLower(lines[j].name) })

	var sb strings.Builder
	fmt.Fprintf(&sb, "Atari ST floppy images - list %s, %d images here. Search for a game, the image it is on is after the arrow.\r\n", list.Version, len(entries))
	fmt.Fprintf(&sb, "index.html next to this file has the same, with a search box.\r\n\r\n")
	for _, l := range lines {
		fmt.Fprintf(&sb, "%-50s ->  %s\r\n", l.name, l.rest) // \r\n - readable in Notepad too
	}
	return sb.String()
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func writeFileAtomic(file string, data []byte) error {
	tmp := file + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
