#!/usr/bin/env python3
"""
Builds the list of the menu disks on AtariLegend (https://www.atarilegend.com/menusets) - for downloading the floppy
images later. Only the HTML pages are read here, no disk images.

For every disk: menuset, disk title, zip URL, size, SHA-512 checksum, condition, the games / software on it.

  python3 -I tools/crawl_atarilegend.py                # -> data/atarilegend/
  python3 -I tools/crawl_atarilegend.py --menusets 1,188    # only these menusets - for trying it out
  python3 -I tools/crawl_atarilegend.py --refresh         # read the pages again, not from the cache

The pages are kept in <out>/cache, so a run that was stopped (Ctrl+C), or a fix of the parser, doesn't read them from
the site again. One page every --pause seconds.

Output in --out:
  menus.json    everything, per disk (items with their kind: game / software / other, and their AtariLegend URL)
  menus.csv     one line per disk: menuset_id, menuset, disk_id, title, zip_url, size, sha512, condition, games, software
                (games "?" = the site says "Unknown content"; [doc] etc. after a name = only its docs are on the disk)
"""

import argparse
import csv
import fcntl
import html
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

BASE = "https://www.atarilegend.com"
USER_AGENT = "cosmo2ex menu disk list (one-off crawl for a floppy image list; github.com/atarijookie/atari-st-floppy-image-downloader)"


def log(msg):
    print(time.strftime("%H:%M:%S ") + msg, flush=True)


class Fetcher:
    def __init__(self, cache_dir, pause, refresh):
        self.cache_dir, self.pause, self.refresh = cache_dir, pause, refresh
        self.requests = 0
        os.makedirs(cache_dir, exist_ok=True)

    def get(self, url):
        name = re.sub(r"[^A-Za-z0-9]+", "_", url[len(BASE):]).strip("_") or "index"
        path = os.path.join(self.cache_dir, name + ".html")
        if not self.refresh and os.path.exists(path):
            return open(path, encoding="utf-8").read()

        for attempt in range(6):
            if self.requests:
                time.sleep(self.pause)
            self.requests += 1
            try:
                req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
                with urllib.request.urlopen(req, timeout=60) as resp:
                    text = resp.read().decode("utf-8", "replace")
                    left = resp.headers.get("x-ws-ratelimit-remaining")
                    if left is not None and left.isdigit() and int(left) < 50:   # close to their limit - slow down
                        log("rate limit: %s requests left, waiting a minute" % left)
                        time.sleep(60)
                break
            except urllib.error.HTTPError as e:
                if e.code != 429 and e.code < 500:
                    raise
                wait = 30 * (attempt + 1)
            except (urllib.error.URLError, OSError):
                wait = 15 * (attempt + 1)
            log("%s: failed, again in %d s" % (url, wait))
            time.sleep(wait)
        else:
            raise RuntimeError("can't get " + url)

        tmp = path + ".part"
        with open(tmp, "w", encoding="utf-8") as f:
            f.write(text)
        os.replace(tmp, path)
        return text


def text_of(fragment):
    return re.sub(r"\s+", " ", html.unescape(re.sub(r"<[^>]+>", " ", fragment))).strip()


def parse_menusets(page):
    """the /menusets page -> [(id, name)]"""
    found = {}
    for m in re.finditer(r'href="%s/menusets/(\d+)"[^>]*>(.*?)</a>' % re.escape(BASE), page, re.S):
        name = text_of(m.group(2))
        if name and m.group(1) not in found:
            found[m.group(1)] = name
    return sorted(found.items(), key=lambda x: int(x[0]))


def parse_disks(page):
    """one page of a menuset -> [disk], next page URL (or None)"""
    t = re.sub(r"\s+", " ", page)

    # the disk cards, up to the paging / the side column (which has the 'latest menus' with other disks)
    end = len(t)
    for marker in ('<ul class="pagination', 'class="col col-sm-6 col-lg-3 order-3"'):
        i = t.find(marker)
        if i != -1:
            end = min(end, i)
    starts = [m.start() for m in re.finditer(r'id="menudisk-\d+"', t[:end])]

    disks = []
    for n, start in enumerate(starts):
        card = t[start:starts[n + 1] if n + 1 < len(starts) else end]
        disk_id = re.match(r'id="menudisk-(\d+)"', card).group(1)

        h3 = re.search(r"<h3[^>]*>(.*?)</h3>", card)
        title = ""
        if h3:
            inner = re.sub(r'<a [^>]*data-bs-toggle="collapse".*?</a>', "", h3.group(1))   # the 'txt' scrolltext button
            inner = re.sub(r'<a [^>]*Permalink.*?</a>', "", inner)
            title = text_of(inner)

        body_end = card.find('class="card-footer')
        body = card[:body_end if body_end != -1 else len(card)]
        body = re.sub(r'<div class="collapse[^"]*" id="scrolltext-.*?</code>', "", body)   # scroll text, has no items

        items = []
        for li in re.findall(r"<li>(.*?)</li>", body):
            a = re.search(r'href="([^"]*)"', li)
            url = a.group(1) if a else ""
            kind = "game" if "/games/" in url else ("software" if "/menusets/software/" in url else "other")
            tag = re.search(r'<small class="text-muted">(.*?)</small>', li)     # e.g. [doc], [doc + solution]
            name = text_of(re.sub(r'<small class="text-muted">.*?</small>', "", li))
            if name:
                items.append({"name": name, "kind": kind, "tag": text_of(tag.group(1)) if tag else "", "url": url})

        notes = re.search(r'<span class="text-muted">Notes: </span>(.*?)</p>', body)
        footer = card[body_end:] if body_end != -1 else ""
        cond = re.search(r'Condition:</span>(.*?)</div>', footer)
        sha = re.search(r'<abbr [^>]*title="([0-9a-f]{64,128})"', footer)
        zip_a = re.search(r'href="([^"]*/storage/zips/[^"]*)"(?: download="([^"]*)")?', footer)
        size = re.search(r'Download disk dump.*?<small[^>]*>(.*?)</small>', footer)

        zip_url = zip_a.group(1) if zip_a else ""
        zip_id = re.search(r"/(\d+)\.zip$", zip_url)
        disks.append({
            "disk_id": int(disk_id),                        # the card on the page (menusets/<set>#menudisk-<id>)
            "zip_id": int(zip_id.group(1)) if zip_id else None,     # the number of the zip - not always the same
            "title": title,
            "content_unknown": "Unknown content" in body,
            "zip_url": zip_url,
            "download_name": html.unescape(zip_a.group(2) or "") if zip_a else "",
            "size": text_of(size.group(1)) if size else "",
            "sha512": sha.group(1) if sha else "",
            "condition": text_of(cond.group(1)) if cond else "",
            "has_scrolltext": "scrolltext-" + disk_id in card,
            "notes": text_of(notes.group(1)) if notes else "",
            "items": items,
        })

    nxt = re.search(r'href="([^"]*)" rel="next"', t)
    return disks, (html.unescape(nxt.group(1)) if nxt else None)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", default="data/atarilegend")
    ap.add_argument("--pause", type=float, default=1.5, help="seconds between page requests (default 1.5)")
    ap.add_argument("--menusets", help="only these menuset ids, e.g. 1,188")
    ap.add_argument("--refresh", action="store_true", help="read the pages from the site again, not from the cache")
    args = ap.parse_args()

    fetcher = Fetcher(os.path.join(args.out, "cache"), args.pause, args.refresh)

    lock = open(os.path.join(args.out, "cache", ".lock"), "w")    # two runs would write the same cache files
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        sys.exit("another run with --out %s is running" % args.out)
    menusets = parse_menusets(fetcher.get(BASE + "/menusets"))
    log("%d menusets" % len(menusets))
    if args.menusets:
        wanted = set(args.menusets.split(","))
        menusets = [m for m in menusets if m[0] in wanted]

    result, t0 = [], time.time()
    try:
        for n, (set_id, set_name) in enumerate(menusets, 1):
            url, pages, seen = "%s/menusets/%s" % (BASE, set_id), 0, set()
            while url and url not in seen and pages < 200:
                seen.add(url)
                disks, url = parse_disks(fetcher.get(url))
                pages += 1
                for d in disks:
                    result.append(dict(menuset_id=int(set_id), menuset=set_name, **d))
            if n % 10 == 0 or n == len(menusets):
                eta = (time.time() - t0) / n * (len(menusets) - n)
                log("%d/%d menusets, %d disks, %d requests, ~%d min left" % (n, len(menusets), len(result),
                                                                             fetcher.requests, eta / 60))
    except KeyboardInterrupt:
        log("stopped - writing what's done, run it again to go on (the read pages are cached)")

    write(args.out, result)
    no_zip = sum(1 for d in result if not d["zip_url"])
    log("done: %d disks in %d menusets, %d without a zip, %d requests to the site" % (
        len(result), len({d["menuset_id"] for d in result}), no_zip, fetcher.requests))


def write(out, disks):
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "menus.json"), "w", encoding="utf-8") as f:
        json.dump(disks, f, ensure_ascii=False, indent=1)
    with open(os.path.join(out, "menus.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["menuset_id", "menuset", "disk_id", "title", "zip_url", "size", "sha512", "condition", "games",
                    "software"])
        for d in disks:
            def names(game):
                return ";".join(i["name"] + (" " + i["tag"] if i["tag"] else "") for i in d["items"]
                                if (i["kind"] == "game") == game)
            w.writerow([d["menuset_id"], d["menuset"], d["disk_id"], d["title"], d["zip_url"], d["size"], d["sha512"],
                        d["condition"], "?" if d["content_unknown"] else names(True), names(False)])


if __name__ == "__main__":
    main()
