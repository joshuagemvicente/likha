# Installing and releasing

Install a published release, or build, verify, and install local release archives. Back to the [README](../README.md).

**Release status:** Local scripts build macOS and Linux archives; no release has been published. A local macOS ARM64 archive passed the documented checksum/install checks and an interactive workflow against a protocol server. `opencode-go` is **live-verified** (user session on 2026-09-29: setup flow, connection check, streaming, and agent tool usage with a real key); repeat the full probe before release after provider-facing request changes. `opencode-zen` and every provider marked pending-probe in the [provider table](providers.md#predefined-providers) await live verification, including ChatGPT (no live probe with a real Plus/Pro account has run). See the [v1 specification](../specs/v1-spec.md), [feature test plan](../specs/feature-test-plan.md), [setup plan](../specs/setup-plan.md), and [unreleased changes](../CHANGELOG.md).

## Install a published release with curl

When a release is published for [joshuagemvicente/likha](https://github.com/joshuagemvicente/likha), a developer on macOS or Linux can install it without cloning this repository:

```sh
curl -fsSL https://raw.githubusercontent.com/joshuagemvicente/likha/main/scripts/install.sh | sh
```

The installer detects your OS and architecture (darwin/linux × amd64/arm64), downloads that release's archive and its SHA-256 checksum manifest, refuses any archive whose checksum does not match the manifest, checks that the binary reports the release version, and installs it to `~/.local/bin/likha` without sudo. Pin a version for CI or reproducibility:

```sh
curl -fsSL https://raw.githubusercontent.com/joshuagemvicente/likha/main/scripts/install.sh | sh -s -- v1.2.3
```

Optional overrides: `LIKHA_VERSION` (same as the pinned argument; conflicting values are refused), `LIKHA_INSTALL_DIR` (default `$HOME/.local/bin`), and, for self-hosted mirrors or tests, `LIKHA_RELEASE_BASE` (each release lives under `<BASE>/<VERSION>/`; plain HTTP is accepted only for loopback hosts) and `LIKHA_RELEASE_API` (latest-release lookup URL). If `~/.local/bin` is not on your `PATH`, the installer prints the `export PATH=...` line to add.

What this does **not** prove: the checksum manifest comes from the same host as the archive, so verification protects against corrupted or truncated downloads, not against a compromised release host. Latest-release resolution uses the GitHub API, which is rate-limited; if the lookup fails, pin a version.

This has been exercised locally against a served release on macOS ARM64 (checksum mismatch, unknown version, and success paths), and on 2026-10-04 a `curl | sh` install over loopback with `LIKHA_RELEASE_BASE` passed checksum, tamper, and version guards plus an installed-binary smoke check. That was a headless approximation, and no real release has been published yet; the [published-binary walkthrough](#manual-tui-release-walkthrough-still-required) remains the release gate before relying on it.

## Build, verify, and install a local archive

From the Likha project root, `release.sh` builds four versioned archives (`darwin`/`linux` × `amd64`/`arm64`) in `dist/`, each containing `likha`, `README.md`, and `LICENSE`, plus a SHA-256 manifest. It **does not publish** them:

```sh
./scripts/release.sh v1.2.3
```

Select the archive matching your machine, for example `darwin_arm64` on Apple Silicon, `darwin_amd64` on Intel macOS, `linux_amd64` on x86-64 Linux, or `linux_arm64` on ARM64 Linux. Verify the locally generated files **before extraction** (the manifest checks all four archives, so keep them together in `dist/`):

```sh
cd dist
shasum -a 256 -c likha_v1.2.3_checksums.txt  # macOS
# or: sha256sum -c likha_v1.2.3_checksums.txt # Linux
cd ..
```

Then run the local installation smoke script. Its interface is `./scripts/smoke-release.sh dist/likha_v1.2.3_<host>_<arch>.tar.gz v1.2.3`, with `<host>`/`<arch>` replaced by the selected target. For example, on Apple Silicon:

```sh
./scripts/smoke-release.sh dist/likha_v1.2.3_darwin_arm64.tar.gz v1.2.3
```

This extracts into a temporary directory and checks `--version` and `--help`, **not** model connectivity. To install the verified archive locally, substitute the same filename below:

```sh
archive=dist/likha_v1.2.3_darwin_arm64.tar.gz  # example: replace for your host
tmp=$(mktemp -d)
tar -xzf "$archive" -C "$tmp"
mkdir -p "$HOME/.local/bin"
install -m 755 "$tmp/likha" "$HOME/.local/bin/likha"
rm -rf "$tmp"
"$HOME/.local/bin/likha" --version
```

Ensure `$HOME/.local/bin` is on your `PATH`, or invoke the binary by its full path. The archive's README and LICENSE are available by extracting them alongside the binary. For an ordinary source build instead, run `go build -o bin/likha ./cmd/likha`.

## Manual TUI release walkthrough (still required)

This is a **procedure to perform**, not a claim that it passed on any published archive or provider:

1. On each supported host/architecture, verify the checksum, install its local archive, check `--version` and `--help`, and run `"$HOME/.local/bin/likha" /path/to/test-repository` in an interactive terminal. Complete the first-run setup with a hosted provider (`--provider opencode-go` with a key skips setup) and confirm the connection check reports **Connected** before prompting.
2. Ask Likha to use `read` or `grep`; check the returned repository content. Ask it to use `edit_file` on a disposable file. Inspect the diff, choose **Decline**, and confirm the file did not change. Ask again, review to the end, choose **Approve**, and confirm the displayed change was applied. Try a multi-page diff and a narrow viewport.
3. Ask it to use `run_command` for a harmless check such as `git status --short`. Inspect the exact command and working directory; decline once, then request it again, review to the end, approve, and inspect the actual output and exit status. Test cancellation without granting permission.
4. Exit with Ctrl+D, run `"$HOME/.local/bin/likha" --sessions /path/to/test-repository`, and resume its ID with `--resume SESSION_ID`. Confirm completed conversation and tool results return, but an interrupted pending review never executes after relaunch. Check that another repository cannot list or resume that session.

Release publication and provider compatibility remain unverified until this walkthrough succeeds on the intended targets with a live-verified provider. For source checks, run `go test ./...` from the Likha project root.
