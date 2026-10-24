# Image list format

`imagelist.json.gz` - the list the downloader reads. Built by `tools/build_list.py`, published as a release asset of
this repo:

    https://github.com/atarijookie/atari-st-floppy-image-downloader/releases/latest/download/imagelist.json.gz

(release downloads are meant for many users downloading often, unlike the raw files of a repo).

## Ideas

- one list of images, which can be downloaded from several places (sources) - each image from the first source which
  has it and works
- **an image is identified by the SHA-256 of the image file** (.st / .msa) - the same image packed differently on two
  sites (other zip, other name inside) is the same image; every stored image is checked against it, so any source is
  fine, even plain http or a mirror run by someone else
- the file name of every image is given by the list - the same for every user, readable (see Naming)
- a source is one line (base URL) - moving or adding one doesn't touch thousands of entries

## Format

JSON, gzipped; the downloader also reads it not gzipped. Example (hashes shortened):

```json
{
  "format": 1,
  "version": "2026-10-24.1",
  "sources": [
    { "id": "atarilegend", "name": "AtariLegend", "base": "https://www.atarilegend.com/storage/zips/menus/", "pauseMs": 1000 },
    { "id": "exxos", "name": "exxos forum - Automation", "base": "https://www.exxosforum.co.uk/atari/games/automation/files/", "pauseMs": 1000 }
  ],
  "images": [
    {
      "id": "ccf18dbc...",
      "file": "ACO_B_Flight-of-the-Intruder.msa",
      "size": 710396,
      "set": "A Clockwork Orange",
      "title": "A Clockwork Orange #[no number]",
      "condition": "intact",
      "content": [
        { "name": "Flight of the Intruder", "kind": "game" },
        { "name": "Ripper 3.1", "kind": "software" },
        { "name": "FastCopy Pro 1.0c", "kind": "software", "tag": "[doc]" }
      ],
      "links": { "atarilegend": "https://www.atarilegend.com/menusets/1#menudisk-1" }
    }
  ],
  "archives": [
    {
      "source": "atarilegend", "path": "1.zip", "sha256": "5c0e...", "size": 649461,
      "contains": [ { "name": "ACO_B.MSA", "image": "ccf18dbc..." } ]
    }
  ]
}
```

### sources

| field | |
|---|---|
| `id` | referenced by the archives |
| `name` | for people |
| `base` | URL prefix: http(s):// or file:// |
| `pauseMs` | pause between the requests to this source - be nice to sites which are not ours (not for file://) |

The order is the preference: the archives of an image are tried in the order of their sources.

### images - what ends up on the disk

| field | |
|---|---|
| `id` | SHA-256 of the image file, lower case hex - unique |
| `file` | file name, unique ignoring case (Windows, macOS); only .st / .msa |
| `folder` | optional, relative, `/` separated - `""` = the output folder itself (all images there now) |
| `size` | of the image file |
| `set`, `title`, `condition` | optional, for people - menu disk group, the disk, `intact` / `slightly damaged` / ... |
| `content` | optional - what's on the disk: `name`, `kind` (`game`, `software`, `other`), `tag` (e.g. `[doc]`, `[doc + solution]` - only the docs of it are on the disk) |
| `links` | optional - e.g. the disk on AtariLegend |

### archives - what is downloaded

| field | |
|---|---|
| `source`, `path` | URL = source `base` + `path` (path segments URL-escaped by the downloader) |
| `type` | `zip` (default), or `image` - the file is the image itself |
| `sha256`, `sha512`, `size` | optional - of the archive file, checked when there |
| `contains` | the images in it, by their `name` in the archive. An archive may have more files - only the listed ones are used |

One archive per file on one source - the same zip on two sources is two entries. An image can be in archives on
several sources; not every archive has all the images, not every source works - so an image is taken from the first
working archive which has it. All the wanted images in a downloaded archive are stored, so a zip with several images
is downloaded once.

## Naming

`<name of the image in the archive>_<games>.<ext>` - e.g. `AUTO012_Arkanoid_Joust_Barbarian+.msa`:

- the games of the disk (not the ones only with docs); a disk with no games - its software
- each name: plain ASCII letters, digits and `-` (`Bard's Tale, The` -> `The-Bards-Tale`)
- at most 60 chars (the cosmo2ex front panel shows 63), whole names only; `+` at the end when not all fit - the rest
  is in `index.html` / `games.txt`
- unique ignoring case: `_2`, `_3`, ... before the extension
- names are stable: the same data gives the same names. When a newer list renames an image (e.g. a fixed game name),
  the downloader renames the file

## Downloader

- the images downloaded before are in `<out>/.downloaded.json` (image id -> file, source, time) - not downloaded
  again, even when deleted (unless `-redownload`); the files themselves can't be checked by their hash, the ST
  writes to them
- an image file which is there but not in `.downloaded.json` (e.g. it was lost): same hash = taken as downloaded,
  otherwise left alone (never overwritten) and reported
- a source with 5 network errors in a row is not used for the rest of the run; an archive which is wrong (404, hash,
  size, not a zip) is not tried again in the run
- free space checked before every download (keeps `-keep-free` MB free)
- writes `index.html` (search, works offline) and `games.txt` (one line per game, for grep / Ctrl+F) of the images
  which are there

## Version changes

`format` changes only when an older downloader couldn't read the list right - it then tells the user to get a newer
one. New optional fields don't change it.
