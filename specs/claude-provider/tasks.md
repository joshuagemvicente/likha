# Tasks: Claude (Anthropic) predefined provider

**Status:** spec'd, not implemented.

1. **Provider row:** append the `claude` row to `var Providers` in
   `internal/model/provider.go` after `cerebras`, with empty `DefaultModel`
   and pending-probe status. Resolution, client, setup, `/providers`,
   `/models`, and `--help` consumption all come from the table iteration.
2. **Model-list route probe (first gate):** with a valid key, confirm
   `GET https://api.anthropic.com/v1/models` accepts Bearer auth and returns
   the OpenAI `data[].id` shape the shared decoder reads. If it does not,
   stop and specify the minimal client remedy before shipping the row.
3. **Full admission probe:** streamed text, structured tool call and
   tool-result roundtrip, mid-stream cancellation, revoked-key error —
   recorded in `specs/predefined-providers/context.md`.
4. **Default model:** pin `DefaultModel` only from the passing probe; until
   then `--model`/`LIKHA_MODEL` is required.
5. **Documentation:** README provider table, v1-spec §3 enumeration,
   CHANGELOG Unreleased entry, and the candidates matrix in
   `specs/additional-providers/provider-candidates.md` — all pending-probe,
   never accepted before step 3 is recorded.

Verify with `go test ./...` from the project root; the existing
`TestResolveProviderSelectsEndpointsAndKeys` loop covers the new row
automatically. Live compatibility is not established by mock tests or
documentation alone.
