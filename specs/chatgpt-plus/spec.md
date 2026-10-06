# Feature: Sign in with ChatGPT (Plus/Pro)

**Status:** in progress (2026-10-05). This official Sign in with ChatGPT
(SIWC) migration supersedes the earlier Codex integration. Automated migration
checks, a user-approved live account/browser walkthrough, and published-release
verification are separate gates; no live sign-in success is claimed.

Refines [FR-02](../v1-spec.md). Protocol and implementation details belong in
[tasks.md](tasks.md), [context.md](context.md), and [research.md](research.md).

## Purpose and eligibility

Likha offers **Sign in with ChatGPT** alongside, not instead of, its API-key
providers. An eligible ChatGPT Plus/Pro user can explicitly share their plan's
available allowance with this free, locally run, open-source coding agent.
Signing in for identity alone does not authorize model use: the user must grant
plan access and have an eligible entitlement.

Shared allowance is limited and shared with other supported uses of the plan.
It is not unlimited or free inference, and a plan does not guarantee access to
every model. Extra credits remain an explicit user opt-in. Likha never silently
switches to API-key billing, another account, or another provider when allowance
is exhausted or access is refused. The OpenAI API-key provider remains a
separately billed bring-your-own-key option.

## Browser-first sign-in

1. Selecting ChatGPT during first-run setup automatically opens the system
   browser to OpenAI's sign-in and consent screen. No API key, device code, pasted
   authorization code, or extra keypress to launch the browser is requested.
2. Selecting an unconnected ChatGPT row in `/providers` starts the same flow
   immediately. A connected row offers account management instead.
3. The user chooses an account/workspace and authorizes plan sharing in OpenAI's
   browser interface. Likha shows a waiting state and lets the user cancel.
4. If the browser launcher fails, Likha shows a manual authorization URL for the
   same flow and clear instructions. A URL is not an extra routine setup step,
   and it does not introduce a device-code or paste-code fallback.
5. A browser callback acknowledges receipt while Likha verifies the authorization;
   receiving a callback is not yet reported as successful sign-in. Only validated
   identity, plan permission, and eligible access advance setup to model choice.
6. Eligible models are fetched automatically for the selected account/workspace.
   The picker shows their reported names and uses their model identifiers when
   sending requests. A single eligible model auto-selects. Failed discovery or an
   empty eligible list produces a recoverable error, not a fabricated model list.

The browser-only CLI entry point is:

```sh
likha --provider chatgpt --login
```

It uses the same consent and local callback flow without entering the TUI.
`--device-login` is deprecated and unsupported: it explains that the user must
use `--login` and the browser method, and never starts the old device flow.
A reachable browser callback on the machine running Likha is required; this
feature does not promise a remote/headless device-code login.

## Saved accounts and sign-out

- `/providers` supports adding an account, reconnecting, selecting a saved
  account/workspace, and signing out. The active choice is labelled visibly.
- Different workspaces remain distinct even when they show the same email
  address. Email is a display label, not an account identity or a deduplication
  key. Account selection is explicit; Likha does not rotate accounts to evade
  usage limits.
- This installation's host identity and each issued account/workspace
  registration survive restarts and sign-out. Reconnecting can reuse that
  registration; signing out does not create a new host identity.
- Access and refresh tokens stay in protected private local state, never in the
  repository, conversation history, or logs. Refreshes rotate and save
  credentials safely, including when requests or Likha instances overlap.
- Signing out attempts remote revocation and then clears the selected login's
  local tokens, even if revocation fails. A failed revocation produces a warning
  explaining that the user may also need to disconnect Likha in ChatGPT settings.
  Host and registration metadata are retained, not authenticated access.
- Credentials from the old Codex integration require fresh official sign-in and
  registration. They are not silently reused as SIWC credentials. Existing API
  keys, repository sessions, and unrelated configuration remain intact.

## Model use and recovery

- ChatGPT model discovery and generation use OpenAI's documented public API,
  not a private ChatGPT/Codex backend. Likha uses its own agent loop; it does not
  launch Codex, impersonate another client, or outsource tool approvals.
- Each request carries the conversation context required for that turn without
  relying on provider-stored conversation state. Text, reasoning, and structured
  tool calls remain compatible with Likha's transcript and tools.
- A response is successful only when the provider reports completed success.
  Failed, incomplete, malformed, cancelled, or prematurely closed streams are
  not completed turns and never authorize tools from partial output.
- Expired access refreshes automatically. Denied consent, missing plan scope,
  ineligible entitlement, revoked access, quota exhaustion, network failures,
  and unsupported requests produce clear recovery guidance without closing the
  TUI or losing the conversation.
- Limit notices are not success and are not permission to bypass plan limits.
  Quota information is distinct from input-context usage. Usage and cost stay
  unknown unless the provider reports them; selecting ChatGPT alone does not
  establish an exact `$0.00` cost. Optional credits follow the user's explicit
  opt-in in ChatGPT settings.
- Cancellation, repository boundaries, tool approvals, child-agent budgets, and
  existing first-use network consent remain unchanged. Sign-in does not authorize
  unattended/background work or any new external service; any background model
  use must stay within the authorization and official usage terms.

## User-facing guidance

The README and provider guide explain browser-first setup, CLI login, account
management, legacy reconnection, allowance and separate API billing, private
local credentials, sign-out limitations, eligibility, and verification status.
Sign-in labels and disclosures follow OpenAI's SIWC UI usage guidelines without
implying endorsement or affiliation.

The action that starts sign-in reads **Continue with ChatGPT**. After the first
successful plan authorization, Likha confirms **You're using your ChatGPT plan**
once and lets the user continue. During plan use, **Using ChatGPT plan** and a
**Manage usage** link to [ChatGPT settings](https://chatgpt.com/settings/usage)
remain discoverable near the model selector or composer. A limit notice makes
that settings link the primary recovery action; Likha offers no app credits of
its own.

## Acceptance

- First-run ChatGPT selection and unconnected `/providers` selection open the
  browser once without an extra launch action; launcher failure alone reveals
  the manual URL.
- Invalid, cancelled, identity-only, or ineligible sign-in never activates the
  provider. Validated consent leads automatically to account-specific models.
- Saved workspace choices, active labels, reconnect, refresh, and sign-out obey
  the account and credential contract above, including across restarts.
- Public-API responses stream text and structured tool calls through Likha's
  approval-gated loop; cancellation prevents later actions and only completed
  responses count as success.
- Automated checks distinguish fixtures from real OpenAI interactions. A live
  probe requires an eligible account, browser access, and explicit user approval;
  `verified (release)` additionally requires the published-binary walkthrough.
