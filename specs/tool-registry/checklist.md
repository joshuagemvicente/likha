# Checklist: tool registry

- [ ] `/tools` identifies available/unavailable tools and permission reasons.
- [ ] Invalid/crafted disallowed calls have no effect and one accurate result.
- [ ] Built-ins, MCP, web, and task calls use the common policy path when installed.
- [ ] Original tool IDs/result order survive concurrency and cancellation.
- [ ] Approval/question interactions serialize and reject late answers.
- [ ] MCP qualified identities cannot silently shadow another tool.
- [ ] Legacy history displays/resumes without replay or permission inference.
- [x] Existing checks and unexercised acceptance cases are recorded.

See [Phase 1 evidence](../tooling-platform/implementation.md); unchecked items
still need their specific acceptance walkthrough.
