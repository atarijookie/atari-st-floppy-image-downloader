#!/bin/sh
# Builds the downloader for all platforms into dist/ - pure Go, no C toolchain needed:
#   ./build.sh            version from git (tag, or commit)
#   ./build.sh 1.0.0      that version
set -eu
cd "$(dirname "$0")"

VERSION=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
NAME=atari-st-floppy-image-downloader
mkdir -p dist

# os/arch[/arm version] -> file name suffix
for target in linux/amd64 linux/arm64 linux/arm/6 linux/386 windows/amd64 windows/386 darwin/amd64 darwin/arm64; do
    os=${target%%/*}; rest=${target#*/}; arch=${rest%%/*}; arm=""
    [ "$arch" != "$rest" ] && arm=${rest#*/}
    suffix="$os-$arch${arm:+v$arm}"
    ext=""; [ "$os" = windows ] && ext=".exe"
    echo "building $suffix"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=$arm go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
        -o "dist/$NAME-$suffix$ext" .
done
ls -l dist
