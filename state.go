package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The images downloaded before, in <out>/.downloaded.json - by their id, as the files change (the ST writes to them)
// and can be renamed by a newer list. An image which is here is not downloaded again, even if its file was deleted
// (unless -redownload).

const stateFileName = ".downloaded.json"

type stateEntry struct {
	Path   string `json:"path"`   // relative to the output dir, '/' as separator
	Source string `json:"source"` // where it came from ("found" = was there already)
	Time   string `json:"time"`
}

type state struct {
	Format int                   `json:"format"`
	Images map[string]stateEntry `json:"images"`

	file    string
	changes int
}

func loadState(dir string) (*state, error) {
	s := &state{Format: 1, Images: map[string]stateEntry{}, file: filepath.Join(dir, stateFileName)}

	data, err := os.ReadFile(s.file)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("%s is broken (%v) - delete it, the images there are then found by their content", s.file, err)
	}
	if s.Images == nil {
		s.Images = map[string]stateEntry{}
	}
	return s, nil
}

func (s *state) add(id, path, source string) {
	s.Images[id] = stateEntry{Path: path, Source: source, Time: time.Now().UTC().Format(time.RFC3339)}
	s.changes++
}

// save - after every few changes, not after each one: the file has thousands of entries. If the program is killed
// before, the images stored since are found again by their content on the next run.
func (s *state) saveSometimes() error {
	if s.changes < 20 {
		return nil
	}
	return s.save()
}

func (s *state) save() error {
	data, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	tmp := s.file + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.file); err != nil {
		return err
	}
	s.changes = 0
	return nil
}
