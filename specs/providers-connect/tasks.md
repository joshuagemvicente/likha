# Tasks: Providers as connection manager

1. **Auth state + route.** Add an auth-state view for configured rows
   (provider, source: state-dir or env, replace hint) routed from
   `confirmDialog`'s dialogProviders branch; remove the
   verify-then-activate path from this surface (delete or repurpose
   `startProviderSwitch`'s dialog use and `applyProviderDirect`'s
   direct activation). Verify: Enter on every kind of row swaps nothing.
2. **Key modal success = store + stay.** Re-point `handleKeyCheckMsg`:
   on a passing check, store/replace the key and remain in the dialog
   (row refreshes its ✔); a failing check keeps the old key. Wire
   key-replace from the auth-state view. Verify: modal tests drive
   store-stays-open; previous provider stays live throughout.
3. **Direct form.** `handleProvidersCommand` with a target opens its
   auth surface; a trailing key argument stores after check. No switch.
   Verify: typed-form tests.
4. **Docs.** commandHelp wording, README, CHANGELOG, specs/README row.
