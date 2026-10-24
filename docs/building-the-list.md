# Building the image list

How `imagelist.json.gz` is made from the sites, and how to make a new one - e.g. when AtariLegend gets new disks,
or a source is added. Format of the list: [imagelist.md](imagelist.md).

Needs Python 3.8+ (only the standard library), and a few GB of disk for the archives.

## The steps

```
crawl_atarilegend.py ─> data/atarilegend/menus.json ─┐
                                                      ├─> fetch_atarilegend.py ─> data/archives/atarilegend/ ─┐
fetch_cosmosex_lists.py ───────────────────────────────────────────────────────> data/archives/cosmosex/ ────┼─> build_list.py ─> dist/imagelist.json.gz
tools/sources.json ───────────────────────────────────────────────────────────────────────────────────────────┘
```

All from the repo root. `data/` and `dist/` are not in git (`.gitignore`).

### 1. Crawl AtariLegend - what disks there are

```sh
python3 -I tools/crawl_atarilegend.py
```

- reads https://www.atarilegend.com/menusets and every menuset page (~600 pages, one every 1.5 s - ~15 minutes)
- writes `data/atarilegend/menus.json` (everything about every disk) and `menus.csv` (one line per disk, for a look)
- the pages are kept in `data/atarilegend/cache/` - a run again reads them from there, not from the site. For the new
  disks on the site: `--refresh` (reads all the pages again)
- only some menusets, to try it: `--menusets 1,188`

### 2. Download the AtariLegend dumps

```sh
python3 -I tools/fetch_atarilegend.py
```

- downloads every zip listed in `menus.json` (~3800, ~2.7 GB, one every second - 1-2 hours) to
  `data/archives/atarilegend/<zip number>.zip`
- checks each one (opens, CRCs, the site's SHA-512) and writes `index.csv` (what's in each zip) and `failed.csv`
- run again: the zips already there are only checked, not downloaded - so after a new crawl it gets just the new ones
- to try it: `--limit 5`

### 3. Download the old cosmosex lists (exxos Automation)

```sh
python3 -I tools/fetch_cosmosex_lists.py
```

- the zips from the old `imagelist.csv` / `!imagelist.csv` (cosmosex) - only the exxos ones work now (561), the
  stonish.net ones (~3100) are dead and end up in `failed.csv`
- `--wayback` - also look for the dead ones in the Wayback Machine, by their file name (found ones are marked
  `ok-wayback` in `index.csv` - check them, the same name doesn't have to be the same disk). The Wayback Machine
  limits the requests - it can take long, or not work at all at times. The builder doesn't use these yet.

### 4. Build the list

```sh
python3 -I tools/build_list.py
```

- opens every archive in `data/archives/*`, hashes every .st / .msa in it - the same image in archives on two sites
  becomes one image with two archives
- names the images (`<name in the zip>_<games>.msa`, see [imagelist.md](imagelist.md#naming)) - in a stable order,
  so the same data gives the same names (the downloader renames the files when a name changes)
- writes `dist/imagelist.json.gz`, prints how many images / archives
- `--version` - the list version, default today's date + `.1` (make it `.2`, ... for a second list on the same day)
- tests of the naming: `python3 -I -m unittest discover -s tools`

### 5. Try it, publish it

```sh
go run . -list dist/imagelist.json.gz -out /tmp/test-images -dry-run         # what would be downloaded
go run . -list dist/imagelist.json.gz -out /tmp/test-images -limit 10        # a few for real
```

Then add `dist/imagelist.json.gz` to a new GitHub release (with the downloader binaries from `./build.sh <version>`) -
the downloader gets the list from the latest release.

## Sources

`tools/sources.json` - the sites, in the order the downloader tries them. `base` + the archive path = the URL.

- **a mirror of the archives** (e.g. your own server with the same files): add it with its own `id` and `base` at the
  top, and put its files where `build_list.py` finds them - or, if it has the same files under the same paths as an
  existing source, users can already use it without a new list: `-source atarilegend=https://mirror.example.org/menus/`
- **a dead source**: remove it from `sources.json` and rebuild - its archives go away, the images stay if another
  source has them
- `pauseMs` - the downloader waits this long between the requests to the source

## Being nice to the sites

The tools wait between the requests (`--pause`), tell who they are (User-Agent), and slow down when AtariLegend's
rate limit header gets low. Don't make the pauses shorter - it's their bandwidth. Ask a site before making it a source
which thousands of downloaders will use.
