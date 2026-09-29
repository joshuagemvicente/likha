#!/bin/sh
# Extract a release like a new user and exercise the binary on this host.
set -eu

usage() {
    printf 'Usage: %s path/to/lisa_vVERSION_OS_ARCH.tar.gz vVERSION\n' "$0" >&2
    exit 2
}

[ "$#" -eq 2 ] || usage
case "$1" in
    /*) archive=$1 ;;
    *) archive=$(pwd -P)/$1 ;;
esac
version=$2
[ -f "$archive" ] || { printf 'Archive not found: %s\n' "$archive" >&2; exit 1; }
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$'; then
    usage
fi

case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) printf 'Unsupported host operating system\n' >&2; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) printf 'Unsupported host architecture\n' >&2; exit 1 ;;
esac
expected="lisa_${version}_${os}_${arch}.tar.gz"
[ "$(basename "$archive")" = "$expected" ] || {
    printf 'Wrong release target or version: expected %s\n' "$expected" >&2
    exit 1
}

# Refuse unexpected archive entries before unpacking into a project-local directory.
contents=$(tar -tzf "$archive") || { printf 'Cannot inspect archive: %s\n' "$archive" >&2; exit 1; }
expected_contents=$(printf 'lisa\nREADME.md\nLICENSE')
[ "$contents" = "$expected_contents" ] || { printf 'Unexpected archive contents: %s\n' "$archive" >&2; exit 1; }

root=$(CDPATH= cd "$(dirname "$0")/.." && pwd -P)
staging=$(mktemp -d "$root/.smoke-release.XXXXXXXX")
cleanup() { rm -rf "$staging"; }
trap cleanup 0
trap 'exit 1' 1 2 3 15
tar -xzf "$archive" -C "$staging"
[ -f "$staging/README.md" ] && [ -f "$staging/LICENSE" ] && [ -x "$staging/lisa" ] || {
    printf 'Missing README.md, LICENSE, or executable lisa in archive\n' >&2
    exit 1
}
actual=$("$staging/lisa" --version)
[ "$actual" = "Lisa $version" ] || {
    printf 'Wrong binary version: expected Lisa %s, got %s\n' "$version" "$actual" >&2
    exit 1
}
help=$("$staging/lisa" --help)
case "$help" in
    *'Usage: lisa'*) ;;
    *) printf 'Installed binary did not print Lisa usage\n' >&2; exit 1 ;;
esac
printf 'Installed %s locally; --version and --help passed\n' "$expected"
