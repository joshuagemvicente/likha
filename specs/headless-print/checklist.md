# Checklist: Headless print mode

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Starting

- [x] `likha -p "…"` runs with stdin and stdout redirected, no TUI, no
      alternate screen.
- [x] `-p -` reads the prompt from stdin; more than 256 KiB is a usage error.
- [x] No configured provider exits 2 without any network request; ChatGPT
      needing a browser login exits 2 and names `--login`.
- [x] Empty prompt, a prompt starting with `/`, and `-p` with `--sessions`,
      `--login` or `--probe-effort` exit 2 before any request; `--json` or
      `--max-time` without `-p` exit 2.
- [x] A failed connection check exits 1 and sends no prompt.
- [x] Configured MCP servers are not started; stderr says so once.

## Approvals never happen

- [x] An edit proposal writes nothing and returns a refused result naming
      headless mode; the model is told to describe the change instead.
- [x] Ask, always-ask and untrusted-verification commands are refused
      without running; read-only commands and trusted checks run unprompted
      with their auto-approved label.
- [x] `ask_user`, web search, web fetch and MCP tools are not offered; a
      forged call is refused.
- [x] No refusal is reported as success or as a user rejection.
- [x] No flag exists that approves an action headlessly.

## Output

- [x] Text mode: stdout holds only the final answer; stderr holds plain
      progress lines, each refusal, and a summary with the session ID.
- [x] `--json`: every stdout line is one JSON object; the first is `start`
      (`schema: 1`); exactly one `result` is last, also after failure,
      cancellation and time limit; stderr is empty after startup.
- [x] Neither mode prints an API key, OAuth token or authorization header,
      even when the provider echoes one. (Tested with an echoed API key,
      including a `--api-key` a custom endpoint never sends. The ChatGPT
      tokens loaded at start are on the redaction list; a token rotated
      mid-run is caught only in its `Bearer …` form.)

## Ending

- [x] Exit codes: 0 completed (also with refusals), 1 failed or time limit,
      2 usage/config, 130 signal.
- [x] SIGINT cancels like Esc, saves the session and exits 130.
- [x] `--max-time` cancels the run and reports `time_limit`.
- [x] The session is listed by `--sessions`, opens with `--resume ID` in the
      TUI, and `-p … --resume ID` continues it.

## Verification record

Checked 2026-10-07 against local `httptest` providers only (unit tests in
`internal/agent`, `internal/headless`, `internal/app`, a TUI resume test,
and `tests/integration/headless_test.go` running the built binary). No
real provider has been called headlessly.

- [x] Automated: unit, app and `tests/integration` suites green.
- [ ] Live probe on one real provider (user-run), text and `--json`.
