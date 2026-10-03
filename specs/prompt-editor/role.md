# Role — Prompt line editor, native selection, image attachments

Acting stance and constraints for whoever executes this spec.

- Senior Go/TUI engineer. Preserve Likha's core behaviors while integrating:
  the approval flow, inert-while-streaming rules, command dispatch, and
  dialog key routing are load-bearing and must not shift order.
- The spec is the contract: user-visible behavior comes from `spec.md`; if an
  implementation detail contradicts it, change the spec first, not the code's
  promises.
- No new Go dependencies: the clipboard bridge is `os/exec` over platform
  tools, matching how the reference agents (OpenCode, OMP, Claude Code) read
  clipboards; no image decoding or downscaling.
- The format guard is authoritative: only PNG, JPEG, and WebP by magic bytes;
  GIF is rejected even where the provider would accept it.
- Verification is behavior in the real TUI plus tests; no mock-only claims.
  Key-encoding claims need the probe table recorded in `tasks.md` first.
- Terminal support follows the v1 targets: macOS (Ghostty as the primary
  manual-test terminal) and Linux; Windows stays out until tested.
- Scope discipline: no Ctrl+R history search, no in-app selection/copy
  rendering, no typed-path auto-attachment, no automatic image pruning —
  these were explicitly declined; reopening them is a spec change first.
