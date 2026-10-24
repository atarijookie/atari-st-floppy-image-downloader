// Package imagelist reads the list of the floppy images (imagelist.json.gz) - format in docs/imagelist.md.
package imagelist

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

const Format = 1

type List struct {
	Format   int       `json:"format"`
	Version  string    `json:"version"`
	Sources  []Source  `json:"sources"`
	Images   []Image   `json:"images"`
	Archives []Archive `json:"archives"`
}

// Source - a site the archives are downloaded from; the order in the list is the order they are tried in
type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Base    string `json:"base"`    // URL of the archive = Base + Archive.Path
	PauseMs int    `json:"pauseMs"` // between the requests to this source
}

// Image - what ends up on the disk
type Image struct {
	ID        string            `json:"id"` // SHA-256 of the image file (.st / .msa), lower case hex
	File      string            `json:"file"`
	Folder    string            `json:"folder,omitempty"` // relative, '/' as separator, "" = the output dir itself
	Size      int64             `json:"size"`
	Set       string            `json:"set,omitempty"`
	Title     string            `json:"title,omitempty"`
	Condition string            `json:"condition,omitempty"`
	Content   []Item            `json:"content,omitempty"`
	Links     map[string]string `json:"links,omitempty"`
}

// Item - a game / software / ... on the image
type Item struct {
	Name string `json:"name"`
	Kind string `json:"kind"`          // game, software, other
	Tag  string `json:"tag,omitempty"` // e.g. [doc] - only the docs of it are on the disk
}

// Archive - a file on a source, with images in it
type Archive struct {
	Source   string  `json:"source"`
	Path     string  `json:"path"`
	Type     string  `json:"type,omitempty"` // "zip" (default) or "image" - the file is the image itself
	SHA256   string  `json:"sha256,omitempty"`
	SHA512   string  `json:"sha512,omitempty"`
	Size     int64   `json:"size,omitempty"`
	Contains []Entry `json:"contains"`
}

type Entry struct {
	Name  string `json:"name"`  // in the archive
	Image string `json:"image"` // Image.ID
}

// Parse reads the list - gzipped or not - and checks it
func Parse(data []byte) (*List, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if data, err = io.ReadAll(io.LimitReader(zr, 256<<20)); err != nil {
			return nil, fmt.Errorf("unpacking the list: %v", err)
		}
	}

	var l List
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("not an image list: %v", err)
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &l, nil
}

// Validate checks everything the downloader relies on - file names and paths especially, as they come from the internet
func (l *List) Validate() error {
	if l.Format != Format {
		return fmt.Errorf("list format %d, this downloader knows format %d - get a newer downloader", l.Format, Format)
	}

	sources := map[string]bool{}
	for _, s := range l.Sources {
		if s.ID == "" || sources[s.ID] {
			return fmt.Errorf("source %q: empty or repeated id", s.ID)
		}
		u, err := url.Parse(s.Base)
		if err != nil || !(u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "file") {
			return fmt.Errorf("source %s: bad base URL %q", s.ID, s.Base)
		}
		sources[s.ID] = true
	}

	images := map[string]bool{}
	paths := map[string]string{}
	for _, img := range l.Images {
		if !isHex(img.ID, 64) || images[img.ID] {
			return fmt.Errorf("image %q: bad or repeated id", img.ID)
		}
		if !SafeName(img.File) || !IsImageName(img.File) {
			return fmt.Errorf("image %s: bad file name %q", img.ID, img.File)
		}
		if !SafeFolder(img.Folder) {
			return fmt.Errorf("image %s: bad folder %q", img.ID, img.Folder)
		}
		p := strings.ToLower(path.Join(img.Folder, img.File)) // the same name in another case is the same file on Windows / macOS
		if other, ok := paths[p]; ok {
			return fmt.Errorf("images %s and %s: the same file %s", other, img.ID, p)
		}
		if img.Size <= 0 {
			return fmt.Errorf("image %s: bad size %d", img.ID, img.Size)
		}
		images[img.ID] = true
		paths[p] = img.ID
	}

	for _, a := range l.Archives {
		if !sources[a.Source] {
			return fmt.Errorf("archive %s: unknown source %q", a.Path, a.Source)
		}
		if a.Path == "" || strings.HasPrefix(a.Path, "/") || strings.Contains(a.Path, "\\") {
			return fmt.Errorf("archive %q: bad path", a.Path)
		}
		for _, part := range strings.Split(a.Path, "/") {
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("archive %q: bad path", a.Path)
			}
		}
		if a.Type != "" && a.Type != "zip" && a.Type != "image" {
			return fmt.Errorf("archive %s: unknown type %q", a.Path, a.Type)
		}
		if (a.SHA256 != "" && !isHex(a.SHA256, 64)) || (a.SHA512 != "" && !isHex(a.SHA512, 128)) {
			return fmt.Errorf("archive %s: bad hash", a.Path)
		}
		if len(a.Contains) == 0 || (a.Type == "image" && len(a.Contains) != 1) {
			return fmt.Errorf("archive %s: bad list of images in it", a.Path)
		}
		for _, e := range a.Contains {
			if !images[e.Image] {
				return fmt.Errorf("archive %s: unknown image %s", a.Path, e.Image)
			}
		}
	}
	return nil
}

// URL of the archive on its source - the path escaped
func (s Source) URL(archivePath string) string {
	parts := strings.Split(archivePath, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return s.Base + strings.Join(parts, "/")
}

// SafeName - just a file name, valid on Windows, macOS and Linux
func SafeName(name string) bool {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") ||
		strings.HasSuffix(name, " ") || strings.ContainsAny(name, `/\:*?"<>|`) {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return true
}

// SafeFolder - relative, '/' separated, each part a SafeName
func SafeFolder(folder string) bool {
	if folder == "" {
		return true
	}
	for _, part := range strings.Split(folder, "/") {
		if !SafeName(part) {
			return false
		}
	}
	return true
}

// IsImageName - .st / .msa, any case
func IsImageName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".st" || ext == ".msa"
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
