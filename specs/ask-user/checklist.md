# Checklist: ask user

- [ ] Complete question, bounded choices, free text, and Skip are reachable.
- [ ] Exactly one answer/refusal/cancel result matches the original call ID.
- [ ] Draft/queue survives; answer input cannot become steering or approval.
- [ ] Child calls refuse and main interactions serialize.
- [ ] Cancellation/late submission/resume cannot replay or infer answers.
- [ ] Narrow layout, existing checks, and unverified interaction cases are recorded.

## Amendment 2026-10-05

- [x] A questionnaire of up to four questions shows progress, a Recommended
      tag with initial focus, and option descriptions.
- [x] Earlier answers can be changed before submission; one result carries
      every answer against the original call ID.
- [x] Per-question skip is reported; all-skipped returns a refusal.
- [x] Malformed questionnaires refuse before the dialog opens.
- [ ] The asking rules are in the main instructions; a live probe shows an
      ambiguous request triggers questions and a clear one does not.
