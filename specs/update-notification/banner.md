# Spec: In-TUI update banner ("Update available")

**Status:** proposed — publish-blocking (planned for v0.1.0's first published
release). Extends [design.md](design.md), which fixes the version-check
mechanism this banner depends on.

**Status note (2026-09-29):** pieces of this spec shipped with the status
line work (`specs/tui-redesign/` Phase C–D): the throttled, silent
availability check (`internal/update`: 24 h throttle, `LISA_UPDATE_API`
override, `LISA_UPDATE_CHECK=0` opt-out, dev builds never notify) exists,
and the status line renders an `update → vX` segment when `status_line.update`
is enabled (off by default). The in-TUI banner itself (§4 text, §5
placement) and the `lisa update` command remain open — per **D-01**, the
banner still ships only together with that command.

## 1. Purpose and goals

Lisa is distributed as GitHub Release archives installed once via
`scripts/install.sh`. Nothing tells a running user that a newer release
exists, so a published fix sits unseen until the user re-reads the README.
This feature closes that gap **before the first published release**, while
the user base is still predictable and the check's failure modes are cheap.

Goals:

1. A user running Lisa in the terminal learns, inside the TUI, that a newer
   release exists and exactly how to get it: the `lisa update` command.
2. The banner is truthful and bounded: it appears only for genuinely newer
   published releases, never installs anything by itself, and stays silent
   when the check fails.
3. The banner never interferes with conversation, streaming, approvals, or
   paging — it is informational, not interactive.

Non-goals (explicit exclusions): auto-update in the background, telemetry,
release-changelog surfacing, modal dialogs or pagers for the banner, a
keybinding to dismiss it, and any change to the versioning scheme or to
release tooling.

## 2. Hard dependency: `lisa update` must exist first

The banner advertises a command. Shipping the banner before the command
exists would print an instruction that fails, which is worse than silence.
Therefore:

- **D-01** `lisa update` is implemented together with, or before, this
  banner. A shipped banner without a working command is a release blocker.
- **D-02** Minimal `lisa update` contract the banner relies on:
  - Invoked as the first positional argument (`lisa update [flags]`). This
    requires the dispatcher change in run.go: today `flags.NArg() == 1`
    treats `update` as a **repository path** (run.go:124–133), so the
    reserved subcommand must be matched before path resolution, and
    `lisa update` must reject a second positional argument. All other
    current invocations (no args, `--sessions`, `--resume`, flags) keep
    their exact behavior; `update` is the only reserved word, and a
    repository legitimately named `update` is handled by `repository
    update` ambiguity rule below. (If that case matters, document
    `lisa ./update` as the path form.)
  - It downloads the latest GitHub Release archive for the host OS/arch via
    the same base/API URLs as `scripts/install.sh` (overridable through the
    same environment variables for testing), verifies the archive against
    the release's SHA-256 manifest, and swaps the binary atomically —
    never leaving a partially written binary at the install path.
  - It identifies its own install location via `os.Executable()` and its
    parent directory; a read-only or system-owned location produces a clear
    failure with the suggested manual install one-liner instead of a
    partial install.
  - A development build (`Version == "dev"`) refuses with "development
    build; update requires a released binary" and exits nonzero.
  - It prints the final versions before and after (`v0.1.0 → v0.2.0`) and
    never asks for provider configuration; it does not touch the state
    directory except its own throttle file (unchanged semantics).
- **D-03** The banner and `lisa update` share the version-check code
  (`internal/update`: `Latest`, `Newer`, throttle file). One endpoint, one
  throttle horizon, no second parser of the response.

The full `lisa update` specification (flags, rollback behavior, PATH advice
parity with install.sh, etc.) may be filled in later; D-02 is the subset the
banner cannot be truthful without.

## 3. Trigger logic (inherited from design.md, restated for the banner)

1. On TUI start, `ui.Init()` schedules one background check (`tea.Cmd`);
   the render is never delayed by it.
2. The check is throttled to at most once per 24 h per state directory,
   recorded in a `checked_ns` JSON file inside the private state directory
   (`LISA_STATE_DIR`, same 0700/0600 permission model as `sessions.sqlite`).
3. The check queries
   `https://api.github.com/repos/gem/lisa/releases/latest` with a 2-second
   timeout; network errors, non-200 responses, unparseable JSON, and
   unparseable tags are all silent no-ops.
4. The banner renders **only** when `Newer(Version, latest)` is true.
5. Opt-out: `LISA_UPDATE_CHECK=0` skips the check entirely (no request, no
   banner, throttle state untouched). There is no in-TUI toggle in this
   feature.

## 4. Exact banner text

Two width variants. The ellipsis-style arrow is `→` (U+2192), already
present on the terminal; the banner must never contain control sequences
beyond regular SGR styling from the theme styles.

- **Wide** (TUI width ≥ 56, logo header shown):
  ```
  Update available: Lisa v0.1.0 → v0.2.0 — run "lisa update" to install
  ```
- **Narrow** (TUI width < 56, compact header):
  ```
  Update ready: v0.1.0 → v0.2.0. Run "lisa update".
  ```

Rules:

- The literal command token `lisa update` appears verbatim in both variants
  and is never line-wrapped across two rows (the existing `fit`/`wrap`
  helpers must keep it intact; if the row is too narrow for the whole
  string, wrap before the command token, not inside it).
- Version strings come from `Version` (installed) and the fetched tag
  (latest); both render with the same `v` prefix.
- No timestamp, no changelog excerpt, no "dismiss with any key" line: the
  banner carries exactly one action.

## 5. Visual design guidelines

- **Roles only, no new styles:** reuse the existing `lisa/ui` theme roles.
  The banner renders with the **Warning** role (bold; palette-dependent
  color) — the role already reserved for user-attention markers (pending
  reviews) — while the command token `lisa update` renders inside the same
  row without extra emphasis (no underline; terminal URLs are not used).
  Never hardcode a RGB code; every palette (including light variants)
  already defines usable accent/dim colors, and `defaultTheme()` has safe
  fallbacks.
- **No animation:** no blinking, no spinner, no pulse. The banner is static
  from the moment the `updateAvailableMsg` message arrives until save /
  relaunch.
- **Iconography:** none. No new glyph entries in `ui.Glyphs`; the word
  "Update" does the marking work, which keeps both `AsciiGlyphs` and
  `NerdGlyphs` users and non-Nerd terminals identical here.
- **Boxing:** none. The banner is one plain text row inside the existing
  header region (after the logo or compact header, before `Repository:`) —
  no border characters, no background fill, no width-filling rule line. The
  row stays free of decoration so copying the command stays clean.
- **Required assets:** none. Terminal text only — no images, no embedded
  fonts, no icon files. This feature adds no files under `ui/`.

## 6. Placement

- **Fixed header region — not the paged content.** The conversation is
  paged (FR-14); a banner living in the page flow would scroll away after
  Home/pgUp and become a transient that a user mid-read never sees. The
  banner therefore lives in the header, appended after the existing header
  lines (logo + `Repository:` + provider + `Model:` in wide mode; the three
  compact rows otherwise) and before the horizontal rule.
- **Both header layouts** must gain the row: `m.header()` has two variants
  (wide logo path and compact path, tui.go:896–902); the banner attaches in
  the same relative position in both.
- **Responsiveness:** at width ≥ 56 the wide text is one row; below that
  the narrow variant takes precedence. If even the narrow text exceeds the
  width, it wraps into two rows with the command token intact (rule in §4);
  the body area shrinks accordingly via the existing height accounting so
  no bottom footer row can be pushed off-screen.
- **Height floors:** if the terminal is too short even for essential rows
  (the existing `m.width >= 56 && m.height >= 19` wide-layout gate already
  handles this), the banner drops to the narrow variant under the same
  threshold logic rather than being clipped.

## 7. Behavior

| Event | Behavior |
| --- | --- |
| Newer release found within throttle window | Banner appears once, stays until relaunch |
| Same version / older / unparseable | No banner, throttle timestamp still recorded |
| Check fails (offline, >2 s, non-200, bad JSON) | No banner; retry on next TUI start within window |
| LISA_UPDATE_CHECK=0 | No request, no banner |
| Development build (Version = "dev") | Never checks, never banners |
| User runs `lisa update` while a TUI session is open | Out of scope for the banner beyond one wording rule: because `lisa update` swaps the binary file but the running process keeps the old image, the banner may not claim the update applies immediately — it is expressed as "Run \"lisa update\"" without a promise of when it takes effect. |
| After a successful `lisa update` | No in-TUI change; the banner disappears on next launch because the new build reports the newer version |

**Persistence and dismissal:**

- No dismissal keystroke, no "seen" flag, no per-version memory: this is a
  rolling-release channel, and one information row is cheap enough to leave
  until the state resolves naturally. (This is a deliberate difference from
  modal flows like themes: there an explicit Cmd keeps a modal open; here a
  passive row must not eat the input budget.)
- The banner state in memory is one struct (`update *updateInfo`); the
  throttle file is the only persisted bit. No schema change: user_version
  stays 1.

## 8. Accessibility considerations

Terminal TUIs have no assistive tree; accessibility here means text-first,
linear, high-contrast, and non-transient.

- **Contrast:** the Warning role must remain readable on every predefined
  palette — verified per theme in themes tests (existing theme suite), not
  eyeballed. Failure is white-on-white or black-on-black anywhere = spec
  violation.
- **Color independence:** the banner's meaning never rides on color; the
  word "Update" and the bold weight carry it even in a monochrome terminal
  (`TERM=dumb`, basic 16-color, or screen-reader linear mode).
- **Screen readers / linear readers:** one short static row, always in the
  same position (bottom of header, above the rule). Never flashes, never
  appears mid-stream, never reorders existing rows — a user's reading
  position cannot be reset by the banner's arrival.
- **Keyboard:** zero new keys, zero key capture. The bubbletea key model
  and the pending-review flow are untouched; the banner cannot trap focus
  or intercept Y/N/Esc.
- **Copy-paste:** the command token is one contiguous ASCII string with
  straight quotes, no smart typography, full-width spaces, or zero-width
  characters, so terminals that let the user select and shell-paste can
  copy `lisa update` verbatim.

## 9. Localization notes

- v1 ships English-only, hardcoded, consistent with the rest of the TUI
  strings (no i18n framework exists and introducing one is out of scope).
- Constraints so later extraction is trivial:
  - Banner text lives in one place (a single template constant, not
    interpolated mid-sentence with a translated fragment).
  - Version tokens (`v0.1.0`, `v0.2.0`) and the command token
    (`lisa update`) are substitutions into the template, never merged into
    the words being translated.
  - Punctuation stays ASCII (`:` `-` `"`) inside the template except the
    `→` arrow; no date formats, no plural forms, no number formatting that
    varies by locale.
- Documentation (README install section, CHANGELOG) is English-only and
  adds the banner to the "Updates" section when the feature lands.

## 10. Verification checklist

- [ ] `Newer` matrix: newer/equal/older/prerelease/unparseable/`dev` behave
      per design.md; prerelease tags never notify over an equal plain
      release.
- [ ] Throttle: second check inside 24 h does not hit the network; after
      the window it does. The timestamp is recorded before the HTTP call,
      so repeated offline starts do not accumulate restart delays (the
      check runs off the render path regardless).
- [ ] Banner appears only when a strictly newer tagged release exists;
      fixture: loopback fake `releases/latest` server (same override point
      as `install.sh`'s `LISA_RELEASE_API`, mirrored as `LISA_UPDATE_API`
      for the Go client).
- [ ] Wide and compact layouts both render the banner; command token never
      splits across rows even at minimum test widths (below 56,
      including a 20-column case).
- [ ] Version-less conditions (equal/older/error/offline/`dev`/opt-out)
      render nothing.
- [ ] Pending-review Y/N/Esc keys behave identically with the banner
      visible; no test regression on the existing review flow.
- [ ] `--version` output stays exactly `Lisa <VERSION>` (install.sh asserts
      it); `--sessions` / `--resume` / non-TTY paths print no banner.
- [ ] `lisa update` works end-to-end against the loopback release server:
      checksum verified, binary atomically swapped, previous version prints
      before, new version verifies after; tampered checksum aborts without
      touching the installed binary.
- [ ] `lisa update` with `Version == "dev"` refuses cleanly.
- [ ] Repository named `update` resolves without ambiguity.
