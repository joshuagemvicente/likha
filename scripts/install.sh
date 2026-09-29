#!/bin/sh
# Install the latest (or a pinned) published Lisa release for this machine.
#
# Public one-liner:
#   curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh
#
# Pin a version:
#   curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh -s -- v1.2.3
#
# Environment overrides:
#   LISA_VERSION      release tag to install (same as the optional argument)
#   LISA_RELEASE_BASE download base; <BASE>/<VERSION>/<archive> is the URL.
#                     Default: https://github.com/gem/lisa/releases/download
#   LISA_RELEASE_API  latest-release lookup URL; used only when no version is
#                     pinned. Default: https://api.github.com/repos/gem/lisa/releases/latest
#   LISA_INSTALL_DIR  target directory; default: $HOME/.local/bin
#
# The archive is checksum-verified against the release's checksum manifest
# before installation. No root privileges are used.
set -eu

DEFAULT_BASE='https://github.com/gem/lisa/releases/download'
DEFAULT_API='https://api.github.com/repos/gem/lisa/releases/latest'

fail() {
    printf 'install.sh: %s\n' "$*" >&2
    exit 1
}

if [ "$#" -gt 1 ]; then
    printf 'Usage: install.sh [vVERSION]\n' >&2
    printf 'Environment overrides: LISA_VERSION, LISA_RELEASE_BASE, LISA_RELEASE_API, LISA_INSTALL_DIR\n' >&2
    exit 2
fi

command -v curl >/dev/null 2>&1 || fail 'curl is required to download a release'
command -v tar >/dev/null 2>&1 || fail 'tar is required to unpack a release'
if command -v sha256sum >/dev/null 2>&1; then
    checksum_tool='sha256sum'
elif command -v shasum >/dev/null 2>&1; then
    checksum_tool='shasum -a 256'
else
    fail 'sha256sum or shasum is required to verify the release checksum'
fi

base=${LISA_RELEASE_BASE:-$DEFAULT_BASE}
api=${LISA_RELEASE_API:-$DEFAULT_API}
install_dir=${LISA_INSTALL_DIR:-"$HOME/.local/bin"}

# Plain HTTP is permitted only for loopback test hosts. Each pattern is
# followed by an authority check so userinfo tricks like
# 127.0.0.1:8000@evil.com are refused: the characters right after scheme and
# host must be a numeric port and digits only.
http_ok=0
case $base in
    https://*) http_ok=1 ;;
    http://127.0.0.1/[!@]*|http://127.0.0.1|http://localhost/[!@]*|http://localhost) http_ok=1 ;;
    http://127.0.0.1:[!@]*|http://localhost:[!@]*)
        if [ "${base#http://127.0.0.1:}" != "$base" ]; then
            rest=${base#http://127.0.0.1:}
        else
            rest=${base#http://localhost:}
        fi
        case $rest in
            */*) portpart=${rest%%/*} ;;
            *) portpart=$rest ;;
        esac
        case $portpart in
            *[!0-9]*|'') : ;;
            *) http_ok=1 ;;
        esac
        ;;
esac
[ "$http_ok" = 1 ] || fail "download base must use HTTPS (refusing: $base)"

# Resolve the release version: argument wins, then LISA_VERSION, then latest.
version=${1:-}
if [ -n "${LISA_VERSION:-}" ]; then
    [ -z "$version" ] || [ "$version" = "$LISA_VERSION" ] || \
        fail "version argument ($version) conflicts with LISA_VERSION ($LISA_VERSION)"
    version=$LISA_VERSION
fi
if [ -z "$version" ]; then
    printf 'install.sh: resolving latest release...\n'
    listing=$(curl -fsSL --retry 2 "$api") || fail "cannot resolve the latest release from $api (rate limited?). Pin one instead: ... | sh -s -- vX.Y.Z"
    version=$(printf '%s\n' "$listing" | sed -n 's/.*"tag_name"[ :]*"\([^"]*\)".*/\1/p' | sed -n '1p')
    [ -n "$version" ] || fail "no tag_name in the latest-release response; pin a version instead: ... | sh -s -- vX.Y.Z"
fi
# Same validation as scripts/release.sh: no URL path components, no surprises.
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$' || {
    printf "install.sh: invalid version '%s' (want vMAJOR.MINOR.PATCH[-PRERELEASE])\n" "$version" >&2
    exit 2
}

# Host platform, same mapping as scripts/smoke-release.sh.
case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "unsupported operating system for this installer: $(uname -s). Supported: macOS (darwin), Linux" ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) fail "unsupported machine architecture: $(uname -m). Supported: amd64 (x86_64), arm64 (aarch64)" ;;
esac

archive="lisa_${version}_${os}_${arch}.tar.gz"
manifest="lisa_${version}_checksums.txt"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/lisa-install.XXXXXXXX") || fail 'cannot create a temporary directory'
trap 'rm -rf "$tmp"' 0
trap 'exit 1' 1 2 3 15

printf "install.sh: downloading %s ...\n" "$archive"
# --fail: HTTP errors abort instead of saving an error page into the file.
curl -fsSL --retry 2 -o "$tmp/$archive" "$base/$version/$archive" || \
    fail "download failed for $base/$version/$archive (unknown version or network problem)"
curl -fsSL --retry 2 -o "$tmp/$manifest" "$base/$version/$manifest" || \
    fail "download failed for checksum manifest at $base/$version/$manifest"

expected=$(awk -v a="$archive" '($2 == a || $2 == "*"a) && $1 ~ /^[0-9a-fA-F]{64}$/ { print $1; exit }' "$tmp/$manifest")
[ -n "$expected" ] || fail "the checksum manifest does not list $archive (manifest is for another release?)"
actual=$($checksum_tool "$tmp/$archive")
actual=$(printf '%s\n' "$actual" | awk '{ print $1 }')
if [ "$actual" != "$expected" ]; then
    printf "install.sh: checksum mismatch for %s: expected %s, got %s\n" "$archive" "$expected" "$actual" >&2
    fail 'refusing to install a modified archive; nothing was installed'
fi
printf 'install.sh: checksum verified (%s)\n' "$expected"

contents=$(tar -tzf "$tmp/$archive") || fail 'cannot inspect the downloaded archive'
expected_contents=$(printf 'lisa\nREADME.md\nLICENSE')
[ "$contents" = "$expected_contents" ] || fail 'unexpected archive contents; aborting'

tar -xzf "$tmp/$archive" -C "$tmp"
[ -x "$tmp/lisa" ] || fail 'archive has no executable lisa binary'

reported=$("$tmp/lisa" --version) || fail 'downloaded binary failed to run'
[ "$reported" = "Lisa $version" ] || fail "binary reports '$reported', expected 'Lisa $version'"

case $install_dir in
    /*) ;;
    *) fail "LISA_INSTALL_DIR must be an absolute path (got: $install_dir)" ;;
esac
mkdir -p "$install_dir" || fail "cannot create $install_dir"
[ -d "$install_dir" ] || fail "not a usable directory: $install_dir"
[ -w "$install_dir" ] || fail "$install_dir is not writable by you; set LISA_INSTALL_DIR to a directory you can write, e.g. export LISA_INSTALL_DIR=\"\$HOME/.local/bin\""

# Replace any previous binary in one shot; the smoke checks above already ran.
mv "$tmp/lisa" "$install_dir/lisa"

printf 'Installed Lisa %s at %s/lisa\n' "$version" "$install_dir"
"$install_dir/lisa" --version

# PATH advice: only when the final binary is not the one the shell would find.
found=$(command -v lisa 2>/dev/null || true)
if [ "$found" != "$install_dir/lisa" ]; then
    case ":$PATH:" in
        *":$install_dir:"*)
            if [ -n "$found" ]; then
                printf 'install.sh: note: your PATH finds an earlier lisa at %s ahead of %s\n' "$found" "$install_dir/lisa"
            fi
            ;;
        *)
            printf 'install.sh: add to your shell profile so future shells find it:\n'
            printf '  export PATH="%s:$PATH"\n' "$install_dir"
            ;;
    esac
fi
