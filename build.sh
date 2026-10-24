#!/bin/sh
# Builds the downloader for all platforms - pure Go, no C toolchain needed:
#   ./build.sh            version from git (tag, or commit)
#   ./build.sh 1.0.0      that version
#
# dist/build/<package>/   the program + README.md, per platform - to run it from here
# dist/<package>.zip      Windows        <package> = atari-st-floppy-image-downloader-<version>-<os>-<arch>
# dist/<package>.tar.gz   Linux, macOS   (keeps the program executable)
# dist/imagelist.json.gz  the image list (from list/), published with the release
# dist/SHA256SUMS         of all the above
set -eu
cd "$(dirname "$0")"

VERSION=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
NAME=atari-st-floppy-image-downloader

rm -rf dist
mkdir -p dist/build

# GNU tar: no owner / group of the build machine in the archive (bsdtar on macOS doesn't know these options)
TAR_OPTS=""
if tar --version 2>/dev/null | grep -q GNU; then
    TAR_OPTS="--owner=0 --group=0"
fi

# os/arch[/arm version]
for target in linux/amd64 linux/arm64 linux/arm/6 linux/386 windows/amd64 windows/386 darwin/amd64 darwin/arm64; do
    os=${target%%/*}; rest=${target#*/}; arch=${rest%%/*}; arm=""
    if [ "$arch" != "$rest" ]; then arm=${rest#*/}; fi
    pkg="$NAME-$VERSION-$os-$arch${arm:+v$arm}"
    ext=""
    if [ "$os" = windows ]; then ext=".exe"; fi

    echo "building $pkg"
    mkdir -p "dist/build/$pkg"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=$arm go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
        -o "dist/build/$pkg/$NAME$ext" .
    cp README.md "dist/build/$pkg/"
    if [ -f LICENSE ]; then cp LICENSE "dist/build/$pkg/"; fi

    if [ "$os" = windows ]; then
        # zip with Python - it's there wherever the list tools run, the zip program often isn't
        python3 -I - "dist/$pkg.zip" "dist/build" "$pkg" <<'EOF'
import os, sys, zipfile
out, base, pkg = sys.argv[1:4]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
    for root, dirs, files in os.walk(os.path.join(base, pkg)):
        dirs.sort()
        for f in sorted(files):
            path = os.path.join(root, f)
            zf.write(path, os.path.relpath(path, base))
EOF
    else
        chmod 755 "dist/build/$pkg/$NAME"
        # shellcheck disable=SC2086 - TAR_OPTS is meant to split
        tar -C dist/build $TAR_OPTS -czf "dist/$pkg.tar.gz" "$pkg"
    fi
done

if [ -f list/imagelist.json.gz ]; then cp list/imagelist.json.gz dist/; fi

(cd dist && sha256sum -- *.zip *.tar.gz *.json.gz > SHA256SUMS)
ls -l dist
