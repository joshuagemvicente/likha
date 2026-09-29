# Tasks: First-run provider setup in the TUI

**Status:** in progress.

1. **Stored provider config.** `<stateDir>/config.json` holds
   `{"provider":…,"model":…}` (mode 0600). `loadStoredConfig`/`saveStoredConfig`
   in `internal/app/config.go`; corrupt file fails loudly. Precedence:
   flag > `LISA_*` env > stored config.
2. **Setup trigger in run.go.** No provider from any source and no endpoint →
   interactive TUI starts in setup mode; non-interactive invocations exit 2
   with a clear error. `--sessions`/`--resume` unaffected.
3. **TUI setup flow.** `internal/app/tui.go`: provider picker → masked key
   entry (hosted only) → async `model.ListModels` check → model picker
   (windowed list, arrow keys, single-model auto-select) → store key +
   config → main conversation view. Esc back one stage; Ctrl+C/D quit.
4. **Docs.** FR-02 wording, README quick-start (now "run Lisa, pick a
   provider, paste your key"), CHANGELOG.
5. **Live probe via setup flow** (user-driven): launch, pick `opencode-go`,
   paste key, observe check + model list + a streamed turn. Evidence recorded
   in `specs/predefined-providers/context.md`.
