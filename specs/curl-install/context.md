# Context: curl-based public installation

Code and docs this feature touches.

## Scripts

- `scripts/install.sh` — new; the public installer, the only entry point.
- `scripts/release.sh` — read-only contract source for asset naming:
  archives `likha_<VERSION>_<OS>_<ARCH>.tar.gz` and manifest
  `likha_<VERSION>_checksums.txt`; version regex
  `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`;
  archives contain exactly `likha`, `README.md`, `LICENSE`; binary prints
  `Likha <VERSION>` for `--version`.
- `scripts/smoke-release.sh` — reference for OS/arch detection mapping
  (`Darwin`→`darwin`, `Linux`→`linux`; `x86_64`/`amd64`→`amd64`,
  `arm64`/`aarch64`→`arm64`) and for the archive-listing strictness the
  installer reuses.

## Docs

- `README.md` — new install section (one-liner, pinned form, overrides,
  verification and status caveat).
- `specs/README.md` — feature index row (status: planned).

## Specs

- `specs/v1-spec.md` — FR-13 (install using documentation alone),
  Installation acceptance row, release gate wording. This feature refines,
  never contradicts, that contract.
- `specs/setup-plan.md` — Feature 5 "shareable release" phase.

## Hosting assumption (decided)

Releases and the manifest are GitHub Release assets of `gem/likha`; the
installer itself is served to `curl` from
`https://raw.githubusercontent.com/gem/likha/main/scripts/install.sh`. The
owner/repo constants live in the installer header (`DEFAULT_BASE`,
`DEFAULT_API`) and must change in exactly one place if the repository moves.
No public web server is operated by the project.

## Known limitations (documented, not deferred work)

- Checksum verification proves the download matches the manifest, not that
  the manifest is authentic; authenticity rests on GitHub's release
  infrastructure and TLS.
- Latest-release resolution uses the GitHub REST API, which is rate-limited
  per source IP; the error message points at pinning a version instead.
