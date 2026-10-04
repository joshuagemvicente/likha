# Checklist — Transcript redesign

Observable outcomes. Mirrors v1-spec FR-03 (distinguishable transcript
blocks) and FR-15 (themes, muted reasoning, legible plain text).

## Blocks (S1)

- [ ] No transcript row starts with a role label (`You:`, `Assistant:`,
      `Tool:`, `Reasoning:`, `Likha:`, `Error:`, `Agent:`) in new or resumed
      sessions.
- [ ] User prompts show `>` on a full-width band in every theme, including
      `default`; nothing else has a block background except code, diff
      lines, and the focused item.
- [ ] Assistant text starts with `⏺`; errors with `✗`; Likha notices with
      `ℹ`; wrapped lines hang under the text.
- [ ] `--ascii` / `LIKHA_ASCII=1` swaps every block glyph for its ASCII form.
- [ ] Under no-color profiles every glyph and outcome word remains.
- [ ] Each tool call is one item: `⏺ Name(args)` plus a `⎿` summary that
      states the outcome in words; the dot blinks while running, then turns
      green or red.
- [ ] grep/glob show no preview lines; read/run/fetch/search/MCP show at most
      3, then `… +N lines (ctrl+o to expand)`.
- [ ] Tab stops once per tool item; the focused item shows the selection
      tint and `›`; Enter/Ctrl+O opens the inspector with the same details as
      before.
- [ ] Prose wraps at word boundaries and never overflows at 40, 60, 80, or
      120 columns.
- [ ] The composer is a rounded box with `>` by default, `minimal` at
      ≤ 40 columns; previously saved composer styles still apply.
- [ ] Older saved sessions open and render without errors.

## Markdown (S2)

- [ ] Headings, bold, italic, strikethrough, inline code, lists, quotes,
      links, tables, rules, and images render per spec; raw `#`/`**`/
      backticks no longer show for well-formed markdown.
- [ ] Fenced code with a known language is highlighted in theme colors;
      unknown languages show a plain code panel.
- [ ] A half-streamed code fence never corrupts rows after it.
- [ ] Content cannot inject terminal escape sequences.

## Live surfaces (S3)

- [ ] Applied edits show the summary sentence and up to 10 numbered `+`/`-`
      lines on diff tints.
- [ ] An explore task appears as one `⏺ Task(…)` item with a live `⎿` line
      and a settle line; no `Agent:` lines appear.
- [ ] Reasoning shows a live 3-line tail, collapses to `✻ Thought for Ns`,
      and Enter on a focused marker expands/collapses it.
- [ ] Each finished turn ends with `✻ model · duration · N tools`, which
      reappears after resume.

## Gates

- [ ] `go build`, `go vet`, `go test ./...`, `go test -race ./...`, gofmt,
      `git diff --check` pass after each slice.
- [ ] Only the approved ~22 label assertions changed meaning; every other
      existing check is unchanged or stronger.
- [ ] User walkthrough recorded after each slice.
