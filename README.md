# Atari ST floppy image downloader

Downloads Atari ST floppy images (menu disks - Automation, Pompey Pirates, The Medway Boys, Zuul, ...) into one folder, and
writes `index.html` and `games.txt` there, so a game can be found without any other program.

- one program, nothing to install - Windows, macOS, Linux (also Raspberry Pi)
- the images come from the sites in the image list, each image is checked by its hash
- run it again later: only the new images are downloaded

Made for [cosmo2ex](https://github.com/atarijookie/cosmo2ex), but the images work in any Atari ST emulator or floppy
emulator.

## Use

1. Download the archive for your system from [Releases](https://github.com/atarijookie/atari-st-floppy-image-downloader/releases/latest):

   | system | file |
   |---|---|
   | Windows | `...-windows-amd64.zip` (old 32-bit Windows: `...-windows-386.zip`) |
   | macOS (Apple chip / Intel) | `...-darwin-arm64.tar.gz` / `...-darwin-amd64.tar.gz` |
   | Linux PC | `...-linux-amd64.tar.gz` |
   | Raspberry Pi | `...-linux-arm64.tar.gz` (64-bit OS), `...-linux-armv6.tar.gz` (32-bit OS) |

   `SHA256SUMS` next to them has their checksums.

2. Unpack it where you want the images - it has a folder with the program and this README - and run the program:
   - Windows: right-click the zip -> "Extract All", then double-click `atari-st-floppy-image-downloader.exe`
     (Windows may warn about an unknown program - "More info" -> "Run anyway")
   - macOS / Linux: `tar -xzf atari-st-floppy-image-downloader-*.tar.gz`, then in that folder
     `./atari-st-floppy-image-downloader` (macOS may block it the first time - right-click it -> Open, or
     `xattr -d com.apple.quarantine atari-st-floppy-image-downloader`)

3. The images go into the `floppy_images` folder next to it - about 3 GB for all ~4400 images, 1-2 hours (it waits a second between the downloads, to be nice to the sites).
   It can be stopped (Ctrl+C, or closing the window) and started again - it goes on where it stopped.
   Each downloaded image is listed with the URL it came from; the bottom line shows the image being downloaded, its
   progress and the speed.

### Finding a game

The file names have the games on the disk: `AUTO012_Arkanoid_Joust_Barbarian+.msa` (`+` = more games on it than fit
into the name). So the file search of your system finds most of them. For all of them:

- `floppy_images/index.html` - open it in a browser, type into the search box (works offline)
- `floppy_images/games.txt` - one line per game, sorted: `Joust  ->  AUTO012_Arkanoid_Joust_Barbarian+.msa`

### Options

```
-out DIR          folder for the images (default: floppy_images next to the program)
-filter "WORDS"   only the images with all these words in their name / menu disk / games, e.g. -filter "automation"
-limit N          at most N images
-dry-run          only tell what would be downloaded
-keep-free MB     MB to keep free on the disk (default 1024)
-redownload       download again the images deleted after downloading (normally they are not downloaded again)
-list URL|FILE    another image list
-source ID=URL    another place for a source of the list, e.g. a mirror or a local copy:
                  -source atarilegend=file:///mnt/mirror/atarilegend/
```

`floppy_images/.downloaded.json` remembers what was downloaded - images are renamed in place when a newer list gives
them a better name, never overwritten.

### Choosing what to download

- **Into another folder** (a new one, or one without images yet): `-out D:\Atari\floppies` or
  `-out ~/atari/floppies` - created if it's not there. Files which are there already are left alone; only the image
  files of the list are written.
- **Only some images**: `-filter`, checked first with `-dry-run`:

  ```
  atari-st-floppy-image-downloader -filter "pompey pirates" -dry-run    # how many, how big
  atari-st-floppy-image-downloader -filter "pompey pirates"             # download them
  ```

  Each run with another filter adds to the same folder.
- **Delete what you don't want** after downloading - deleted images stay deleted, the next run doesn't download them
  again (they are in `.downloaded.json`). To get one back: `-redownload` (all the deleted ones), or start over in a new
  `-out` folder.
- Images can't be deleted before they are downloaded - choose with `-filter` instead.

### Running from the source

Needs Go 1.19+. `go run` builds the program into a temporary folder, so give `-out` (otherwise the images would go
next to the temporary program):

```sh
go run . -out ~/atari/floppies -dry-run
go build -o stfloppy . && ./stfloppy -out ~/atari/floppies     # or build it once
```

With a list made locally (`list/imagelist.json.gz`, see [docs/building-the-list.md](docs/building-the-list.md)), and
the archives already downloaded by the tools - nothing is downloaded from the sites then:

```sh
go run . -list list/imagelist.json.gz -out ~/atari/floppies \
    -source atarilegend=file://$PWD/data/archives/atarilegend/ \
    -source exxos=file://$PWD/data/archives/cosmosex/
```

## The image list

`imagelist.json.gz` (format: [docs/imagelist.md](docs/imagelist.md)) - which images there are, which games are on
them, and which archives on which sites have them. Built by the tools in `tools/` - crawl the sites, download their
archives, build the list. How, step by step: [docs/building-the-list.md](docs/building-the-list.md).

## Building the downloader

Go 1.19 or newer, no other dependencies:

```sh
go test ./...
./build.sh 1.0.0         # all platforms -> dist/
```

- `dist/<name>-<version>-<os>-<arch>.zip` (Windows) / `.tar.gz` (Linux, macOS) - what goes into a release, with
  `dist/imagelist.json.gz` and `dist/SHA256SUMS`
- `dist/build/<name>-<version>-<os>-<arch>/` - the same unpacked, e.g. to run it right away:
  `./dist/build/*-linux-amd64/atari-st-floppy-image-downloader -list list/imagelist.json.gz -out ~/atari/floppies`

### Releasing

A tag `v<version>` builds and publishes a release (`.github/workflows/release.yml`): tests, the binaries for all the
platforms, and `list/imagelist.json.gz` - the downloader gets the list from the latest release.

```sh
git tag v1.0.0
git push origin v1.0.0
```

A new image list (see [docs/building-the-list.md](docs/building-the-list.md)): commit `list/imagelist.json.gz`, then
tag a new version the same way.
