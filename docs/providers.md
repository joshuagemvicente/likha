# Providers and sign-in

Hosted providers, key resolution, first-run setup, and official Sign in with
ChatGPT (SIWC). Back to the [README](../README.md).

**ChatGPT migration status:** in progress (2026-10-05). This guide describes the
replacement browser-first flow. No real account/browser probe has run. Local
fixtures do not establish working Plus/Pro authorization, entitlement, or live
Responses compatibility; published-release verification is also pending.

## Requirements

- Go 1.24 or later to build from source or produce local release archives;
  macOS or Linux. A downloaded, prebuilt archive does not require Go.
- An interactive terminal for the TUI; Likha does not run it through a pipe.
- A hosted provider and your API key, or an eligible ChatGPT Plus/Pro account
  with explicit plan-sharing consent. ChatGPT sign-in requires a browser that
  can reach the local callback on the machine running Likha; it has no device-code
  alternative.
- A model that supports SSE streaming **and structured tool calls**. Pending-probe
  providers have not passed Likha's full live check. Likha hosts no models and
  bundles no local inference server.

## Predefined providers

| Provider | `--provider` name | API key | Base URL (default) | Default model | Probe status |
| --- | --- | --- | --- | --- | --- |
| OpenAI | `openai` | `LIKHA_OPENAI_API_KEY` | `https://api.openai.com/v1` | `gpt-4o-mini` | pending-probe |
| OpenRouter | `openrouter` | `LIKHA_OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` | `openai/gpt-4o-mini` | pending-probe |
| Amazon Bedrock | `bedrock` | `LIKHA_BEDROCK_API_KEY` | `https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1` (other regions via `--endpoint`) | `anthropic.claude-3-5-haiku-20241022-v1:0` | pending-probe |
| Dialagram | `dialagram` | `LIKHA_DIALAGRAM_API_KEY` | `https://dialagram.me/router/v1` | none — pass `--model` | pending-probe |
| Opencode Zen | `opencode-zen` | `LIKHA_OPENCODE_ZEN_API_KEY` | `https://opencode.ai/zen/v1` | `gpt-5.3-codex` | pending-probe |
| Opencode Go | `opencode-go` | `LIKHA_OPENCODEGO_API_KEY` | `https://opencode.ai/zen/go/v1` | `glm-5.3-flash` | live-verified* |
| ChatGPT (Plus/Pro) | `chatgpt` | none — ChatGPT browser sign-in | `https://api.openai.com/v1` (Responses) | from eligible account models | pending-probe |
| Groq | `groq` | `LIKHA_GROQ_API_KEY` | `https://api.groq.com/openai/v1` | none — pass `--model` | pending-probe |
| xAI | `xai` | `LIKHA_XAI_API_KEY` | `https://api.x.ai/v1` | none — pass `--model` | pending-probe |
| Together AI | `together` | `LIKHA_TOGETHER_API_KEY` | `https://api.together.ai/v1` | none — pass `--model` | pending-probe |
| Mistral AI | `mistral` | `LIKHA_MISTRAL_API_KEY` | `https://api.mistral.ai/v1` | none — pass `--model` | pending-probe |
| Cerebras | `cerebras` | `LIKHA_CEREBRAS_API_KEY` | `https://api.cerebras.ai/v1` | none — pass `--model` | pending-probe |
| Anthropic Claude | `claude` | `LIKHA_CLAUDE_API_KEY` | `https://api.anthropic.com/v1` (OpenAI SDK compatibility layer) | `claude-opus-5-5` | pending-probe |
| DeepSeek | `deepseek` | `LIKHA_DEEPSEEK_API_KEY` | `https://api.deepseek.com/v1` | `deepseek-flash` | pending-probe |
| Google Gemini | `gemini` | `LIKHA_GEMINI_API_KEY` | `https://generativelanguage.googleapis.com/v1beta/openai` (OpenAI compatibility endpoint) | `gemini-2.5-flash` | pending-probe |

`live-verified*` means a user-run probe passed on 2026-09-29; repeat the full probe
after provider-facing request changes before release. A pending-probe API-key
row's default is the shipped code default, not a probed choice. Endpoint docs or
route compatibility do not substitute for Likha's full live checks: streamed
text, structured tool calls, cancellation, tool-result fidelity, and revoked-key
handling. ChatGPT's migration also requires a real account/browser probe.

For API-key providers, key resolution order is `--api-key`, then `LIKHA_API_KEY`,
then the provider's own `LIKHA_<NAME>_API_KEY`, then private `providers.json`
(mode `0600`). Passing `--api-key` once stores the key for later runs. Keys never
come from the selected repository or enter the session database. `chatgpt` uses
its own saved registration and OAuth tokens, not these key sources; see
[Sign in with ChatGPT (Plus/Pro)](#sign-in-with-chatgpt-pluspro). Endpoint URLs
may not contain credentials, query strings, or fragments; plain HTTP is accepted
only for loopback hosts. The ChatGPT provider uses the documented public OpenAI
endpoint, not a private Codex backend or a custom billing fallback.

Likha checks the connection at startup and before each agent run; it reports a
dead or revoked key before sending prompts. Endpoints outside the list carry a
visible **unverified** warning. `opencode-go` is live-verified; the remaining
providers, including ChatGPT, await live probes. See
[predefined providers](../specs/predefined-providers/spec.md).

Likha identifies itself with `User-Agent: likha/<version>`. The Opencode providers
also receive your stable conversation ID in `x-opencode-session` for routing and
prompt caching. ChatGPT sends the selected account's bearer access token to
public `/v1/models` and `/v1/responses`; the old Codex account/routing headers are
not part of its SIWC contract.

Without a model name, API-key providers use their documented defaults (listed
above); a row with `none — pass --model` requires `LIKHA_MODEL` or `--model`.
ChatGPT choices come from the selected account's eligible models. For an endpoint
outside the list, Likha uses the first model that endpoint reports.
`likha --help` prints options, providers, environment variables, and examples.

## First-run setup (no flags needed)

Run `likha` without provider configuration:

1. **Choose a provider**: ↑/↓ and Enter, or type to filter. Rows show the host and
   whether the provider's `LIKHA_*_API_KEY` variable is set.
2. **Connect**: API-key providers show masked input with a character count and
   check the key. A failed check leaves the field open to fix it. If the key
   variable is set, Enter on an empty field uses it without saving it to disk.
   Selecting **ChatGPT** opens the system browser automatically for OpenAI
   sign-in and consent. No key, device code, pasted code, or second launch action
   is required. Likha shows a manual URL only when its browser launcher fails.
3. **Choose a model**: filter the provider's reported models, with context sizes
   where available. An eligible default may be marked `recommended`; a single
   model auto-selects. After validated ChatGPT authorization, Likha fetches the
   current account's eligible models automatically. It does not use a curated
   model list to infer plan access.
4. **Pick a theme**: preview each theme in the screen and a sample transcript.

Likha stores your choices in private `config.json` and `providers.json`, both
mode `0600`. Later launches use the stored configuration. Explicit flags and
environment settings take precedence; non-interactive unconfigured invocations
fail clearly rather than starting setup.

Using a stored key/login from setup:

```sh
go run ./cmd/likha /path/to/repository
```

Or explicitly configure an API-key provider:

```sh
export LIKHA_API_KEY="sk-..."
go run ./cmd/likha --provider opencode-go /path/to/repository
```

For API-key providers, `--model` and `--endpoint` override stored choices and
defaults. An unlisted provider requires `--endpoint`, not an unknown `--provider`
name, and runs unverified. A model must emit structured tool calls, not prose
instructions. Connection, revoked-key, and incompatible-protocol errors keep the
UI open. Omit the repository argument to use the current directory, and put
options before the path. `--help` and `--version` do not enter the TUI.

## Sign in with ChatGPT (Plus/Pro)

The `chatgpt` provider uses official SIWC to authorize eligible requests against
your shared plan allowance. OpenAI documents this direct flow for free,
open-source/local tools such as Likha. Likha registers under its own name and
implements the public protocol in Go; it does not reuse Codex's client ID, copy
DevKit code, or launch Codex. This category match does not guarantee your
account/workspace entitlement or OpenAI endorsement.

### Browser sign-in

1. Select ChatGPT in first-run setup, or select its unconnected row in
   `/providers`. **Continue with ChatGPT** starts the flow and opens your system
   browser without a second keypress.
2. Sign in, choose the account/workspace for a new registration, and authorize
   plan sharing in OpenAI's browser screen. Identity-only sign-in is insufficient
   for inference. Likha shows a waiting state; cancel if you do not want to
   proceed. A manual authorization URL appears only if browser launch fails.
3. The browser returns to an ephemeral-port callback on `127.0.0.1` with a stable
   `/auth/callback` path. Its page acknowledges receipt while Likha verifies the
   result; receipt alone does not mean sign-in succeeded.
4. Likha verifies identity and granted plan permission, then fetches eligible
   models from public `https://api.openai.com/v1/models`. It shows reported display
   names and uses model slugs for requests. Empty/failed discovery reports an
   error rather than substituting a hard-coded list.

After your first successful plan authorization, the UI confirms **You're using
your ChatGPT plan**. **Using ChatGPT plan** and **Manage usage** let you recognize
plan use and open [ChatGPT Usage](https://chatgpt.com/settings/usage).

For the same browser flow without entering the TUI:

```sh
likha --provider chatgpt --login
# From a source checkout:
go run ./cmd/likha --provider chatgpt --login
```

Then launch Likha with `--provider chatgpt` in your repository. This is browser
login, not a remote device-code flow; the browser must reach the local callback
on the running machine. `--device-login` is **deprecated and unsupported** and
directs you to `--login`. No ChatGPT token environment variable or API-key flag
replaces official registration.

### Saved accounts, reconnect, and sign-out

Use `/providers` to add an account/workspace, reconnect, choose a saved
registration, or sign out. The picker uses distinct stable labels and shows the
active account. Two workspaces can show the same email and still require separate
registrations. Use `/models` to select a provider/model for the conversation;
account changes refresh ChatGPT's model choices. Likha does not rotate accounts
to work around plan limits.

Before first sign-in, Likha persists a stable opaque identity for this host.
OpenAI issues each new registration's client ID. Likha keeps it with the verified
account identity and reuses it for reconnect. Restarting Likha, switching
accounts, or signing out does not create another host or discard registration
metadata. A new attempt does not replace your active account before validation.

Sign-out stops requests for the selected account, attempts remote revocation,
and clears its local access, refresh, and ID tokens. Likha retains host and
registration metadata for a later reconnect. If revocation cannot be confirmed,
it warns you; also disconnect the app in ChatGPT settings if needed. Signing out
one saved registration does not erase the others.

**Migrating an old login:** legacy Codex tokens lack the required SIWC
registration. Sign in again with the browser method. Likha preserves stored API
keys, sessions, and unrelated configuration instead of treating old tokens as
official SIWC credentials.

### Allowance, credits, and recovery

ChatGPT plan usage shares allowance with other apps using your plan. It grants
neither unlimited/free inference nor guaranteed access to each model. Plus users'
five-hour limit is shared across apps; OpenAI documents that this five-hour limit
does not apply to Pro, which does not mean Pro has unlimited usage. App-specific
limits may also apply. Review the actual allowance and limits in
[ChatGPT Usage](https://chatgpt.com/settings/usage).

Optional credits follow your **explicit opt-in in ChatGPT settings**. Likha does
not buy credits or change your credit settings. It does not switch to a separately
billed OpenAI API key, another provider, or another account when plan usage fails.
Choose the `openai` BYOK provider yourself if you want API-key billing.

Usage and cost remain **unknown unless OpenAI reports them**. ChatGPT provider
selection is not proof of exact `$0.00` cost; API-key catalog prices do not price
shared plan allowance. Any reported plan usage stays distinct from `ctx`, which
measures a request's input-context usage.

| Problem | Next step |
| --- | --- |
| Consent declined or plan scope missing | Explicitly enable plan access through account management, or choose a separately configured provider. Identity-only login cannot send model requests. |
| Account/workspace not eligible, or region/policy refusal | Review the restriction. Repeated sign-in cannot override entitlement or policy. |
| Plan/app limit reached | Pause new requests and use **Manage usage**. The error alone does not establish a reset time or mean the whole plan is empty. |
| Confirmed disconnection or unusable refresh token | Reconnect the saved account with the browser flow. |
| Temporary network/availability failure | Retry later; Likha preserves recoverable credentials. |
| Unsupported capability/request | Correct the selected model, tool, or request; retrying the same invalid request will not fix it. |

### Local credentials and requests

Likha keeps separate registration records in private `providers.json`, including
validated identity, granted scopes, token set, expiry, and active choice. Host
metadata also stays in the private state directory. Unix directory permissions
are `0700`; protected files are `0600`. Writes replace files atomically. Locked
rotation keeps overlapping requests/processes from reusing a refresh token.
Credentials stay out of the repository, session database, logs, and support
transcripts. Do not share authorization URLs that contain returning-login hints.

Model turns go to public `https://api.openai.com/v1/responses` with `store: false`,
`stream: true`, and the history needed for each request. Likha keeps its own
tools, approvals, cancellation, and agent loop. It accepts success only after
`response.completed`; failed, incomplete, or interrupted streams are not
successful turns and cannot execute partial tool calls. The SIWC preview supports
a restricted set of request fields and tools, not all OpenAI API capabilities.

OpenAI receives the prompts, prior context, and tool results you send. `store:
false` does not override OpenAI's privacy policies. SIWC does not import your
existing ChatGPT conversations. Sign-in grants no new filesystem/tool permissions,
and background model use requires express consent under the
[SIWC Terms](https://openai.com/policies/sign-in-with-chatgpt-terms/). The integration
must remain in your controlled runtime and cannot pool subscriptions or evade
usage limits.

### Official references and verification

Sources checked on **2026-10-05**: [overview](https://developers.openai.com/siwc/token-sharing-open-source.md),
[sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in.md),
[accounts/sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions.md),
[models/inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference.md),
[preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations.md),
[token reference](https://developers.openai.com/siwc/token-sharing-open-source/token-reference.md),
[errors/recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery.md),
[UI guidance](https://developers.openai.com/siwc/ui-ux-guidelines.md), and
[SIWC Terms](https://openai.com/policies/sign-in-with-chatgpt-terms/) (dated
2026-09-29).

The [feature checklist](../specs/chatgpt-plus/checklist.md) separates automated
migration checks, an explicitly user-approved live account/browser probe, and
published-release verification. This guide makes no claim that live Plus/Pro
sign-in or those release gates have passed.
