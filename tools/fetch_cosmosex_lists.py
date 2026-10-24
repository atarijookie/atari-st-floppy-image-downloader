#!/usr/bin/env python3
"""
Downloads the floppy images of the old cosmosex image lists (imagelist.csv, !imagelist.csv) - for building the list.

Keeps the original zip files (same names as in the lists, all in one folder - the names don't collide), but only the
valid ones: HTTP 200, a zip which opens, all CRCs fine, at least one .st / .msa inside. Whatever came instead (the HTML
pages stonish.net now redirects to, 404s, broken zips) is not saved, it's listed in failed.csv.

  python3 -I tools/fetch_cosmosex_lists.py                     # both default lists -> data/archives/cosmosex
  python3 -I tools/fetch_cosmosex_lists.py --limit 5 --out /tmp/x   # try it on a few files first
  python3 -I tools/fetch_cosmosex_lists.py --wayback           # + look for the missing ones in the Wayback Machine

Can be stopped (Ctrl+C) and run again - files already there and valid are not downloaded again.

Output in --out:
  <name>.zip        the images
  index.csv         one line per list entry: file, status, source, size, sha256, images in the zip, games, original URL
                    status: ok / ok-wayback (found in the Wayback Machine by its file name - check it) / failed
  failed.csv        the entries which couldn't be downloaded, with the reason
  lists/            copies of the lists as they were downloaded
"""

import argparse
import csv
import hashlib
import io
import json
import os
import posixpath
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

DEFAULT_LISTS = [
    "https://joo.kie.sk/cosmosex/update/!imagelist.csv",    # the long one - has all of the short one
    "https://joo.kie.sk/cosmosex/update/imagelist.csv",
]
USER_AGENT = "cosmo2ex image archiver (one-off download for re-hosting)"
MAX_FILE_SIZE = 32 << 20
IMAGE_EXTS = (".st", ".msa")
WAYBACK_CDX = "https://web.archive.org/cdx/search/cdx"


def log(msg):
    print(time.strftime("%H:%M:%S ") + msg, flush=True)


def fetch(url, timeout=120, max_size=MAX_FILE_SIZE):
    """GET url -> bytes. Raises on HTTP errors / network errors / too big."""
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=timeout) as resp:      # follows redirects
        data = resp.read(max_size + 1)
        if len(data) > max_size:
            raise ValueError("bigger than %d MB" % (max_size >> 20))
        return data, resp.geturl()


def fetch_retry(url, tries=3, pause=5, **kw):
    """fetch() with retries on network errors and 5xx / 429 - not on 404 & co."""
    for i in range(tries):
        try:
            return fetch(url, **kw)
        except urllib.error.HTTPError as e:
            if e.code < 500 and e.code != 429:
                raise
            err = e
        except (urllib.error.URLError, OSError) as e:
            err = e
        if i < tries - 1:
            time.sleep(pause * (i + 1))
    raise err


def parse_list(text):
    """-> version, [(url, name, games)]"""
    version, entries = "", []
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        fields = line.split(",")
        url = fields[0].strip()
        p = urllib.parse.urlparse(url)
        name = posixpath.basename(p.path)
        if p.scheme in ("http", "https") and p.netloc and name and not name.startswith(".") \
                and name.lower().endswith((".zip",) + IMAGE_EXTS):
            entries.append((url, name, [g.strip() for g in fields[2:] if g.strip()]))
        elif not version and not entries and len(fields) == 1:
            version = line
    return version, entries


def check_file(name, data):
    """-> list of the images in it; raises ValueError if it's not a valid floppy zip / image"""
    if name.lower().endswith(IMAGE_EXTS):
        if name.lower().endswith(".msa") and data[:2] != b"\x0e\x0f":
            raise ValueError("no MSA header")
        if name.lower().endswith(".st") and (len(data) == 0 or len(data) % 512):
            raise ValueError("ST size is not whole sectors")
        return [name]

    try:
        zf = zipfile.ZipFile(io.BytesIO(data))
    except zipfile.BadZipFile:
        start = data[:60].decode("latin-1", "replace").replace("\n", " ").strip()
        raise ValueError("not a zip file (starts with: %r)" % start)
    bad = zf.testzip()
    if bad:
        raise ValueError("broken zip - CRC error in %s" % bad)
    images = [i.filename for i in zf.infolist() if not i.is_dir() and i.filename.lower().endswith(IMAGE_EXTS)]
    if not images:
        raise ValueError("no .st / .msa in the zip (has: %s)" % ", ".join(i.filename for i in zf.infolist())[:200])
    return images


def save(path, data):
    tmp = path + ".part"
    with open(tmp, "wb") as f:
        f.write(data)
    os.replace(tmp, path)


def wayback_index(hosts, pause):
    """lower case file name -> [(timestamp, original url)] of the zips archived under <host>/download/"""
    found = {}
    for host in hosts:
        resume = None
        pages = waits = 0
        while True:
            q = {"url": host + "/download/", "matchType": "prefix", "output": "json", "fl": "timestamp,original",
                 "filter": ["statuscode:200", "mimetype:application/zip"], "collapse": "urlkey", "limit": "5000",
                 "showResumeKey": "true"}
            if resume:
                q["resumeKey"] = resume
            url = WAYBACK_CDX + "?" + urllib.parse.urlencode(q, doseq=True)
            data, _ = fetch_retry(url, tries=5, pause=30, timeout=300, max_size=256 << 20)
            if data.lstrip().startswith(b"<"):      # "Temporarily Offline" page - that's their rate limit
                waits += 1
                if waits > 10:
                    raise RuntimeError("the Wayback Machine keeps refusing - try --wayback again later")
                log("wayback: rate limited, waiting 2 minutes")
                time.sleep(120)
                continue
            rows = json.loads(data or b"[]")
            resume = None
            for row in rows[1:]:
                if len(row) == 1:                   # [resumeKey] after an empty row
                    resume = row[0]
                elif len(row) == 2:
                    ts, orig = row
                    found.setdefault(posixpath.basename(urllib.parse.urlparse(orig).path).lower(), []).append((ts, orig))
            pages += 1
            log("wayback: %s - %d pages, %d names so far" % (host, pages, len(found)))
            if not resume:
                break
            time.sleep(pause)
    return found


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", default="data/archives/cosmosex")
    ap.add_argument("--list", action="append", help="list URL (or local file), can be given more times - default: both cosmosex lists")
    ap.add_argument("--pause", type=float, default=1.0, help="seconds between downloads (default 1)")
    ap.add_argument("--limit", type=int, default=0, help="only the first N entries - for trying it out")
    ap.add_argument("--wayback", action="store_true", help="look for the failed ones in the Wayback Machine, by file name")
    args = ap.parse_args()

    os.makedirs(os.path.join(args.out, "lists"), exist_ok=True)

    # --- the lists, merged (the same URL in both lists only once)
    entries, seen = [], set()
    for src in args.list or DEFAULT_LISTS:
        if os.path.exists(src):
            text = open(src, "rb").read()
        else:
            text, _ = fetch_retry(src)
        save(os.path.join(args.out, "lists", posixpath.basename(urllib.parse.urlparse(src).path) or "list.csv"), text)
        version, items = parse_list(text.decode("latin-1"))
        new = [e for e in items if e[0] not in seen]
        seen.update(e[0] for e in new)
        entries += new
        log("list %s: version %s, %d entries, %d new" % (src, version or "?", len(items), len(new)))
    if args.limit:
        entries = entries[:args.limit]

    # --- download
    results = {}        # name -> dict
    t0 = time.time()
    try:
        download_all(args, entries, results, t0)
    except KeyboardInterrupt:
        log("stopped - writing what's done so far, run it again to go on")
        write_reports(args, results, t0)
        sys.exit(130)
    write_reports(args, results, t0)


def download_all(args, entries, results, t0):
    for n, (url, name, games) in enumerate(entries, 1):
        path = os.path.join(args.out, name)
        r = {"file": name, "url": url, "games": games, "status": "failed", "source": "", "reason": ""}
        results[name] = r
        try:
            if os.path.exists(path):                # from an earlier run - just check it again
                data = open(path, "rb").read()
                r["images"] = check_file(name, data)
                r["status"], r["source"] = "ok", "earlier run"
            else:
                data, final_url = fetch_retry(url)
                r["images"] = check_file(name, data)
                save(path, data)
                r["status"], r["source"] = "ok", final_url
                time.sleep(args.pause)
            r["size"], r["sha256"] = len(data), hashlib.sha256(data).hexdigest()
        except KeyboardInterrupt:
            raise
        except Exception as e:                      # everything else is just this one file failing
            r["reason"] = "%s: %s" % (type(e).__name__, e)
            if not os.path.exists(path):
                time.sleep(args.pause)
        ok = sum(1 for x in results.values() if x["status"].startswith("ok"))
        if n % 25 == 0 or n == len(entries) or r["status"] == "failed" and n < 20:
            eta = (time.time() - t0) / n * (len(entries) - n)
            log("%d/%d  ok %d  failed %d  (%s%s)  ~%d min left" % (n, len(entries), ok, n - ok, name,
                ": " + r["reason"][:80] if r["reason"] else "", eta / 60))

    # --- the Wayback Machine for the failed ones, by the file name (the paths there are different from the lists)
    failed = [r for r in results.values() if r["status"] == "failed"]
    if args.wayback and failed:
        hosts = sorted({urllib.parse.urlparse(r["url"]).netloc.lower().removeprefix("www.") for r in failed})
        log("wayback: looking for %d failed files from %s" % (len(failed), ", ".join(hosts)))
        try:
            index = wayback_index(hosts, pause=5)
        except Exception as e:                      # keep what was downloaded, the reports are written anyway
            log("wayback: can't get the list of archived files: %s" % e)
            index = {}
        for i, r in enumerate(failed, 1):
            cands = index.get(r["file"].lower(), [])
            for ts, orig in sorted(cands, reverse=True):    # newest snapshot first
                try:
                    data, _ = fetch_retry("https://web.archive.org/web/%sid_/%s" % (ts, orig), tries=3, pause=30)
                    r["images"] = check_file(r["file"], data)
                except Exception as e:
                    r["reason"] += " | wayback %s: %s" % (orig, e)
                    time.sleep(3)
                    continue
                save(os.path.join(args.out, r["file"]), data)
                r.update(status="ok-wayback", source="https://web.archive.org/web/%s/%s" % (ts, orig),
                         size=len(data), sha256=hashlib.sha256(data).hexdigest(), reason="")
                if len(cands) > 1:
                    r["reason"] = "%d archived files with this name: %s" % (len(cands), " ".join(o for _, o in cands))
                time.sleep(3)
                break
            if i % 25 == 0:
                log("wayback: %d/%d checked, %d found" % (i, len(failed), sum(1 for x in failed if x["status"] == "ok-wayback")))



def write_reports(args, results, t0):
    with open(os.path.join(args.out, "index.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["file", "status", "source", "size", "sha256", "images", "games", "list_url", "note"])
        for r in results.values():
            w.writerow([r["file"], r["status"], r["source"], r.get("size", ""), r.get("sha256", ""),
                        ";".join(r.get("images", [])), ";".join(r["games"]), r["url"],
                        r["reason"] if r["status"] != "failed" else ""])
    with open(os.path.join(args.out, "failed.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["file", "list_url", "reason"])
        for r in results.values():
            if r["status"] == "failed":
                w.writerow([r["file"], r["url"], r["reason"]])

    ok = sum(1 for r in results.values() if r["status"] == "ok")
    okw = sum(1 for r in results.values() if r["status"] == "ok-wayback")
    log("done: %d entries - %d ok, %d from the Wayback Machine, %d failed (see failed.csv), in %d min" % (
        len(results), ok, okw, len(results) - ok - okw, (time.time() - t0) / 60))


if __name__ == "__main__":
    main()
