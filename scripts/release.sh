#!/bin/sh
# Build local, versioned release archives; publishing is a separate, explicit step.
set -eu

usage() {
    printf 'Usage: %s vMAJOR.MINOR.PATCH[-PRERELEASE]\n' "$0" >&2
    exit 2
}

[ "$#" -eq 1 ] || usage
version=$1
# Keep linker arguments and archive names predictable; never accept path components.
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$'; then
    usage
fi

root=$(CDPATH= cd "$(dirname "$0")/.." && pwd -P)
dist=$root/dist
[ -f "$root/go.mod" ] && [ -f "$root/go.sum" ] && [ -f "$root/README.md" ] && [ -f "$root/LICENSE" ] || {
    printf 'Missing go.mod, go.sum, README.md, or LICENSE in %s\n' "$root" >&2
    exit 1
}
command -v go >/dev/null 2>&1 || { printf 'Go is required to build releases\n' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool=shasum
else
    printf 'sha256sum or shasum is required\n' >&2
    exit 1
fi

artifact_available() {
    [ ! -e "$1" ] && [ ! -L "$1" ]
}

[ ! -L "$dist" ] || { printf 'Release directory must not be a symlink: %s\n' "$dist" >&2; exit 1; }
mkdir -p "$dist"
for os in darwin linux; do
    for arch in amd64 arm64; do
        archive="likha_${version}_${os}_${arch}.tar.gz"
        artifact_available "$dist/$archive" || { printf 'Release artifact already exists: %s\n' "$dist/$archive" >&2; exit 1; }
    done
done
manifest="likha_${version}_checksums.txt"
artifact_available "$dist/$manifest" || { printf 'Release artifact already exists: %s\n' "$dist/$manifest" >&2; exit 1; }

staging=$(mktemp -d "$dist/.release.XXXXXXXX")
cleanup() { rm -rf "$staging"; }
trap cleanup 0
trap 'exit 1' 1 2 3 15

for os in darwin linux; do
    for arch in amd64 arm64; do
        target=$staging/$os-$arch
        mkdir "$target"
        (cd "$root" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOWORK=off GOFLAGS= go build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w -X likha/internal/tui.Version=$version" -o "$target/likha" ./cmd/likha)
        cp "$root/README.md" "$root/LICENSE" "$target/"
        chmod 755 "$target/likha"
        archive="likha_${version}_${os}_${arch}.tar.gz"
        # macOS tar otherwise adds AppleDouble ._ entries that its own listing hides.
        COPYFILE_DISABLE=1 tar -czf "$staging/$archive" -C "$target" likha README.md LICENSE
    done
done
(
    cd "$staging"
    : > "$manifest"
    for os in darwin linux; do
        for arch in amd64 arm64; do
            archive="likha_${version}_${os}_${arch}.tar.gz"
            if [ "$checksum_tool" = sha256sum ]; then
                sha256sum "$archive" >> "$manifest"
            else
                shasum -a 256 "$archive" >> "$manifest"
            fi
        done
    done
)

for os in darwin linux; do
    for arch in amd64 arm64; do
        archive="likha_${version}_${os}_${arch}.tar.gz"
        artifact_available "$dist/$archive" || { printf 'Release artifact already exists: %s\n' "$dist/$archive" >&2; exit 1; }
        mv "$staging/$archive" "$dist/$archive"
        printf '%s\n' "$dist/$archive"
    done
done
artifact_available "$dist/$manifest" || { printf 'Release artifact already exists: %s\n' "$dist/$manifest" >&2; exit 1; }
mv "$staging/$manifest" "$dist/$manifest"
printf '%s\n' "$dist/$manifest"
