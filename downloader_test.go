package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atarijookie/atari-st-floppy-image-downloader/internal/imagelist"
)

func imageData(fill byte, size int) []byte {
	return bytes.Repeat([]byte{fill}, size)
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fake sources: /<source>/<path>, counts the requests
type fakeServer struct {
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
	down  map[string]bool // source answers 503
	srv   *httptest.Server
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{files: map[string][]byte{}, hits: map[string]int{}, down: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits[r.URL.Path]++
		src := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
		if f.down[src] {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		data, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) hit(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[p]
}

// test list: images a, b (in one zip), c (plain image file), d (only on the second source)
type fixture struct {
	t    *testing.T
	srv  *fakeServer
	out  string
	data map[string][]byte // image name -> content
	list *imagelist.List
}

func newFixture(t *testing.T) *fixture {
	fx := &fixture{t: t, srv: newFakeServer(t), out: filepath.Join(t.TempDir(), "floppyImages"),
		data: map[string][]byte{"a": imageData(1, 1024), "b": imageData(2, 2048), "c": imageData(3, 512), "d": imageData(4, 512)}}

	ab := zipOf(t, map[string][]byte{"A.MSA": fx.data["a"], "dir/B.ST": fx.data["b"], "readme.txt": []byte("hi")})
	d := zipOf(t, map[string][]byte{"D.ST": fx.data["d"]})
	fx.srv.files["/one/ab.zip"] = ab
	fx.srv.files["/one/c.st"] = fx.data["c"]
	fx.srv.files["/two/ab.zip"] = ab
	fx.srv.files["/two/d disk.zip"] = d

	base := fx.srv.srv.URL
	fx.list = &imagelist.List{
		Format: 1, Version: "test.1",
		Sources: []imagelist.Source{{ID: "one", Name: "One", Base: base + "/one/"}, {ID: "two", Name: "Two", Base: base + "/two/"}},
		Images: []imagelist.Image{
			{ID: sum(fx.data["a"]), File: "A_Arkanoid_Joust.msa", Size: 1024, Set: "Automation", Title: "Automation #1",
				Content: []imagelist.Item{{Name: "Arkanoid", Kind: "game"}, {Name: "Joust", Kind: "game"}, {Name: "Joust", Kind: "game", Tag: "[doc]"}}},
			{ID: sum(fx.data["b"]), File: "B_Barbarian.st", Size: 2048, Content: []imagelist.Item{{Name: "Barbarian <script>", Kind: "game"}}},
			{ID: sum(fx.data["c"]), File: "C.st", Folder: "sub/dir", Size: 512},
			{ID: sum(fx.data["d"]), File: "D_Dungeon-Master.st", Size: 512, Content: []imagelist.Item{{Name: "Dungeon Master", Kind: "game"}}},
		},
		Archives: []imagelist.Archive{
			{Source: "one", Path: "ab.zip", SHA256: sum(ab), Size: int64(len(ab)), Contains: []imagelist.Entry{
				{Name: "A.MSA", Image: sum(fx.data["a"])}, {Name: "dir/B.ST", Image: sum(fx.data["b"])}}},
			{Source: "two", Path: "ab.zip", Contains: []imagelist.Entry{
				{Name: "A.MSA", Image: sum(fx.data["a"])}, {Name: "dir/B.ST", Image: sum(fx.data["b"])}}},
			{Source: "one", Path: "c.st", Type: "image", Contains: []imagelist.Entry{{Name: "c.st", Image: sum(fx.data["c"])}}},
			{Source: "two", Path: "d disk.zip", Contains: []imagelist.Entry{{Name: "D.ST", Image: sum(fx.data["d"])}}},
		},
	}
	if err := fx.list.Validate(); err != nil {
		t.Fatal(err)
	}
	return fx
}

func (fx *fixture) run(opt options) *downloader {
	fx.t.Helper()
	if opt.out == "" {
		opt.out = fx.out
	}
	var log bytes.Buffer
	d := newDownloader(opt, fx.list, newFetcher(), newConsole(&log, false, nil))
	d.sleep = func(context.Context, time.Duration) {}
	d.diskFree = func(string) (uint64, error) { return 100 << 30, nil }
	if err := d.run(context.Background()); err != nil {
		fx.t.Fatalf("run: %v\n%s", err, log.String())
	}
	return d
}

func (fx *fixture) runWith(opt options, setup func(d *downloader)) (*downloader, string) {
	fx.t.Helper()
	opt.out = fx.out
	var log bytes.Buffer
	d := newDownloader(opt, fx.list, newFetcher(), newConsole(&log, false, nil))
	d.sleep = func(context.Context, time.Duration) {}
	d.diskFree = func(string) (uint64, error) { return 100 << 30, nil }
	setup(d)
	if err := d.run(context.Background()); err != nil {
		fx.t.Fatalf("run: %v\n%s", err, log.String())
	}
	return d, log.String()
}

func (fx *fixture) content(rel string) []byte {
	data, err := os.ReadFile(filepath.Join(fx.out, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return data
}

func TestDownloadAll(t *testing.T) {
	fx := newFixture(t)
	d := fx.run(options{})

	if d.stats.downloaded != 4 || d.stats.failed != 0 {
		t.Fatalf("stats %+v", d.stats)
	}
	for rel, name := range map[string]string{"A_Arkanoid_Joust.msa": "a", "B_Barbarian.st": "b", "sub/dir/C.st": "c", "D_Dungeon-Master.st": "d"} {
		if !bytes.Equal(fx.content(rel), fx.data[name]) {
			t.Errorf("%s: wrong content", rel)
		}
	}
	if fx.srv.hit("/one/ab.zip") != 1 { // a and b from one download
		t.Errorf("ab.zip downloaded %d times", fx.srv.hit("/one/ab.zip"))
	}
	if fx.srv.hit("/two/ab.zip") != 0 {
		t.Errorf("second source used though the first worked")
	}

	entries, _ := os.ReadDir(fx.out)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("left over %s", e.Name())
		}
	}

	// index: the games, escaped
	html := string(fx.content("index.html"))
	if !strings.Contains(html, "Arkanoid") || strings.Contains(html, "Barbarian <script>") || !strings.Contains(html, "test.1") {
		t.Errorf("index.html doesn't have the games, or isn't escaped")
	}
	txt := string(fx.content("games.txt"))
	for _, want := range []string{"Arkanoid", "Joust [doc]", "->  A_Arkanoid_Joust.msa   (Automation #1)", "(content unknown)", "sub/dir/C.st"} {
		if !strings.Contains(txt, want) {
			t.Errorf("games.txt has no %q:\n%s", want, txt)
		}
	}

	// again - nothing to do
	d = fx.run(options{})
	if d.stats.downloaded != 0 || d.stats.had != 4 || fx.srv.hit("/one/ab.zip") != 1 {
		t.Fatalf("second run: stats %+v", d.stats)
	}
}

func TestFallbackToOtherSource(t *testing.T) {
	fx := newFixture(t)
	fx.srv.files["/one/ab.zip"] = zipOf(t, map[string][]byte{"A.MSA": imageData(9, 1024)}) // another file there now - hash fails
	d, log := fx.runWith(options{}, func(*downloader) {})

	if !bytes.Equal(fx.content("A_Arkanoid_Joust.msa"), fx.data["a"]) || !bytes.Equal(fx.content("B_Barbarian.st"), fx.data["b"]) {
		t.Fatalf("not from the second source\n%s", log)
	}
	if fx.srv.hit("/one/ab.zip") != 1 || fx.srv.hit("/two/ab.zip") != 1 || d.stats.failed != 0 {
		t.Errorf("hits %v, stats %+v", fx.srv.hits, d.stats)
	}
	if d.state.Images[sum(fx.data["a"])].Source != "two" {
		t.Errorf("source in state: %+v", d.state.Images[sum(fx.data["a"])])
	}
}

func TestSourceDownIsSkipped(t *testing.T) {
	fx := newFixture(t)
	fx.srv.down["one"] = true
	d, log := fx.runWith(options{}, func(*downloader) {})

	// a, b from two; c only on one - failed
	if d.stats.downloaded != 3 || d.stats.failed != 1 || fx.content("sub/dir/C.st") != nil {
		t.Fatalf("stats %+v\n%s", d.stats, log)
	}
	if d.badArchive[&fx.list.Archives[0]] { // a network error doesn't mark the archive as bad
		t.Errorf("archive marked bad after a network error")
	}
}

func TestDeletedNotDownloadedAgainUnlessAsked(t *testing.T) {
	fx := newFixture(t)
	fx.run(options{})
	os.Remove(filepath.Join(fx.out, "D_Dungeon-Master.st"))

	d := fx.run(options{})
	if d.stats.downloaded != 0 || fx.content("D_Dungeon-Master.st") != nil {
		t.Fatalf("deleted image downloaded again: %+v", d.stats)
	}
	if strings.Contains(string(fx.content("games.txt")), "Dungeon") {
		t.Errorf("deleted image still in games.txt")
	}

	d = fx.run(options{redownload: true})
	if d.stats.downloaded != 1 || fx.content("D_Dungeon-Master.st") == nil {
		t.Fatalf("-redownload: %+v", d.stats)
	}
}

func TestRenamedByNewerList(t *testing.T) {
	fx := newFixture(t)
	fx.run(options{})
	os.WriteFile(filepath.Join(fx.out, "A_Arkanoid_Joust.msa"), imageData(7, 1024), 0o644) // the ST wrote to it

	fx.list.Images[0].File = "A_Arkanoid_Joust_Pacman.msa"
	d := fx.run(options{})
	if d.stats.renamed != 1 || d.stats.downloaded != 0 {
		t.Fatalf("stats %+v", d.stats)
	}
	if got := fx.content("A_Arkanoid_Joust_Pacman.msa"); !bytes.Equal(got, imageData(7, 1024)) {
		t.Errorf("renamed file lost the ST's writes")
	}
	if fx.content("A_Arkanoid_Joust.msa") != nil {
		t.Errorf("old name still there")
	}
}

func TestExistingFilesFoundOrKept(t *testing.T) {
	fx := newFixture(t)
	os.MkdirAll(fx.out, 0o755)
	os.WriteFile(filepath.Join(fx.out, "A_Arkanoid_Joust.msa"), fx.data["a"], 0o644)    // the same - found by its content
	os.WriteFile(filepath.Join(fx.out, "B_Barbarian.st"), []byte("my own file"), 0o644) // another file - never overwritten

	d := fx.run(options{})
	if d.stats.found != 1 || d.stats.conflicts != 1 || d.stats.downloaded != 2 {
		t.Fatalf("stats %+v", d.stats)
	}
	if string(fx.content("B_Barbarian.st")) != "my own file" {
		t.Errorf("own file overwritten")
	}
	if fx.srv.hit("/one/ab.zip") != 0 { // nothing in it wanted
		t.Errorf("ab.zip downloaded although A is there and B is kept")
	}
}

func TestFilterLimitDryRun(t *testing.T) {
	fx := newFixture(t)
	d := fx.run(options{filter: []string{"dungeon"}})
	if d.stats.downloaded != 1 || fx.content("D_Dungeon-Master.st") == nil {
		t.Fatalf("filter: %+v", d.stats)
	}

	d = fx.run(options{dryRun: true})
	if d.stats.downloaded != 0 || fx.content("A_Arkanoid_Joust.msa") != nil || fx.srv.hit("/one/ab.zip") != 0 {
		t.Fatalf("dry run downloaded: %+v", d.stats)
	}

	d = fx.run(options{limit: 1})
	if d.stats.downloaded != 1 { // only A - B is in the same zip, but not one of the first 1
		t.Fatalf("limit: %+v", d.stats)
	}
}

func TestNoSpaceStops(t *testing.T) {
	fx := newFixture(t)
	d, log := fx.runWith(options{keepFree: 1 << 30}, func(d *downloader) {
		d.diskFree = func(string) (uint64, error) { return 1 << 30, nil }
	})
	if d.stats.downloaded != 0 || !strings.Contains(log, "would stay free") {
		t.Fatalf("stats %+v\n%s", d.stats, log)
	}
}

func TestStopped(t *testing.T) {
	fx := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var log bytes.Buffer
	d := newDownloader(options{out: fx.out}, fx.list, newFetcher(), newConsole(&log, false, nil))
	d.sleep = func(context.Context, time.Duration) {}
	if err := d.run(ctx); err != nil {
		t.Fatal(err)
	}
	if d.stats.downloaded != 0 || !strings.Contains(log.String(), "run it again") {
		t.Fatalf("stats %+v\n%s", d.stats, log.String())
	}
	if _, err := os.Stat(filepath.Join(fx.out, stateFileName)); err != nil {
		t.Errorf("state not written: %v", err)
	}
}

func TestOneRunAtATime(t *testing.T) {
	dir := t.TempDir()
	a, _ := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	defer a.Close()
	if !lockFile(a) {
		t.Fatal("first lock failed")
	}
	b, _ := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	defer b.Close()
	if lockFile(b) {
		t.Fatal("second lock succeeded")
	}

	fx := newFixture(t)
	fx.out = dir
	d := newDownloader(options{out: dir}, fx.list, newFetcher(), newConsole(&bytes.Buffer{}, false, nil))
	if err := d.run(context.Background()); err == nil || !strings.Contains(err.Error(), "another download") {
		t.Fatalf("second run: %v", err)
	}
}

func TestLocalSource(t *testing.T) {
	fx := newFixture(t)
	mirror := t.TempDir()
	for p, data := range fx.srv.files {
		if strings.HasPrefix(p, "/one/") {
			os.WriteFile(filepath.Join(mirror, strings.TrimPrefix(p, "/one/")), data, 0o644)
		}
	}
	fx.list.Sources[0].Base = "file://" + filepath.ToSlash(mirror) + "/"
	d := fx.run(options{})
	if d.stats.downloaded != 4 || fx.srv.hit("/one/ab.zip") != 0 {
		t.Fatalf("stats %+v, hits %v", d.stats, fx.srv.hits)
	}
}
