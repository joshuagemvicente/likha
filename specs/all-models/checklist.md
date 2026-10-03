# Checklist: All-provider models in `/models`

Status mirrors [spec.md](spec.md) acceptance criteria; unchecked until
observable in the real TUI.

- [ ] Bare `/models` groups every configured provider's models under its
      own non-selectable display-name header, active provider's section
      first, model ids only on the rows, with no per-row provider column.
- [ ] Cursor starts on the live model row and never lands on a header;
      navigation skips headers; typing filters over model and provider
      text with header visibility following its rows; the loading guard and
      Esc-discard behave as before.
- [ ] Enter on an active-provider row switches live plus stored, exactly as
      today.
- [ ] Enter on another provider's row moves the session to that
      provider+model, stores the pair, names both in one entry; the next
      turn uses the new pair.
- [ ] A failed provider contributes no rows but is named in a muted note
      when other rows list; total failure shows the dialog error; Esc
      applies nothing.
- [ ] Unconfigured providers are absent; `chatgpt` contributes its curated
      list when signed in; no configured provider keeps today's refusal
      with zero network traffic.
- [ ] A failed cross-provider activation closes the dialog with a visible
      error and leaves the previous provider/model live.
- [ ] `go test ./...` from the project root passes, including the feature's
      tests.
