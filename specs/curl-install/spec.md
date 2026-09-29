# Spec: curl-based public installation

**Status:** implemented (local) — no release published; the curl install is verified only against served local artifacts until a real release exists

## Context

Lisa needs a developer-friendly install path for people who never clone this
repository: a single `curl | sh` line that installs the correct release binary
on macOS or Linux. [v1-spec.md](../v1-spec.md) FR-13 requires that a user can
install and run a versioned macOS/Linux release using the documentation alone;
this feature turns that into a one-line, checksum-verified, no-root install.

Releases (per `scripts/release.sh`) are four `tar.gz` archives
(`darwin`/`linux` × `amd64`/`arm64`, each containing `lisa`, `README.md`,
`LICENSE`) plus a SHA-256 manifest `lisa_<VERSION>_checksums.txt`, published as
GitHub Release assets of `gem/lisa`. The installer is hosted in this repository
and fetched by URL, so it must not depend on a checkout of Lisa.

## User-visible behavior

1. A developer on macOS or Linux installs Lisa with:

   ```
   curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh
   ```

   This installs the latest published release's binary for the running
   OS/architecture into `~/.local/bin/lisa` and prints a confirmation line
   containing the installed version and final binary path. No root privileges
   are used; the script never asks for a password or runs `sudo`.

2. A developer can pin a version:

   ```
   curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh -s -- v1.2.3
   ```

   The pinned-tag argument behaves identically but skips latest-release
   lookup.

3. Before anything runs, the installer detects the host OS (`Darwin`→`darwin`,
   `Linux`→`linux`) and architecture (`x86_64`/`amd64`→`amd64`,
   `arm64`/`aarch64`→`arm64`) and refuses any other platform with an
   actionable message naming the supported targets.

4. The installer downloads exactly two artifacts over HTTPS from the release
   base: `lisa_<VERSION>_<OS>_<ARCH>.tar.gz` and
   `lisa_<VERSION>_checksums.txt`. It computes the archive's SHA-256 digest and
   verifies it against the manifest line for that exact archive filename. A
   mismatch, a missing manifest entry, a truncated download, or an unexpected
   archive listing aborts the install with a clear error and leaves nothing
   installed.

5. The extracted `lisa` binary must report `Lisa <VERSION>` for its
   `--version` flag; anything else aborts the install. Readiness checks
   (`--version`, `--help`) run from a private temporary directory before the
   binary is moved into place.

6. Environment overrides (all optional, for mirrors and testing):

   - `LISA_VERSION=<tag>` — equivalent to the pinned argument. Setting both
     with different values is an error.
   - `LISA_RELEASE_BASE=<URL>` — base where `<BASE>/<VERSION>/<archive>` is
     the download URL. Default is
     `https://github.com/gem/lisa/releases/download`.
   - `LISA_RELEASE_API=<URL>` — latest-release lookup; default
     `https://api.github.com/repos/gem/lisa/releases/latest`. Used only when
     no version is pinned.
   - `LISA_INSTALL_DIR=<dir>` — target directory; default `$HOME/.local/bin`.

7. Security boundaries:

   - Plain `http://` download bases are refused except for numeric ports on
     loopback hosts (`127.0.0.1`, `localhost`), permitted for local testing
     only; an embedded-authority URL like `127.0.0.1:8000@evil.example.com`
     is also refused.
   - The version token is validated (`vMAJOR.MINOR.PATCH[-PRERELEASE]`) so it
     can never introduce URL path components.
   - The checksum manifest is downloaded from the same host as the archive, so
     verification protects against truncated/corrupted downloads, not against
     a compromised release host. Publication integrity relies on GitHub
     release infrastructure and TLS; the documented verification story says
     exactly this and does not overclaim.

8. Temporary files live in a private `mktemp` directory and are removed on
   every exit path, successful or not.

## Acceptance criteria

- [ ] On this machine (darwin arm64), running the installer against a served
      local release (via `LISA_RELEASE_BASE` loopback override) installs a
      binary whose `--version` matches the served tag, without root.
- [ ] A wrong checksum (archive tampered after manifest generation) aborts
      with a checksum error and installs nothing.
- [ ] A request for a nonexistent version aborts with a download/lookup error
      and installs nothing; a prior installation (if any) remains untouched.
- [ ] Unsupported OS/architecture input aborts before any download.
- [ ] A non-loopback `http://` base is refused before any download.
- [ ] No installation remnants survive a failed run, and a successful run
      leaves no temporary files behind.
- [ ] `README.md` documents the one-liner, pinned version, overrides, what is
      verified, and the still-open release gate.
