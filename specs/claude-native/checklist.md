# Checklist: Native Claude client

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md). Live-probe items stay unchecked
until the user records them in [context.md](context.md#live-probe).

## Wire

- [ ] Requests for the `claude` provider go to `POST /v1/messages` with
      `x-api-key` and `anthropic-version: 2023-06-01`, streamed, and carry no
      `Authorization` header.
- [ ] Every other provider's request bodies are byte-identical to a build
      without this feature.
- [ ] A custom endpoint other than `https://api.anthropic.com/v1` keeps the
      chat-completions wire.

## Conversation

- [ ] The system prompt, repository instructions and a compaction summary
      at the start of the conversation arrive as the top-level system prompt.
- [ ] A later developer note arrives as user-turn text at its position,
      after any tool results in that turn.
- [ ] Parallel tool results arrive together in one user turn, results
      before text.
- [ ] No request ever carries `temperature`, `top_p`, `top_k`,
      `tool_choice` or an assistant prefill.
- [ ] An invalid tool name fails before sending and names the tool.

## Reasoning and replay

- [ ] On an adaptive-thinking model, summarized reasoning streams into the
      muted reasoning view during the turn.
- [ ] On Haiku 4.5 or an unclassified model, the request has no thinking
      field and succeeds.
- [ ] The second request of a tool loop sends the first response's content
      blocks back unchanged, thinking and signatures included.
- [ ] After a model switch, earlier turns go without thinking, and the
      request succeeds.
- [ ] A session with Claude replay state resumes and continues.
- [ ] Compaction and session naming send tool history as text and no
      thinking.
- [ ] Display reasoning text never appears in a request body.
- [ ] A response cut off inside a thinking block is sent back later
      without thinking, and the request succeeds.
- [ ] After a plan-mode toggle or an AGENTS.md change between prompts, the
      next prompt in the same session succeeds (mismatched thinking is
      dropped, not fatal).

## Effort

- [ ] While the row is unprobed, no `output_config` is sent and the switch
      entry says effort is not yet verified for Anthropic Claude.
- [ ] After the FR-40 probe, a chosen level is sent as
      `output_config.effort` and Default sends none.

## Usage and caching

- [ ] Requests with tools carry automatic `cache_control`; requests without
      tools do not.
- [ ] A second request in a tool loop reports cache reads, and the prompt
      count includes them.
- [ ] Spend for a Claude request uses cache read and write prices.

## Models and errors

- [ ] Setup, `/providers` and `/models` list Claude models from
      `GET /v1/models`, with display names and context windows.
- [ ] A bad key reports rejected credentials on the model list and on a
      request.
- [ ] A rejected request carrying effort shows the effort hint; one carrying
      replayed thinking shows the `/compact` hint.
- [ ] Overload, rate-limit and server errors, in HTTP or in the stream, are
      visible errors that FR-36 treats as transient; a spend-cap 429 is not.
- [ ] A stream that ends before `message_stop` is an error, not an answer.
- [ ] A refusal shows `Claude declined the request (category: …).` and
      Anthropic's explanation; its partial text is never sent back.
- [ ] Claude errors include Anthropic's request ID when it was returned.

## Release

- [ ] `go test ./...` and `go vet ./...` pass from the project root.
- [ ] README, `docs/providers.md` and v1-spec describe `claude` as
      pending-probe.
- [ ] Live admission probe recorded by the user (model list, streamed text
      and reasoning, tool round trip with replay, cache read on the second
      request, cancellation, bad key, prompt after a plan-mode toggle).
- [ ] CHANGELOG entry states what was verified.
