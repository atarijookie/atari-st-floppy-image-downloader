#!/usr/bin/env python3
"""
Downloads the menu disk dumps listed by the AtariLegend crawl (data/atarilegend/menus.json, made by
crawl_atarilegend.py) - for building the image list later.

  python3 -I tools/fetch_atarilegend.py                # -> data/archives/atarilegend/
  python3 -I tools/fetch_atarilegend.py --limit 5       # just the first 5, to try it

Keeps the zips as they are, named by their number on AtariLegend (<zip_id>.zip - their names have spaces, '#', and
repeat). Every zip is checked: it must open, all CRCs fine. The SHA-512 from the site is compared with the zip and with
the files in it, so we know what it's the hash of. Can be stopped (Ctrl+C) and run again - zips already there and
fine are not downloaded again.

Output in --out:
  <zip_id>.zip
  index.csv     one line per zip: zip_id, status, size, sha512 match (zip / image / no), files in the zip with their
                size and SHA-256 (the image ids for the list), menuset, title, condition, games, URL
  failed.csv    the ones which couldn't be downloaded, with the reason
"""

import argparse
import csv
import fcntl
import hashlib
import io
import json
import os
import sys
import time
import urllib.error
import urllib.request
import zipfile

USER_AGENT = "cosmo2ex image archiver (one-off download of the menu disks for a floppy image list)"
MAX_FILE_SIZE = 32 << 20
IMAGE_EXTS = (".st", ".msa", ".stx", ".dim")


def log(msg):
    print(time.strftime("%H:%M:%S ") + msg, flush=True)


def fetch(url, tries=5):
    """GET -> bytes; retries network errors, 5xx and 429 (longer each time), not 404 & co."""
    for attempt in range(tries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
            with urllib.request.urlopen(req, timeout=120) as resp:
                data = resp.read(MAX_FILE_SIZE + 1)
                left = resp.headers.get("x-ws-ratelimit-remaining")
                if left is not None and left.isdigit() and int(left) < 50:     # close to their limit - slow down
                    log("rate limit: %s requests left, waiting a minute" % left)
                    time.sleep(60)
            if len(data) > MAX_FILE_SIZE:
                raise ValueError("bigger than %d MB" % (MAX_FILE_SIZE >> 20))
            return data
        except urllib.error.HTTPError as e:
            if e.code != 429 and e.code < 500:
                raise
            err, wait = e, 30 * (attempt + 1)
        except (urllib.error.URLError, OSError) as e:
            err, wait = e, 15 * (attempt + 1)
        if attempt < tries - 1:
            log("%s: %s, again in %d s" % (url, err, wait))
            time.sleep(wait)
    raise err


def check_zip(data):
    """-> [(name, size, sha256, sha512)] of the files in the zip; raises ValueError if it's broken"""
    try:
        zf = zipfile.ZipFile(io.BytesIO(data))
    except zipfile.BadZipFile:
        start = data[:60].decode("latin-1", "replace").replace("\n", " ").strip()
        raise ValueError("not a zip file (starts with: %r)" % start)
    bad = zf.testzip()
    if bad:
        raise ValueError("broken zip - CRC error in %s" % bad)
    files = []
    for info in zf.infolist():
        if info.is_dir():
            continue
        content = zf.read(info)
        files.append((info.filename, len(content), hashlib.sha256(content).hexdigest(), hashlib.sha512(content).hexdigest()))
    if not files:
        raise ValueError("empty zip")
    return files


def describe(data, disk):
    files = check_zip(data)
    sha = disk["sha512"].lower()
    if not sha:
        match = "no hash"
    elif hashlib.sha512(data).hexdigest() == sha:
        match = "zip"
    elif any(f[3] == sha for f in files):
        match = "image"
    else:
        match = "no"
    return files, match


def save(path, data):
    tmp = path + ".part"
    with open(tmp, "wb") as f:
        f.write(data)
    os.replace(tmp, path)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--menus", default="data/atarilegend/menus.json")
    ap.add_argument("--out", default="data/archives/atarilegend")
    ap.add_argument("--pause", type=float, default=1.0, help="seconds between downloads (default 1)")
    ap.add_argument("--limit", type=int, default=0, help="only the first N zips - for trying it out")
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)
    lock = open(os.path.join(args.out, ".lock"), "w")      # two runs would write the same files
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        sys.exit("another run with --out %s is running" % args.out)

    # the disks with a dump, each zip once (the same zip can be listed on more disks)
    disks, seen = [], set()
    for d in json.load(open(args.menus, encoding="utf-8")):
        if d["zip_url"] and d["zip_url"] not in seen and d.get("zip_id"):
            seen.add(d["zip_url"])
            disks.append(d)
    if args.limit:
        disks = disks[:args.limit]
    log("%d zips to get" % len(disks))

    results, t0 = [], time.time()
    try:
        for n, d in enumerate(disks, 1):
            path = os.path.join(args.out, "%d.zip" % d["zip_id"])
            r = {"disk": d, "status": "failed", "reason": "", "files": [], "match": "", "size": ""}
            results.append(r)
            downloaded = False
            try:
                if os.path.exists(path):                # from an earlier run - check it again
                    data = open(path, "rb").read()
                    r["files"], r["match"] = describe(data, d)
                    r["status"] = "ok (earlier run)"
                else:
                    downloaded = True
                    data = fetch(d["zip_url"])
                    r["files"], r["match"] = describe(data, d)
                    save(path, data)
                    r["status"] = "ok"
                r["size"] = len(data)
            except KeyboardInterrupt:
                raise
            except Exception as e:                      # just this one failed
                r["reason"] = "%s: %s" % (type(e).__name__, e)
            if downloaded:
                time.sleep(args.pause)

            if n % 50 == 0 or n == len(disks) or (r["reason"] and n < 50):
                ok = sum(1 for x in results if x["status"].startswith("ok"))
                eta = (time.time() - t0) / n * (len(disks) - n)
                log("%d/%d  ok %d  failed %d  (%d.zip%s)  ~%d min left" % (n, len(disks), ok, n - ok, d["zip_id"],
                    ": " + r["reason"][:80] if r["reason"] else "", eta / 60))
    except KeyboardInterrupt:
        log("stopped - writing what's done, run it again to go on")

    write_reports(args.out, results)
    ok = [x for x in results if x["status"].startswith("ok")]
    matches = {}
    for x in ok:
        matches[x["match"]] = matches.get(x["match"], 0) + 1
    log("done: %d zips - %d ok, %d failed (failed.csv); the site's SHA-512 matches: %s; in %d min" % (
        len(results), len(ok), len(results) - len(ok), matches, (time.time() - t0) / 60))


def write_reports(out, results):
    with open(os.path.join(out, "index.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["zip_id", "status", "size", "sha512_match", "files", "menuset_id", "menuset", "disk_id", "title",
                    "condition", "games", "software", "zip_url"])
        for r in results:
            d = r["disk"]
            def names(game):
                return ";".join(i["name"] + (" " + i["tag"] if i.get("tag") else "") for i in d["items"]
                                if (i["kind"] == "game") == game)
            files = ";".join("%s:%d:%s" % (name, size, sha256) for name, size, sha256, _ in r["files"])
            w.writerow([d["zip_id"], r["status"], r["size"], r["match"], files, d["menuset_id"], d["menuset"],
                        d["disk_id"], d["title"], d["condition"], "?" if d.get("content_unknown") else names(True),
                        names(False), d["zip_url"]])
    with open(os.path.join(out, "failed.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["zip_id", "zip_url", "reason"])
        for r in results:
            if not r["status"].startswith("ok"):
                w.writerow([r["disk"]["zip_id"], r["disk"]["zip_url"], r["reason"]])


if __name__ == "__main__":
    main()
