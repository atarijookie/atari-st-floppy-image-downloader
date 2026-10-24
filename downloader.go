package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atarijookie/atari-st-floppy-image-downloader/internal/imagelist"
)

const (
	maxArchiveSize      = 64 << 20
	maxSourceNetErrors  = 5 // that many network errors in a row - the source is down, not used for the rest of the run
	spaceMarginPerImage = 64 << 20
)

var errNoSpace = errors.New("not enough free space")

type options struct {
	out        string
	keepFree   uint64   // bytes to keep free on the disk
	filter     []string // only the images whose name / set / title / content has all these words (lower case)
	limit      int      // at most this many images, 0 = all
	dryRun     bool
	redownload bool // also the images downloaded before whose file is gone
	noIndex    bool
}

type downloader struct {
	opt      options
	list     *imagelist.List
	fetch    *fetcher
	con      *console
	diskFree func(string) (uint64, error)
	sleep    func(context.Context, time.Duration)

	state       *state
	sources     map[string]imagelist.Source
	archivesFor map[string][]*imagelist.Archive // image id -> the archives with it, in the order of the sources
	wanted      map[string]*imagelist.Image     // still to download in this run
	srcErrors   map[string]int                  // network errors in a row per source
	srcDead     map[string]bool
	badArchive  map[*imagelist.Archive]bool
	tmpCount    int

	stats struct {
		listed, had, found, downloaded, failed, conflicts, renamed int
		bytes                                                      int64
	}
}

func newDownloader(opt options, list *imagelist.List, f *fetcher, con *console) *downloader {
	d := &downloader{
		opt: opt, list: list, fetch: f, con: con, diskFree: diskFreeBytes, sleep: sleepCtx,
		sources:     map[string]imagelist.Source{},
		archivesFor: map[string][]*imagelist.Archive{},
		wanted:      map[string]*imagelist.Image{},
		srcErrors:   map[string]int{},
		srcDead:     map[string]bool{},
		badArchive:  map[*imagelist.Archive]bool{},
	}

	order := map[string]int{}
	for i, s := range list.Sources {
		d.sources[s.ID] = s
		order[s.ID] = i
	}
	for i := range list.Archives {
		a := &list.Archives[i]
		for _, e := range a.Contains {
			d.archivesFor[e.Image] = append(d.archivesFor[e.Image], a)
		}
	}
	for id := range d.archivesFor {
		as := d.archivesFor[id]
		sort.SliceStable(as, func(i, j int) bool { return order[as[i].Source] < order[as[j].Source] })
	}
	return d
}

func sleepCtx(ctx context.Context, t time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(t):
	}
}

func (d *downloader) logf(format string, a ...interface{}) {
	d.con.logf(format, a...)
}

// relPath of the image in the output dir, '/' separated
func relPath(img *imagelist.Image) string {
	return path.Join(img.Folder, img.File)
}

func (d *downloader) abs(rel string) string {
	return filepath.Join(d.opt.out, filepath.FromSlash(rel))
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func (d *downloader) run(ctx context.Context) error {
	if err := os.MkdirAll(d.opt.out, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(d.opt.out, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if !lockFile(lock) { // two runs would download the same images and overwrite each other's .downloaded.json
		return fmt.Errorf("another download into %s is running", d.opt.out)
	}

	st, err := loadState(d.opt.out)
	if err != nil {
		return err
	}
	d.state = st

	d.applyRenames()
	todo := d.selectImages()

	if d.opt.dryRun {
		d.reportDryRun(todo)
		return d.state.save()
	}

	if len(todo) > 0 {
		d.logf("%d images to download", len(todo))
	}
	for n, img := range todo {
		if ctx.Err() != nil {
			break
		}
		if d.wanted[img.ID] == nil { // came in an archive with another image
			continue
		}
		err := d.getImage(ctx, img, n+1, len(todo))
		if errors.Is(err, errNoSpace) {
			d.logf("stopped - less than %d MB would stay free on the disk", d.opt.keepFree>>20)
			break
		}
		if err != nil && ctx.Err() == nil {
			d.stats.failed++
			d.logf("[%d/%d] %s: FAILED - %v", n+1, len(todo), img.File, err)
		}
		if err := d.state.saveSometimes(); err != nil {
			return err
		}
	}

	if err := d.state.save(); err != nil {
		return err
	}
	if !d.opt.noIndex {
		if err := writeIndex(d.opt.out, d.list, d.state); err != nil {
			return err
		}
	}

	s := d.stats
	d.logf("%d images in the list (%s): %d here before, %d found (already there), %d downloaded (%d MB), %d failed, %d not overwritten, %d renamed",
		s.listed, d.list.Version, s.had, s.found, s.downloaded, s.bytes>>20, s.failed, s.conflicts, s.renamed)
	if ctx.Err() != nil {
		d.logf("stopped - run it again to go on")
	}
	return nil
}

// applyRenames - an image downloaded before has another name in this list: rename the file (if it's there and the new
// name is free)
func (d *downloader) applyRenames() {
	for i := range d.list.Images {
		img := &d.list.Images[i]
		e, ok := d.state.Images[img.ID]
		newRel := relPath(img)
		if !ok || e.Path == newRel {
			continue
		}
		oldAbs, newAbs := d.abs(e.Path), d.abs(newRel)
		if !exists(oldAbs) || (exists(newAbs) && !strings.EqualFold(e.Path, newRel)) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err == nil && os.Rename(oldAbs, newAbs) == nil {
			d.logf("renamed %s -> %s", e.Path, newRel)
			e.Path = newRel
			d.state.Images[img.ID] = e
			d.state.changes++
			d.stats.renamed++
		}
	}
}

// selectImages - the images of the list to download now; the ones already there (by their content) are only noted
func (d *downloader) selectImages() []*imagelist.Image {
	var todo []*imagelist.Image
	for i := range d.list.Images {
		img := &d.list.Images[i]
		if !d.matches(img) {
			continue
		}
		d.stats.listed++

		if e, ok := d.state.Images[img.ID]; ok {
			if !d.opt.redownload || exists(d.abs(e.Path)) {
				d.stats.had++
				continue
			}
		} else if target := d.abs(relPath(img)); exists(target) {
			if sum, err := fileSHA256(target); err == nil && sum == img.ID { // e.g. the state file was lost
				d.state.add(img.ID, relPath(img), "found")
				d.stats.found++
			} else {
				d.logf("%s: a different file with this name is there, not overwritten", relPath(img))
				d.stats.conflicts++
			}
			continue
		}

		if len(d.archivesFor[img.ID]) == 0 {
			continue
		}
		todo = append(todo, img)
	}
	if d.opt.limit > 0 && len(todo) > d.opt.limit {
		todo = todo[:d.opt.limit]
	}
	for _, img := range todo {
		d.wanted[img.ID] = img
	}
	return todo
}

func (d *downloader) matches(img *imagelist.Image) bool {
	if len(d.opt.filter) == 0 {
		return true
	}
	var sb strings.Builder
	sb.WriteString(img.File + " " + img.Set + " " + img.Title)
	for _, c := range img.Content {
		sb.WriteString(" " + c.Name)
	}
	text := strings.ToLower(sb.String())
	for _, w := range d.opt.filter {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func (d *downloader) reportDryRun(todo []*imagelist.Image) {
	var size int64
	perSource := map[string]int{}
	for _, img := range todo {
		size += img.Size
		perSource[d.archivesFor[img.ID][0].Source]++
	}
	d.logf("%d images in the list (%s): %d here before, %d found (already there), %d to download (%d MB unpacked), %d not overwritten",
		d.stats.listed, d.list.Version, d.stats.had, d.stats.found, len(todo), size>>20, d.stats.conflicts)
	for _, s := range d.list.Sources {
		if perSource[s.ID] > 0 {
			d.logf("  %d first tried from %s (%s)", perSource[s.ID], s.Name, s.Base)
		}
	}
}

// getImage tries the archives with the image, in the order of the sources, until one works
func (d *downloader) getImage(ctx context.Context, img *imagelist.Image, n, total int) error {
	var lastErr error
	tried := 0
	for _, a := range d.archivesFor[img.ID] {
		if d.badArchive[a] || d.srcDead[a.Source] {
			continue
		}
		tried++
		if err := d.checkSpace(a, img); err != nil {
			return err
		}

		d.con.startFile(n, total, img.File, a.Size)
		got, err := d.getArchive(ctx, a)
		d.con.endFile()
		for _, g := range got {
			d.logf("[%d/%d] %s  <- %s", n, total, g, d.sources[a.Source].URL(a.Path))
		}
		if d.wanted[img.ID] == nil { // got it
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			err = badFile("%s isn't in it", img.File)
		}
		lastErr = fmt.Errorf("%s %s: %v", a.Source, a.Path, err)

		var netErr *networkError
		if errors.As(err, &netErr) {
			d.srcErrors[a.Source]++
			if d.srcErrors[a.Source] >= maxSourceNetErrors {
				d.srcDead[a.Source] = true
				d.logf("%s: %d network errors in a row, not used for the rest of this run (%v)", a.Source, d.srcErrors[a.Source], err)
			}
		} else {
			d.srcErrors[a.Source] = 0
			d.badArchive[a] = true
		}
	}
	if tried == 0 {
		return errors.New("no working source has it (all failed in this run)")
	}
	return lastErr
}

func (d *downloader) checkSpace(a *imagelist.Archive, img *imagelist.Image) error {
	free, err := d.diskFree(d.opt.out)
	if err != nil {
		return nil // can't tell - don't stop because of that
	}
	need := d.opt.keepFree + uint64(a.Size) + uint64(img.Size) + spaceMarginPerImage
	if free < need {
		return errNoSpace
	}
	return nil
}

// getArchive downloads the archive and stores the wanted images from it, returns the stored ones
func (d *downloader) getArchive(ctx context.Context, a *imagelist.Archive) ([]string, error) {
	src := d.sources[a.Source]
	d.tmpCount++
	tmp := filepath.Join(d.opt.out, fmt.Sprintf(".download-%d.part", d.tmpCount))
	defer os.Remove(tmp)

	err := d.fetch.toFile(ctx, src.URL(a.Path), tmp, maxArchiveSize, d.con.progress)
	if !strings.HasPrefix(src.Base, "file:") { // be nice to the source, also after an error - not needed for a local folder
		d.sleep(ctx, time.Duration(src.PauseMs)*time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	if err := checkArchiveFile(tmp, a); err != nil {
		return nil, err
	}

	if a.Type == "image" {
		f, err := os.Open(tmp)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		img := d.wanted[a.Contains[0].Image]
		if img == nil {
			return nil, nil
		}
		if err := d.store(f, img, a.Source); err != nil {
			return nil, err
		}
		return []string{relPath(img)}, nil
	}

	zr, err := zip.OpenReader(tmp)
	if err != nil {
		return nil, badFile("not a zip file: %v", err)
	}
	defer zr.Close()

	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	var got []string
	var firstErr error
	for _, e := range a.Contains { // all the wanted images in it - not just the one it was downloaded for
		img := d.wanted[e.Image]
		if img == nil {
			continue
		}
		err := d.storeFromZip(files[e.Name], img, a.Source)
		if err == nil {
			got = append(got, relPath(img))
		} else if firstErr == nil {
			firstErr = fmt.Errorf("%s: %v", e.Name, err)
		}
	}
	return got, firstErr
}

func (d *downloader) storeFromZip(f *zip.File, img *imagelist.Image, source string) error {
	if f == nil {
		return badFile("not in the zip")
	}
	rc, err := f.Open()
	if err != nil {
		return badFile("can't unpack: %v", err)
	}
	defer rc.Close()
	return d.store(rc, img, source)
}

// store writes the image from r, if it has the right size and hash; never over an existing file
func (d *downloader) store(r io.Reader, img *imagelist.Image, source string) error {
	rel := relPath(img)
	target := d.abs(rel)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(target), "."+img.File+".part")
	defer os.Remove(tmp)

	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(r, img.Size+1))
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		return badFile("can't unpack: %v", copyErr)
	case closeErr != nil:
		return closeErr
	case n != img.Size:
		return badFile("wrong size (%d, the list says %d)", n, img.Size)
	case hex.EncodeToString(h.Sum(nil)) != img.ID:
		return badFile("wrong content (hash doesn't match the list)")
	}

	if exists(target) {
		d.stats.conflicts++
		return fmt.Errorf("a file with this name appeared meanwhile, not overwritten")
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}

	d.state.add(img.ID, rel, source)
	delete(d.wanted, img.ID)
	d.stats.downloaded++
	d.stats.bytes += img.Size
	return nil
}

// checkArchiveFile - size and hash of the downloaded archive, if the list has them
func checkArchiveFile(file string, a *imagelist.Archive) error {
	if a.Size == 0 && a.SHA256 == "" && a.SHA512 == "" {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	var hs []hash.Hash
	var want []string
	if a.SHA256 != "" {
		hs, want = append(hs, sha256.New()), append(want, a.SHA256)
	}
	if a.SHA512 != "" {
		hs, want = append(hs, sha512.New()), append(want, a.SHA512)
	}
	writers := make([]io.Writer, len(hs))
	for i, h := range hs {
		writers[i] = h
	}
	n, err := io.Copy(io.MultiWriter(append(writers, io.Discard)...), f)
	if err != nil {
		return err
	}
	if a.Size != 0 && n != a.Size {
		return badFile("wrong size (%d, the list says %d) - the source has another file now", n, a.Size)
	}
	for i, h := range hs {
		if hex.EncodeToString(h.Sum(nil)) != want[i] {
			return badFile("wrong hash - the source has another file now")
		}
	}
	return nil
}

func fileSHA256(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
