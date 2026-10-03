# Tasks: curl-based public installation

Ordered implementation tasks. Each task includes its verification.

Status: all unchecked. See [spec.md](spec.md) for behavior wording.

## 1. Installer script `scripts/install.sh`

- [x] POSIX `sh` script, `set -eu`, no bashisms; usage message lists the
      overridable environment variables.
- [x] Validate version token with the same regex `scripts/release.sh` uses.
- [x] Resolve version: first argument wins; else `LIKHA_VERSION`; conflicting
      values are an error; else resolve latest via `LIKHA_RELEASE_API` response
      `tag_name`.
- [x] Detect host OS/arch with `uname -s` / `uname -m`, mapping is the same as
      `scripts/smoke-release.sh`.
- [x] Download base scheme check: refuse non-`https` bases except numeric
      ports on `127.0.0.1`/`localhost`, and reject authority tricks such as
      `127.0.0.1:8000@evil.example.com` (userinfo embedding).
- [x] Download archive + checksum manifest into a `mktemp` private directory;
      clean it up on all exits (`trap ... 0`, `trap 'exit 1' 1 2 3 15`).
- [x] Verify SHA-256 of the downloaded archive against the manifest line for
      that exact filename (`sha256sum`, else `shasum -a 256`).
- [x] Extract; verify listing is exactly `likha`, `README.md`, `LICENSE`; run
      `--version` (expected `Likha <VERSION>`).
- [x] Install with `mv` into `LIKHA_INSTALL_DIR` (default `$HOME/.local/bin`),
      creating the directory if needed, without sudo; abort with an
      instruction if it is not writable.
- [x] Print success line with version and path; print PATH advice when the
      install dir is not in `PATH`; note an existing `likha` found earlier in
      `PATH`.
- **Verify:** done — success, checksum-mismatch, bogus-version 404,
      non-HTTPS base, userinfo-port trick, env/arg conflict, invalid
      version-token paths (all against local mirrors on macOS arm64, no
      temp leftovers).

## 2. Documentation

- [x] `README.md` gains an "Install a published release with curl" section:
      one-liner, pinned form, overrides, verification note, and status caveat
      (works once a real release is published; manual walkthrough remains the
      release gate).
- [x] Specs index `specs/README.md` lists this feature folder.
- **Verify:** read the section top-to-bottom while pretending to be a new
  user; each instruction must be copy-pasteable on macOS and Linux.

## 3. Preflight checks (no code)

- [x] Confirm `scripts/release.sh` asset names match what the installer
      downloads (`likha_<VERSION>_<OS>_<ARCH>.tar.gz`,
      `likha_<VERSION>_checksums.txt`).
- [x] Confirm raw.githubusercontent URL pattern works for a repo with no
      published repo yet: the URL is fixed in the installer header; the
      release publication step later must not need installer edits.
- **Verify:** names checked against `scripts/release.sh` source; documented
  in [context.md](context.md).

## 4. Local smoke test (mirrors spec acceptance)

- [x] Build release: `./scripts/release.sh v0.9.9` (test tag).
- [x] Serve a versioned mirror tree on loopback: `python3 -m http.server`.
- [x] Run `LIKHA_RELEASE_BASE=http://127.0.0.1:8742 LIKHA_INSTALL_DIR=/tmp/likha-smoke/bin
      ./scripts/install.sh v0.9.9`; expect installed binary reporting
      `Likha v0.9.9`.
- [x] Tamper a copy of the archive (flip a byte) and rerun; expect
      checksum-refusal, nothing installed, temp cleaned.
- [x] Latest-release lookup pinned through `LIKHA_RELEASE_API` JSON; expect
      tag parsed from the fixture and the same install result.
- **Verify:** done; run against local mirrors only; no permanent test
  scripts committed. Unsupported host OS/arch cannot be exercised on macOS
  arm64 (platform check reviewed against `smoke-release.sh` mapping only).
