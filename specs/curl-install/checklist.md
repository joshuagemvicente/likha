# Checklist: curl-based public installation

Observable outcomes, mirrors [spec.md](spec.md). Status words per
[specs/README.md](../README.md) rules.

- [ ] A developer who has never cloned Lisa can install on macOS or Linux
      using only the README one-liner and the running shell.
- [ ] Downloaded archive is checksum-verified against the release manifest
      before anything is executed or put on `PATH`.
- [ ] No root (`sudo`) is required or used anywhere in the install path.
- [ ] A tampered/truncated/non-listed archive refuses to install with a clear
      error, and no files change on the user's machine.
- [ ] The installed binary's `--version` output matches the release tag that
      was selected (pinned or latest).
- [ ] Failure of any step (lookup, download, checksum, extraction, install) is
      reported with an actionable message, and the user's existing
      installation, if any, stays intact.
- [ ] Temporary files disappear from the system on both success and failure.
- [ ] Download base is HTTPS, except explicit loopback-only tests.
- [ ] Status of this feature is only ever: planned, in progress,
      implemented (local), or verified (release); claiming verified (release)
      requires the published-binary walkthrough from
      [v1-spec.md](../v1-spec.md).
