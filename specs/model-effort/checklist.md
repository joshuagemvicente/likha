# Checklist: Model effort

Observable outcomes, mirroring [spec.md](spec.md). Status words follow the
rules in [specs/README.md](../README.md).

## Choosing a level

- [ ] Picking a model with published levels in `/models`, on a provider that
      can carry effort, opens an effort step listing Default and exactly the
      model's levels, in its published order.
- [ ] The step's cursor starts on the level last used with that provider and
      model, else on Default.
- [ ] Enter in the step switches the model and sets the level together, and
      the switch entry names both.
- [ ] Esc in the step returns to the model list and changes nothing.
- [ ] A model without levels switches on the first Enter, as before.
- [ ] A reasoning model on a provider that cannot carry effort switches on
      the first Enter, and the switch entry says why effort stays at the
      provider default.
- [ ] First-run setup, ChatGPT sign-in and `--model` never prompt for effort.

## `/effort`

- [ ] `/effort` opens the step for the current model without switching it.
- [ ] `/effort <level>` sets a published level or `default`.
- [ ] Unknown levels, extra words and unpublished levels are visible errors
      that name the accepted values and keep the draft.
- [ ] `/effort` explains why when the model or provider has no effort.
- [ ] `/effort` and model switching refuse during an active run or pending
      review.

## Requests

- [ ] At Default, request bodies are byte-identical to bodies sent before
      this feature, on every wire path (chat completions, Responses and
      Anthropic Messages).
- [ ] At a level, each main-turn request carries exactly one effort field,
      in the provider's recorded wire form, observed by a test server.
- [ ] Explore children and profile tasks carry the session's level;
      compaction and session naming carry none.
- [ ] A provider rejecting a request with effort shows a visible error with
      the effort hint, and the request is not retried.

## Remembering and display

- [ ] The level survives relaunch through `config.json`, per provider and
      model. Choosing Default removes the stored entry.
- [ ] A stored level the model no longer publishes gives Default and one
      notice.
- [ ] The status bar shows `model · level` when the level is not Default,
      readable without color or Nerd Font glyphs, and drops the level first
      on narrow terminals.

## Evidence

- [ ] Every provider marked as carrying effort has a recorded live probe in
      [context.md](context.md#provider-support-matrix) and
      `docs/providers.md`. No row claims support without one.
- [ ] `go test ./...` passes with the feature's tests, and no skipped,
      always-passing or mock-only test is used as evidence.
