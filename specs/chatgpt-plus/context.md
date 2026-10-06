# Context: official Sign in with ChatGPT migration

**Status:** in progress (2026-10-05). This page maps the migration seams;
it does not claim that concurrent implementation has passed verification.

## Contract and decisions

- [v1-spec.md FR-02](../v1-spec.md) is the amended product contract.
- [spec.md](spec.md) describes user-visible behavior; [tasks.md](tasks.md)
  contains implementation requirements and verification gates.
- [research.md](research.md) supersedes the 2026-09-29 OpenCode/OMP Codex notes
  with official OpenAI sources checked on 2026-10-05. Those earlier fixture
  outcomes and third-party constants are not SIWC evidence.
- Likha fits the documented free, local open-source app category. It implements
  the public protocol in Go without copying DevKit code, another tool's client
  identity, or a Codex runtime. Eligibility and OpenAI's terms still apply; this
  classification is not a live entitlement result or a legal approval.
- ChatGPT plan use requires an authorized grant and eligible entitlement.
  Keep the separately billed OpenAI API-key provider distinct. No automatic
  API-key fallback, account rotation, usage-limit bypass, or inferred exact-zero
  cost. Optional credits follow explicit user opt-in in ChatGPT settings.

## Code seams

| Path | Migration responsibility |
| --- | --- |
| `internal/model/provider.go` | ChatGPT OAuth provider identity and public API/resource constants; remove fixed Codex client, port, device route, and curated eligibility assumptions. |
| `internal/model/oauth.go` | Dynamic registration/reconnect, ephemeral `127.0.0.1` callback with stable path, state/nonce/PKCE, public token exchange/refresh, OIDC signature/claims verification, scope gates, and revocation discovery. |
| `internal/providers/keyfile.go` | Protected API-key and SIWC records, account/workspace identity and active choice, stable host ID, atomic persistence and cross-process refresh coordination, safe legacy-token handling. |
| `internal/providers/provider.go`, `config.go` | Provider/account resolution and persisted user choices without changing API-key precedence or exposing credentials to the repository. |
| `internal/model/client.go` | Authenticated public model discovery, account-specific token refresh/persistence seam, bearer Responses requests, useful error classification and cancellation. |
| `internal/model/codex.go` | Existing Responses mapping/parser; migrate its wire behavior to official SIWC. The filename is historical, not permission to retain private backend behavior. |
| `internal/tui/setup.go`, `setup_view.go` | Automatic browser start, waiting/error/cancel states, launcher-failure URL, validated model discovery, and first plan-use confirmation. |
| `internal/tui/providers.go`, `models_all.go`, `dialog.go` | Unconnected-row login and connected account management, distinct/active labels, reconnect/add/sign-out, current account-specific models and plan-use/usage guidance. |
| `internal/app/run.go` | Browser `--login`, unsupported/deprecated `--device-login` response, refresh/store wiring, and helpful missing-login errors. |
| `internal/model/pricing.go`, TUI cost summaries | Parent-owned change: use reported cost or unknown; provider identity alone cannot establish `$0.00`. |
| `internal/agent`, `internal/explore`, `internal/mcp` | Preserve the local agent/tool loop, approvals, cancellation, child budgets, and network consent. Check background model work for express SIWC consent. |

Follow `AGENTS.md` import boundaries: `model` must not import `providers`,
`agent` must not import providers/TUI packages, and only `cmd/likha` imports
`app`. Supply credential persistence/locking through the existing appropriate
seam rather than introducing a package cycle.

## Registration, host, and credential invariants

- One persisted host ID identifies this local runtime before the first attempt.
  Restarts, account switches, and sign-out preserve it. It is opaque and is not
  proof of identity or possession of a token.
- Initial authorization uses `dynamic_agent_client` and `agent_name_hint=Likha`.
  The callback's issued ID binds the registration to its user/workspace.
  Reconnect uses that issued ID. Issued ID plus verified ID-token `sub` separates
  registrations; email alone and legacy `chatgpt_account_id` parsing do not.
- Each attempt owns its exact callback URI, state, nonce, and S256 verifier.
  The port may vary; scheme `http`, host `127.0.0.1`, and `/auth/callback` remain
  stable. Only a validated matching result can replace the selected credentials.
- Tokens, granted scopes, identity, and expiry/refresh timing remain associated
  with the same registration. Rotating refresh tokens require a store lock,
  re-read, and atomic replacement across processes, not just a goroutine mutex.
- A retained ID token may serve as a returning-login hint, even after its
  authentication expiry, but it cannot replace verification of the newly issued
  ID token. Sign-out clears access, refresh, and ID tokens while retaining the
  registration mapping and host ID.
- State directory `0700`, protected files `0600`, atomic writes, no token/log
  leakage. Legacy Codex tokens require fresh auth rather than a guessed migration.

## Related guides and specs

- [README](../../README.md), [provider guide](../../docs/providers.md),
  [usage guide](../../docs/usage.md), and
  [first-run setup](../first-run-setup/spec.md) must describe the browser-first
  exception to API-key setup.
- [Predefined providers](../predefined-providers/spec.md) retains its full live
  probe gate; SIWC docs or fixture success cannot admit this provider as accepted.
- Older [providers-connect](../providers-connect/spec.md),
  [all-models](../all-models/spec.md), and
  [models-perf](../models-perf/spec.md) describe state-only ChatGPT management or
  curated/no-network models. This feature supersedes those ChatGPT-specific
  assumptions. Their API-key behavior and model-dialog caching goals remain.
- [Model metadata](../model-metadata/spec.md) receives the parent-owned cost
  amendment. Optional quota summaries must stay separate from `ctx`; SIWC does
  not guarantee the old Codex rate-window headers.
- [Usage guide](../../docs/usage.md) applies the same unknown-unless-reported
  spend rule to ChatGPT plan use.
- [Model recovery](../model-recovery/spec.md) must distinguish an explicit plan
  limit from transient generic 429 errors and must not retry unsupported requests
  or introduce a billing fallback.

## Evidence and remaining constraints

Official documentation was read without inspecting user credentials or starting
sign-in. Implementation proceeds in parallel. Parent records commands and
outcomes only after integrated code stabilizes. Required remaining evidence:
automated checks of the migration, an explicitly authorized real account/browser
probe, interactive branding/usage/recovery checks, background-consent review,
and the published-release walkthrough. No Plus/Pro OAuth success is inferred
from fixtures or historical Codex tests.
