# Checklist: Chunked message content

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Reading replies

- [ ] A reply whose content streams as Mistral thinking and text chunks
      completes. Thinking shows in the muted reasoning style, and the
      answer shows as assistant text, in the order they arrived.
- [ ] A reply that switches from chunk lists to plain strings partway
      through reads as one answer.
- [ ] Thinking spread across several chunks is saved as one reasoning
      text.
- [ ] Image, document, audio, file, reference and unknown chunks are
      skipped without an error or notice, and the rest of the reply still
      shows.
- [ ] A reply made only of chunks Likha cannot show ends with a visible
      error naming the chunk type, and the turn is not shown as successful.
- [ ] Broken content (a number, a boolean, an object, or a malformed chunk)
      ends the turn with the existing visible malformed-stream error, and
      the turn is not shown as successful.
- [ ] Replies with plain string content look and behave exactly as before.
- [ ] Tool calls that follow chunked thinking run as usual.
- [ ] Main turns, explore children, profile tasks, compaction and session
      naming all read chunked replies. Compaction summaries and session
      names never contain thinking.

## Saved sessions

- [ ] A resumed session shows the chunked reply's reasoning muted and its
      answer as assistant text.
- [ ] Requests are unchanged. Mistral thinking is not sent back until
      reasoning-replay lands.
- [ ] After reasoning-replay lands (M2), a later request to the same
      Mistral endpoint and model carries exactly one leading thinking chunk
      before the answer text. Requests to any other endpoint or model carry
      none.

## Effort matrix

- [ ] The model-effort matrix and `docs/providers.md` list `mistral` as
      `pending-probe`, not `blocked`. No Mistral effort is sent until M2
      has landed and its probe passes.

## Honesty and verification

- [ ] `go test ./...` covers every acceptance criterion with httptest
      streams. No skipped or always-passing test backs a claim.
- [ ] The `CHANGELOG.md` entry states automated checks only, until the
      user reports a live Mistral probe.
- [ ] The user-run live probe (Mistral reasoning model at `high` with
      tools, two tool rounds and a follow-up) is recorded in
      [research.md](research.md) before the status reaches verified
      (release).
