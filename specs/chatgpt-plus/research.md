# Official Sign in with ChatGPT: migration evidence

**Reviewed:** 2026-10-05. **Migration status:** in progress. Sources below are
official OpenAI documentation read on that date; the SIWC Terms page is dated
2026-09-29. This review used public documentation only, with no credential reads,
live sign-in, entitlement checks, or live inference.

This page supersedes the 2026-09-29 research about third-party Codex integrations.
The current implementation contract is [spec.md](spec.md) and FR-02 in
[v1-spec.md](../v1-spec.md), not another harness's development-branch code.

## Primary sources

| Official source | Evidence used | Date checked |
| --- | --- | --- |
| [OSS/local overview](https://developers.openai.com/siwc/token-sharing-open-source.md) | Optional plan usage, user/workspace-bound client registration, host identity, public Responses flow, free/local OSS category. | 2026-10-05 |
| [Registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in.md) | Dynamic public-client registration, exact authorize/token endpoints, loopback rules, PKCE/state/nonce, ID-token verification, plan scopes, protected storage. | 2026-10-05 |
| [Accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions.md) | Separate account/workspace registrations, active labels, returning hints, serialized refresh, revocation, local credential security, shared allowance. | 2026-10-05 |
| [Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference.md) | Authenticated `models[]` envelope, display/slug/visibility, public `/v1/responses`, stateless streaming, completed-only success. | 2026-10-05 |
| [Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations.md) | Required HTTP request shape, unsupported fields/tools, function namespaces, input and conversation-state limits. | 2026-10-05 |
| [Token reference](https://developers.openai.com/siwc/token-sharing-open-source/token-reference.md) | Returned token fields, granted scope, expiry and rotating refresh, opaque access-token metadata. | 2026-10-05 |
| [Errors and recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery.md) | Identity-only authorization, explicit consent recovery, admission/error shapes, scope/eligibility/quota/refresh handling. | 2026-10-05 |
| [UI/UX guidelines](https://developers.openai.com/siwc/ui-ux-guidelines.md) | Continue with ChatGPT, first-use confirmation, visible plan use, Manage usage, limit recovery and branding. | 2026-10-05 |
| [Sign in with ChatGPT Terms](https://openai.com/policies/sign-in-with-chatgpt-terms/) | Application identity, user-controlled runtime/storage, no app charge for SIWC, express background consent, no account/allowance pooling or limit bypass, privacy/branding obligations. Published 2026-09-29. | 2026-10-05 |

## 1. Scope, category, allowance, and terms

The overview documents direct SIWC for open-source and locally hosted apps.
Likha is a free, locally run OSS tool, so this is its integration path. Paid or
remotely managed services require separate engagement with OpenAI. This is a
documented category match, not a promise that a particular account/workspace is
eligible or that OpenAI has approved this implementation.

Identity scopes do not grant inference. Plan usage requires
`chatgpt.tokens.use.direct`, resource permission, user consent, and eligible
account/workspace access. SIWC does not expose existing ChatGPT conversations or
account context. Requests use the connected app's allowance for the authenticated
user, not a general-purpose relay for other tools or users.

The shared allowance has limits; models and policies can differ by account.
Plus users share a five-hour total across apps using their ChatGPT plan; the
accounts/sessions page says this five-hour limit does not apply to Pro users.
That is not a promise of unlimited Pro usage or a guaranteed seven-day window.
App-specific limits can also apply. Users manage allowance and explicit credit
opt-in at [ChatGPT Usage](https://chatgpt.com/settings/usage). No fixed cost or
zero-cost guarantee follows from the provider name. Likha must leave usage/cost
unknown unless OpenAI reports them and must not silently switch to a separately
billed API key when plan use is denied or exhausted.

The SIWC Terms require the app's own identity and reasonable security/privacy
controls. Persistent tokens must stay local and under the user's control.
Requests must arise from that user's activity or expressly authorized automation;
obtain express consent before background use. No account rotation, splitting,
pooling, resale, or other limit bypass is permitted. Free SIWC use cannot require
paying Likha or upgrading an app tier. Likha implements the documented protocol
in Go without copying DevKit software or another application's credentials;
this choice does not waive OpenAI's terms, license conditions, branding rules,
or maintainers' privacy responsibilities.

## 2. Client registration and host identity

A client registration binds to the authenticated user and selected workspace.
A host identifies where the local tool runs. Host restarts and sign-out do not
create new hosts or registrations. Save the host ID before the first sign-in.
The overview accepts a JWK thumbprint URI, a `urn:uuid:<UUIDv4>` value, or
`did:key`; the identifier must be opaque rather than an email or user ID.

For a first registration, use `client_id=dynamic_agent_client`,
`agent_name_hint=Likha`, and the persisted `ext_agent_host_id`. Save the callback's
issued client ID and use it for token exchange and returning sign-in.
`dynamic_agent_client` is an entrypoint, not a durable issued client ID.

Keep separate registrations keyed by issued client ID and verified ID-token
`sub`, even when email labels match. Email and `sub` alone are not workspace IDs.
Show distinct stable labels and the active choice. For returning authorization,
omit `agent_name_hint`, reuse issued client ID and host ID, and associate retained
ID-token/email hints only with that selected registration. A pending sign-in
cannot overwrite a working account before its identity is verified.

## 3. Supported browser and token protocol

| Concern | Official requirement |
| --- | --- |
| Authorization endpoint | `https://auth.openai.com/api/accounts/authorize` |
| Token endpoint | `https://auth.openai.com/api/accounts/oauth/token` |
| Public resource | `https://api.openai.com/v1` |
| Identity scopes | `openid profile email` |
| Plan/renewal scopes | `offline_access resource.invoke chatgpt.tokens.use.direct` |
| Callback | HTTP `127.0.0.1` loopback, listener started before browser launch. Across reconnects only the port may vary; scheme/host/path stay unchanged. The exact per-attempt URI must match authorization and exchange. `localhost` is not a substitute. |
| Proof/binding | Fresh random state, nonce, and PKCE verifier per attempt; S256 challenge. Validate state even on `access_denied`. |
| New callback | Require code, matching state, and issued client ID; missing issued ID leaves registration incomplete. |
| Returning callback | It may omit client ID; preserve the pending registration's exact ID and reject a different one. |
| Code exchange | Form-encoded `authorization_code`, issued client ID, code, verifier, exact callback URI, and resource. No client secret or partner API key. |
| ID token | Verify signature against OpenAI's published JWKS; check issuer, audience against issued ID, expiry, and nonce. Use verified `sub`. Decoding claims is insufficient. |
| Inference permission | Use token response's granted scopes. A valid ID token alone cannot enable plan inference. |
| Storage | Protected per-registration record with identity, tokens, scopes, expiry and saved timing; owner-only `0600` file, atomic writes, no commits/logs. Likha also requires a private `0700` state directory. |

Likha's browser-first UX and ephemeral callback port are product decisions within
this official flow. First-run and unconnected `/providers` selection launch the
browser without another keypress. Only a launcher error reveals the manual URL.
The callback page must acknowledge receipt, not claim success before exchange,
signature/claims verification, and permission checks finish.

The new CLI method is `likha --provider chatgpt --login`. The earlier
`--device-login` method is unsupported/deprecated; docs and help must direct
users to the browser flow. Nothing in these sources authorizes reuse of the
old Codex device endpoints as an SIWC alternative.

## 4. Refresh and disconnection

The token response includes access/refresh/ID tokens, token type, expiry,
space-separated granted scope, and `earliest_refresh_at`. The reference lists
one-hour access tokens and 30-day refresh tokens; each successful refresh returns
a replacement refresh token with a new lifetime. Use returned timing rather than
guessing from token contents.

Refresh with `grant_type=refresh_token`, the associated issued client ID, saved
refresh token, and resource. Omit scope to retain the grant. Serialize refreshes
for the same session across processes and replace access, refresh, scopes, and
expiry together. A goroutine-only single-flight does not address two processes
sharing a rotating token store.

On sign-out, stop requests and attempt revocation at the `revocation_endpoint`
from `https://auth.openai.com/.well-known/openid-configuration`. POST the refresh
token with `token_type_hint=refresh_token` and issued client ID. Empty HTTP 200
is success, including an already-invalid token. Retry network/5xx failures with
bounded backoff while the token is available. If remote revocation cannot be
confirmed, clear local access/refresh/ID tokens and warn that the user may need
to disconnect the app in ChatGPT Settings. Keep the registration mapping and
host ID for reconnect.

Terminal refresh errors include `invalid_grant`, `invalid_refresh_token`,
`token_expired`, `refresh_token_expired`, `refresh_token_invalidated`, and
`refresh_token_reused`. Clear unusable tokens and reauthorize with the saved
issued client ID. `invalid_client` points to client configuration, not a generic
user-password problem. Temporary network/infrastructure failures alone do not
justify deleting credentials. OpenAI does not currently push disconnect
notifications to the app.

## 5. Public models and Responses

Fetch `GET https://api.openai.com/v1/models` with the selected OAuth bearer token.
SIWC returns `models[]`: display entries with `visibility == "list"`, preserve
server ordering, show `display_name`, and use `slug` for requests. Reload on
account switches. This is not the API-key `data[].id` envelope; a bundled catalog
or hard-coded list cannot prove account eligibility.

Send `POST https://api.openai.com/v1/responses` with that bearer token,
`store: false`, `stream: true`, and `input` as an array carrying needed history.
Use `instructions` or developer messages rather than explicit system message
items. Over HTTP, omit `previous_response_id` and persistent `conversation`.
Function/custom tools need supported namespaces or `additional_tools` input
items. Likha keeps its own agent, local tools, approval flow, and cancellation.

The preview rejects `background`, `conversation`, `max_output_tokens`,
`max_tool_calls`, `metadata`, `moderation`, `multi_agent`, `prompt`,
`prompt_cache_retention`, `safety_identifier`, `temperature`, `top_logprobs`,
`top_p`, `truncation`, and `user`. Hosted MCP/connectors, file search, image
generation, Code Interpreter, native computer use, Responses `tool_search`, and
top-level `programmatic_tool_calling` are not supported by this route. Local
function tools do not grant those hosted capabilities. Text/images/files depend
on the selected model; audio/video, Files upload, and transcription are outside
this flow. Likha's separately planned attachment UI is not implemented by this
migration.

Read through `response.completed` before accepting success. `response.failed`
can report a usage-limit failure after deltas begin. `response.incomplete`,
explicit error, interruption, and EOF without completion are distinct failures,
not successful partial responses or authority to dispatch partially received tools.

## 6. Error, usage, and UI contract

Preserve status, response shape, exact code/parameter when present, and request
ID. Direct admission can return `{"detail":"..."}` rather than an API `error`
object; its diagnostic text is not a stable code. The official recovery page
distinguishes scope/identity rejection (401), policy/region refusal (403), and
temporary routing unavailability (503).

| Structured code | Documented recovery |
| --- | --- |
| `subscription_sharing_user_not_eligible` | Explain account/workspace/policy restriction; do not repeat requests or loop through OAuth. |
| `subscription_sharing_usage_limit_exceeded` | Pause plan requests and open ChatGPT Usage. An app-specific limit may apply; do not infer a reset time or an empty whole plan. |
| `subscription_sharing_usage_unavailable`, `subscription_sharing_user_unavailable` | Preserve credentials; retry temporarily unavailable service with bounded backoff. |
| `subscription_sharing_unsupported_capability` | Inspect `error.param` and fix/remove unsupported inputs/tools/overrides; do not resend the same invalid request. |
| `subscription_sharing_route_not_supported` | Correct method/endpoint; another client's routes do not authorize this one. |
| `subscription_sharing_invalid_user` | Diagnose credential context and retain request ID; reconnect after confirmed revocation or terminal refresh failure. |
| `chatpass_v2_scope_not_authorized`, `chatpass_v2_invalid_authorization_context` | Correct client/grant configuration; do not retry or change billing. |

For identity-only login, mark plan use disabled and offer explicit enable-plan
consent or user-selected separate API-key configuration. `prompt=consent` is the
documented existing mechanism for explicit enablement; use `force_reconsent=true`
only after OpenAI confirms deployment for the integration. Ordinary returning
sign-in must not force reconsent. This OAuth parameter is unrelated to the
unsupported Responses request-body `prompt` field.

UI guidance calls for **Continue with ChatGPT**, a once-only **You're using your
ChatGPT plan** confirmation, visible **Using ChatGPT plan**, and **Manage usage**
linking to [ChatGPT Usage](https://chatgpt.com/settings/usage). A limit notice
makes Manage usage primary. Likha has no own credit product to upsell and must
not imply OpenAI sponsorship. A terminal-appropriate presentation and its
interactive verification remain implementation work.

## Superseded history and verification boundary

The 2026-09-29 notes examined a development-branch OpenCode Codex plugin and OMP
material. They led to a copied public Codex client ID, fixed `localhost:1455`,
private backend/device endpoints, decoded-only account claims, curated models,
Codex rate headers, and zero-cost assumptions. Those are obsolete for this
feature and must not be used as current requirements or migration proof.
Development-branch observations do not establish OpenCode V2 internals; this
document makes no claim about V2's SIWC implementation.

Old credentials cannot be repurposed without official registration and fresh
sign-in. Prior local Codex fixture tests establish only the behavior they tested.
They do not establish working SIWC OAuth for a Plus/Pro account, live model
availability, public-route tool compatibility, or released-binary usability.
The parent will record integrated automated outcomes; a real account/browser
probe needs explicit user approval and a published-release walkthrough remains
open.
