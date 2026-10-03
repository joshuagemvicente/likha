# Checklist: First-run provider setup in the TUI

- [ ] Bare `likha` (no flags, no env, no stored config) opens setup in an interactive terminal.
- [ ] Provider picker lists all predefined providers with base URLs.
- [ ] Every provider prompts for a masked API key before the connection check.
- [ ] A wrong/revoked key shows the classified connection error and allows retry.
- [ ] The model picker shows the provider's reported models; a single model auto-selects.
- [ ] Completing setup stores provider + model (`config.json`) and key (`providers.json`), both 0600.
- [ ] The next launch skips setup and starts the conversation with the stored provider/model.
- [ ] Explicit flags/environment skip setup and override stored config.
- [ ] Non-interactive invocations without configuration exit 2 with a clear message.
- [ ] `--sessions`/`--resume` work without stored config and never trigger setup.
- [ ] Session continuity is unaffected; existing sessions resume after setup.
