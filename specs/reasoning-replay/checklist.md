# Checklist: Reasoning replay

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Tool loops

- [ ] A DeepSeek tool loop runs past its first tool round without a 400.
- [ ] A DeepSeek session keeps working after switching between
      `deepseek-flash` and `deepseek-v4-pro`.
- [ ] A Gemini 3 tool loop runs past its first tool round without a 400,
      including a round with parallel tool calls.
- [ ] A Gemini 2.5 tool loop works as it did before this feature.
- [ ] OpenRouter tool loops work exactly as before while its row ships
      disabled. With the form enabled in a local probe build, an Anthropic
      and a Gemini model complete a tool loop with `reasoning_details`
      returned on every later request.

## What is sent

- [ ] Display reasoning appears in no request body, on any provider.
- [ ] DeepSeek reasoning text goes only to the DeepSeek endpoint. Signed
      state goes only to the endpoint and model that issued it.
- [ ] Requests to providers without a replay requirement are byte-identical
      to requests before this feature.
- [ ] After a switch to another provider or a signing model, the old
      model's state is not sent, and it is sent again after switching back.
- [ ] A Gemini current-turn tool call that the current model did not issue
      carries Google's documented skip value; a call it issued is sent as
      received.

## Persistence and history

- [ ] Replay state survives save and resume.
- [ ] Sessions saved before this feature resume and continue.
- [ ] `/compact` output carries no replay state.
- [ ] Explore children and profile tasks keep their own state across their
      own rounds, and never share it with the parent.
- [ ] Replay state is never shown in the transcript or agent detail views.

## Failures

- [ ] A 400 on a request whose earlier turns lacked required state, or that
      carried replay state, shows the `/compact` hint once and is not
      retried. Any other 400 shows no hint.
- [ ] A request stopped by Steer now on DeepSeek does not make the next
      request fail.

## Evidence

- [ ] Each enabled provider has a recorded live probe in
      [context.md](context.md#provider-matrix) and `docs/providers.md`. No
      row is called verified without one.
- [ ] `go test ./...` passes with the feature's tests, and no skipped,
      always-passing or mock-only test is used as evidence.
