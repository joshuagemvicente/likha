# Checklist: Fast `/models` open (cache + progressive render)

Status mirrors [spec.md](spec.md) acceptance criteria; unchecked until
observable in the real TUI.

- [ ] A repeat `/models` open within TTL renders the cached list
      immediately (no loading state) with the cursor on the live pair.
- [ ] Background refresh replaces sections in place in final order; the
      cursor is never yanked after the user navigates.
- [ ] A cold open shows the first-arriving section before stragglers
      finish; Enter applies with ≥1 section present and is a no-op with
      zero.
- [ ] A slow/failed provider is named in the muted note; cached rows
      survive a failed refresh; total failure with no cache shows the
      dialog error.
- [ ] A newly configured provider appears and a removed one disappears on
      the next open; a restart opens cold.
- [ ] `chatgpt` contributes instantly with no network; apply paths and
      filtering are unchanged.
- [ ] `go test ./...` from the project root passes, including the feature's
      tests.
