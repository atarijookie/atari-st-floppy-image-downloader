// atari-st-floppy-image-downloader - downloads Atari ST floppy images from the sources in an image list, into one
// folder, and writes index.html + games.txt there for finding the games. See README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/atarijookie/atari-st-floppy-image-downloader/internal/imagelist"
)

var version = "dev" // set by build.sh

const defaultListURL = "https://github.com/atarijookie/atari-st-floppy-image-downloader/releases/latest/download/imagelist.json.gz"

type sourceFlags map[string]string

func (s sourceFlags) String() string { return "" }
func (s sourceFlags) Set(v string) error {
	id, base, ok := strings.Cut(v, "=")
	if !ok || id == "" || base == "" {
		return fmt.Errorf("expected id=URL, e.g. atarilegend=https://mirror.example.org/menus/")
	}
	s[id] = base
	return nil
}

func main() {
	code := run()
	waitIfOwnConsole() // double-clicked on Windows - keep the window open to read the result
	os.Exit(code)
}

func run() int {
	var opt options
	listURL := flag.String("list", defaultListURL, "the image list: URL or local file")
	flag.StringVar(&opt.out, "out", "", "folder for the images (default: floppyImages next to this program)")
	keepFreeMB := flag.Uint64("keep-free", 1024, "MB to keep free on the disk")
	filter := flag.String("filter", "", "only the images with all these words in their name / menu disk / games, e.g. -filter \"automation\"")
	flag.IntVar(&opt.limit, "limit", 0, "download at most this many images (0 = all)")
	flag.BoolVar(&opt.dryRun, "dry-run", false, "only tell what would be downloaded")
	flag.BoolVar(&opt.redownload, "redownload", false, "download again the images downloaded before whose file was deleted")
	flag.BoolVar(&opt.noIndex, "no-index", false, "don't write index.html and games.txt")
	sources := sourceFlags{}
	flag.Var(sources, "source", "use another URL for a source of the list, e.g. -source atarilegend=file:///mnt/mirror/ (can be repeated)")
	showVersion := flag.Bool("version", false, "show the version")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Atari ST floppy image downloader %s\n\nDownloads the floppy images from the image list into one folder, "+
			"with index.html and games.txt for finding the games.\nRun it again later for the new images - the ones there are not downloaded again.\n\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return 0
	}
	if opt.out == "" { // next to the program - double-clicked in Finder, the current dir would be the home dir
		opt.out = "floppyImages"
		if exe, err := os.Executable(); err == nil {
			if exe, err = filepath.EvalSymlinks(exe); err == nil {
				opt.out = filepath.Join(filepath.Dir(exe), "floppyImages")
			}
		}
	}
	opt.keepFree = *keepFreeMB << 20
	opt.filter = strings.Fields(strings.ToLower(*filter))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		fmt.Println("\nstopping - writing what's done...")
		cancel()
	}()

	f := newFetcher()
	fmt.Printf("image list: %s\n", *listURL)
	data, err := f.bytes(ctx, *listURL, 256<<20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't get the image list: %v\n", err)
		return 1
	}
	list, err := imagelist.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad image list: %v\n", err)
		return 1
	}
	for id, base := range sources {
		found := false
		for i := range list.Sources {
			if list.Sources[i].ID == id {
				list.Sources[i].Base, found = base, true
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "-source %s: no such source in the list\n", id)
			return 1
		}
	}
	if len(sources) > 0 {
		if err := list.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "-source: %v\n", err)
			return 1
		}
	}
	fmt.Printf("list %s: %d images, %d archives on %d sources; images go to %s\n",
		list.Version, len(list.Images), len(list.Archives), len(list.Sources), opt.out)

	d := newDownloader(opt, list, f, os.Stdout)
	if err := d.run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !opt.noIndex && !opt.dryRun {
		fmt.Printf("look for the games in %s and %s\n", "index.html", "games.txt")
	}
	return 0
}
