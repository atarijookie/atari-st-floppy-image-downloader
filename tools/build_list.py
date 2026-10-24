#!/usr/bin/env python3
"""
Builds the image list (imagelist.json.gz, format in docs/imagelist.md) from the downloaded archives.

  python3 -I tools/build_list.py                     # data/ -> list/imagelist.json.gz

Reads:
  tools/sources.json                    the sources, in the order the downloader tries them
  data/atarilegend/menus.json           crawl_atarilegend.py - the disks, what's on them
  data/archives/atarilegend/            fetch_atarilegend.py - <zip_id>.zip + index.csv
  data/archives/cosmosex/               fetch_cosmosex_lists.py - the zips of the old lists + index.csv

Every archive is opened, every .st / .msa in it is hashed - the SHA-256 of the image is its id, so the same image in
archives on several sources is one image. File names (option E): <name in the archive>_<games>.<ext>, at most
--max-name chars, plain ASCII, '+' at the end when not all the games fit; unique ignoring case.
"""

import argparse
import csv
import datetime
import gzip
import hashlib
import json
import os
import re
import sys
import unicodedata
import zipfile

IMAGE_EXTS = (".st", ".msa")
WINDOWS_RESERVED = {"CON", "PRN", "AUX", "NUL"} | {"COM%d" % i for i in range(1, 10)} | {"LPT%d" % i for i in range(1, 10)}


def log(msg):
    print(msg, flush=True)


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def slug(text):
    """'Bard's Tale, The' -> 'The-Bards-Tale' - ASCII letters, digits and '-' only"""
    m = re.match(r"^(.*), (The|A|An|Le|La|Les|Der|Die|Das)$", text.strip())
    if m:
        text = m.group(2) + " " + m.group(1)
    text = unicodedata.normalize("NFKD", text).encode("ascii", "ignore").decode()
    text = text.replace("'", "").replace("&", " and ")
    return re.sub(r"[^A-Za-z0-9]+", "-", text).strip("-")


def url_key(url):
    """'https://www.Example.org/x' -> 'example.org/x' - the old lists have the URLs before the redirect to www."""
    rest = url.lower().split("//", 1)[-1]
    return rest[4:] if rest.startswith("www.") else rest


def stem_of(name):
    """name of the image in the archive without the extension, safe as a file name"""
    base = re.split(r"[\\/]", name)[-1]
    stem = slug(os.path.splitext(base)[0]) or "disk"
    if stem.upper() in WINDOWS_RESERVED:
        stem += "_"
    return stem


def make_name(stem, ext, names, max_len):
    """option E: stem_name1_name2...ext, '+' when some names didn't fit"""
    out, left = stem, list(names)
    while left:
        s = slug(left[0])
        if not s:
            left.pop(0)
            continue
        more = "+" if len(left) > 1 else ""
        if len(out) + 1 + len(s) + len(more) + len(ext) <= max_len:
            out += "_" + s
            left.pop(0)
            continue
        if out == stem:                       # not even the first one fits - a part of it, cut at a '-'
            room = max_len - len(stem) - 1 - 1 - len(ext)
            part = s[:room]
            if "-" in part[1:]:
                part = part[:part.rindex("-")]
            if len(part) >= 3:
                out += "_" + part
        break
    if left:
        out += "+"
    return out + ext


def names_for_file(content):
    """the names in the file name: the games (not just their docs) - or, on a disk with no games, the software"""
    games = [c["name"] for c in content if c["kind"] == "game" and not c.get("tag")]
    if games:
        return games
    return [c["name"] for c in content if c["kind"] != "game" and not c.get("tag")]


def read_zip(path):
    """-> [(name in the zip, ext, size, sha256)] of the images in it"""
    found = []
    with zipfile.ZipFile(path) as zf:
        for info in zf.infolist():
            ext = os.path.splitext(info.filename)[1].lower()
            if info.is_dir() or ext not in IMAGE_EXTS:
                continue
            data = zf.read(info)
            found.append((info.filename, ext, len(data), hashlib.sha256(data).hexdigest()))
    return found


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--sources", default="tools/sources.json")
    ap.add_argument("--atarilegend-menus", default="data/atarilegend/menus.json")
    ap.add_argument("--atarilegend-archives", default="data/archives/atarilegend")
    ap.add_argument("--cosmosex-archives", default="data/archives/cosmosex")
    ap.add_argument("--out", default="list/imagelist.json.gz")
    ap.add_argument("--version", default=datetime.date.today().isoformat() + ".1")
    ap.add_argument("--max-name", type=int, default=60, help="max length of a file name (default 60)")
    args = ap.parse_args()

    sources = json.load(open(args.sources, encoding="utf-8"))
    source_ids = [s["id"] for s in sources]

    images = {}         # id -> image
    archives = []

    def add_image(img_id, name, ext, size, meta, source):
        img = images.get(img_id)
        if img is None:
            img = images[img_id] = {"id": img_id, "size": size, "ext": ext, "stem": stem_of(name),
                                    "stemSource": source, "meta": None}
        elif source_ids.index(source) < source_ids.index(img["stemSource"]):   # the name from the preferred source
            img["stem"], img["stemSource"] = stem_of(name), source
        if meta and (img["meta"] is None or len(meta.get("content", [])) > len(img["meta"].get("content", []))):
            img["meta"] = meta          # the richer description wins

    # --- AtariLegend
    disks = {}
    if os.path.exists(args.atarilegend_menus):
        for d in json.load(open(args.atarilegend_menus, encoding="utf-8")):
            if d.get("zip_id"):
                disks.setdefault(d["zip_id"], d)
    al_index = os.path.join(args.atarilegend_archives, "index.csv")
    if "atarilegend" in source_ids and os.path.exists(al_index):
        for row in csv.DictReader(open(al_index, encoding="utf-8")):
            if not row["status"].startswith("ok"):
                continue
            zip_id = int(row["zip_id"])
            path = os.path.join(args.atarilegend_archives, "%d.zip" % zip_id)
            d = disks.get(zip_id)
            meta = None
            if d:
                meta = {"set": d["menuset"], "title": d["title"], "condition": d["condition"],
                        "content": [] if d.get("content_unknown") else
                                   [{k: v for k, v in (("name", i["name"]), ("kind", i["kind"]), ("tag", i.get("tag", ""))) if v}
                                    for i in d["items"]],
                        "links": {"atarilegend": "https://www.atarilegend.com/menusets/%d#menudisk-%d" % (d["menuset_id"], d["disk_id"])}}
            contained = read_zip(path)
            for name, ext, size, img_id in contained:
                add_image(img_id, name, ext, size, meta, "atarilegend")
            if contained:
                archives.append({"source": "atarilegend", "path": "%d.zip" % zip_id, "sha256": sha256_file(path),
                                 "size": os.path.getsize(path),
                                 "contains": [{"name": n, "image": i} for n, _, _, i in contained]})
        log("atarilegend: %d archives" % sum(1 for a in archives if a["source"] == "atarilegend"))

    # --- the old cosmosex lists (only the archives on the sources we have - exxos; stonish is dead)
    cx_index = os.path.join(args.cosmosex_archives, "index.csv")
    if os.path.exists(cx_index):
        for row in csv.DictReader(open(cx_index, encoding="utf-8")):
            if row["status"] != "ok":
                continue
            src = next((s for s in sources if url_key(row["list_url"]).startswith(url_key(s["base"]))), None)
            if not src:
                continue
            path = os.path.join(args.cosmosex_archives, row["file"])
            games = [g for g in row["games"].split(";") if g]
            meta = {"set": "Automation" if src["id"] == "exxos" else "", "title": os.path.splitext(row["file"])[0],
                    "content": [{"name": g, "kind": "game"} for g in games]}
            contained = read_zip(path)
            for name, ext, size, img_id in contained:
                add_image(img_id, name, ext, size, meta, src["id"])
            if contained:
                archives.append({"source": src["id"], "path": row["file"], "sha256": sha256_file(path),
                                 "size": os.path.getsize(path),
                                 "contains": [{"name": n, "image": i} for n, _, _, i in contained]})
        log("old lists: %d archives" % sum(1 for a in archives if a["source"] != "atarilegend"))

    # --- file names: in a stable order, so the same data gives the same names
    def order(img):
        m = img["meta"] or {}
        return (m.get("set", "").lower(), m.get("title", "").lower(), img["stem"].lower(), img["id"])

    taken, out_images = set(), []
    for img in sorted(images.values(), key=order):
        m = img["meta"] or {}
        content = m.get("content", [])
        name = make_name(img["stem"], img["ext"], names_for_file(content), args.max_name)
        n = 2
        while name.lower() in taken:          # unique ignoring case (Windows, macOS)
            suffix = "_%d" % n
            base = make_name(img["stem"], "", names_for_file(content), args.max_name - len(suffix) - len(img["ext"]))
            name = base + suffix + img["ext"]
            n += 1
        taken.add(name.lower())
        entry = {"id": img["id"], "file": name, "size": img["size"]}
        for key in ("set", "title", "condition"):
            if m.get(key):
                entry[key] = m[key]
        if content:
            entry["content"] = content
        if m.get("links"):
            entry["links"] = m["links"]
        out_images.append(entry)

    archives.sort(key=lambda a: (source_ids.index(a["source"]), a["path"]))
    result = {"format": 1, "version": args.version, "sources": sources, "images": out_images, "archives": archives}

    os.makedirs(os.path.dirname(args.out) or ".", exist_ok=True)
    data = json.dumps(result, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    tmp = args.out + ".part"
    with gzip.open(tmp, "wb", 9) as f:
        f.write(data)
    os.replace(tmp, args.out)

    total = sum(i["size"] for i in out_images)
    multi = sum(1 for i in out_images if sum(1 for a in archives for c in a["contains"] if c["image"] == i["id"]) > 1)
    log("%s: version %s, %d images (%d MB unpacked), %d archives, %d images on more than one source, %d kB packed" % (
        args.out, args.version, len(out_images), total >> 20, len(archives), multi, os.path.getsize(args.out) >> 10))


if __name__ == "__main__":
    main()
