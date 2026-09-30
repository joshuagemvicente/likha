# Design: Rolling-release update notification

**Status:** proposed — current version v0.1.0

## Context

Lisa is a Go CLI with a Bubble Tea TUI (`internal/app`). The release pipeline
(`scripts/release.sh`) stamps the tag into `internal/app.Version` via
`-ldflags "-X lisa/internal/app.Version=$version"` and publishes four archives
plus a checksum manifest as GitHub Release assets of `gem/lisa`. The installer
(`scripts/install.sh`) already resolves `releases/latest` via
`https://api.github.com/repos/gem/lisa/releases/latest`, so the newest version
is discoverable with no extra infrastructure. Versioning (semver tags, no
scheme change) and the update path (re-running `install.sh`) stay unchanged.

## Rollout model

Releases stay as-is; "rolling" here means users are told about each new tag
rather than mass-redeployed. The check is read-only: Lisa never auto-updates,
writes nothing except a 24-hour throttle timestamp, and stays silent offline.

## Step-by-step design

1. **New package `internal/update`** with two functions:

   ```go
   // Latest returns the newest published release tag (e.g. "v0.2.0").
   func Latest(ctx context.Context) (string, error) {
       req, err := http.NewRequestWithContext(ctx, http.MethodGet,
           "https://api.github.com/repos/gem/lisa/releases/latest", nil)
       if err != nil { return "", err }
       req.Header.Set("Accept", "application/vnd.github+json")
       res, err := http.DefaultClient.Do(req)
       if err != nil { return "", err }
       defer res.Body.Close()
       if res.StatusCode != http.StatusOK { return "", fmt.Errorf("github api: %s", res.Status) }
       var payload struct{ TagName string `json:"tag_name"` }
       if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&payload); err != nil { return "", err }
       if !validTag(payload.TagName) { return "", fmt.Errorf("unexpected tag %q", payload.TagName) }
       return payload.TagName, nil
   }

   // Newer reports whether latest is a higher version than current.
   // "dev" builds and unparseable versions never notify.
   func Newer(current, latest string) bool {
       c, ok := parse(current); if !ok { return false }
       l, ok := parse(latest);  if !ok { return false }
       return l.compare(c) > 0
   }
   ```

   `parse` accepts only the release scheme (`v0.1.0`, optional `-prerelease`),
   mirroring the regex in `scripts/release.sh`; prereleases compare after the
   plain version per semver. No external semver dependency is added — the
   scheme is exactly three numbers, so ~15 lines of integer compare suffice.

2. **Throttle in the state dir.** Lisa already stores everything under the
   `LISA_STATE_DIR` (default: OS config dir's `lisa` child). Add one opaque
   JSON file, same permissions model as `sessions.sqlite` (0700 dir, 0600
   file):

   ```go
   // internal/update/throttle.go
   type state struct{ CheckedNS int64 `json:"checked_ns"` }
   const interval = 24 * time.Hour

   func shouldCheck(dir string) (bool, func(int64) error) // read/atomically-write checked_ns
   ```

   Check-at-most-once-per-day-per-machine; also the natural kill switch
   (`LISA_UPDATE_CHECK=0` skips the check entirely).

3. **Non-blocking check at TUI start.** `ui.Init()` currently returns `nil`
   (`internal/app/tui.go:118`). Bubble Tea runs `tea.Cmd`s on a goroutine, so
   a 2-second-timeout check never delays render:

   ```go
   func (m *ui) Init() tea.Cmd {
       return checkUpdateCmd(m.stateDir)
   }

   type updateAvailableMsg struct{ version, url string }

   func checkUpdateCmd(stateDir string) tea.Cmd {
       return func() tea.Msg {
           ok, mark, err := update.Prepared(stateDir) // throttle read/write
           if err != nil || !ok { return nil }
           ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
           defer cancel()
           latest, err := update.Latest(ctx)
           if err != nil || !update.Newer(app.Version, latest) { return nil }
           mark(time.Now().UnixNano())
           return updateAvailableMsg{version: latest, url: "https://github.com/gem/lisa/releases/tag/" + latest}
       }
   }
   ```

   Failures return `nil` (no-op message) — a missing update is a non-event.

4. **Display: one accent-colored line under the Lisa header.** The TUI already
   renders `Repository:` / provider / `Model:` lines via `m.header()`
   (`internal/app/tui.go:896`) and the statuses in the footer via
   `m.theme.Help.Render` (`tui.go:1035`). Add a field `update *updateInfo` to
   `ui`; when `updateAvailableMsg` arrives, set it and let `rebuild()`/`View()`
   append one line:

   ```
   ⬡ Lisa v0.1.0 → v0.2.0 is available: run
     curl -fsSL https://raw.githubusercontent.com/gem/lisa/main/scripts/install.sh | sh
   ```

   Rendered with the theme's accent color (same style family as `Title`), at
   most one line, always visible in both wide-logo and compact layouts
   (`header()` has two variants — extend both). It is dismissed by upgrading;
   no keystroke/NAG loop, no pager, no modal — unlike the themes modal or
   pending-review flow, it must never steal input.

## UI presentation summary

- **Where:** directly under the four-line Lisa logo block, above
  `Repository:` — inside the terminal, before the conversation.
- **What:** version transition + pinned-version-free upgrade command.
- **What it does NOT do:** does not auto-install, does not interrupt typing,
  does not appear in JSON/non-TTY modes, does not alter `--version` output
  (`Lisa <VERSION>` must keep matching the tag exactly — the installer
  asserts it).

## Verification outline

- Unit: `Newer` across equal/older/newer/prerelease/unparseable inputs;
  throttle read/write respects the 24-hour window.
- Integration: point `LISA_RELEASE_API`'s equivalent at a loopback server
  (mirror the `install.sh` override, e.g. `LISA_UPDATE_API`) serving a fake
  `releases/latest`; assert the one-line notice renders and no notice renders
  when the version equals current.
- Offline/network-failure: check must return before 2s and render nothing.
