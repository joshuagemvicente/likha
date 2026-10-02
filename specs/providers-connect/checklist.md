# Checklist: Providers as connection manager

- [ ] Enter on any /providers row never switches the live provider/model.
- [ ] Unconfigured row opens the key modal; success stores the key and
      stays in the dialog.
- [ ] Configured row shows an auth-state view naming the key source;
      replacing re-checks; failure keeps the old key.
- [ ] chatgpt row is state-only; not signed in points at setup/--device-login.
- [ ] /providers <n-or-name> [key] opens auth and never switches.
- [ ] /models cross-provider switching unchanged; `go test ./...` green.
