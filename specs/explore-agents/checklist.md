# Checklist: nested explore

- [ ] Same-model fresh contexts use bounded briefs and root instructions.
- [ ] Depth-1 explore can spawn explore; depth-2 cannot spawn.
- [ ] Forged edit/shell/MCP/web/interaction calls have no effect.
- [ ] Four executing children is a run-wide cap, not a per-parent allowance.
- [ ] Waiting parents release slots; nested work and resumed parents make progress.
- [ ] Total spawn, queue-inclusive time, and child-round caps have visible outcomes.
- [ ] Partial/failure/cancelled results retain call order and exactly-once identity.
- [ ] Branch/run cancellation stops pending and subsequent descendant actions.
- [ ] Resume marks interrupted nodes without replay; usage remains attributed.
- [ ] Existing checks and unverified scheduler/provider cases are recorded.
