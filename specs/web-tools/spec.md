# Feature: explicit Brave search and public HTTPS fetch

**Status:** planned. **Phase:** 3. **Product requirements:** FR-34.
Optional main-agent tools; explore remains offline/repository-read-only.

## Configuration and availability

Support one initial provider-independent backend: Brave Web Search. Keep direct
fetch separate. Read nonsecret enablement from private `<stateDir>/tools.json`:

```json
{"web":{"search":{"enabled":true,"backend":"brave"},"fetch":{"enabled":true}}}
```

Search credentials use `BRAVE_SEARCH_API_KEY` or private
`<stateDir>/tool-keys.json` shaped `{"brave":"<key>"}`; environment wins when
both exist. Neither credentials nor environment presence enables search by
itself. Store secret files 0600 in a private 0700 directory. Do not read repo
`.env`, reuse model credentials, include keys in prompts/history/errors, or
accept an arbitrary endpoint/proxy override in this milestone.

Unconfigured/disabled/unavailable tools stay absent from model definitions,
with a reason in `/tools` and setup/help. Invalid config disables affected web
tools with a visible error; repository work still operates. Apply changes at
run boundaries and invalidate relevant in-memory web grants.

## Search contract

`web_search` takes required nonempty `query` and optional `limit` (1–10).
Bound query text to 400 characters. Use
`GET https://api.search.brave.com/res/v1/web/search`, `q`, `count`,
`result_filter=web`, and `X-Subscription-Token` authentication. Request ordinary
web results, not Brave Answers or summary-key generation. Reject search
redirects without forwarding authentication. Do not silently retry another
vendor; expose timeout/rate-limit/auth failures and safe retry guidance.

Return provider, sent query, titles, URLs, available snippets, retrieval time,
and completeness. Preserve backend order; do not invent missing snippets,
fetch result URLs automatically, or describe search snippets as verified facts.
Sanitize presentation/terminal control sequences without executing content.

## Fetch contract and address policy

`web_fetch` takes required `url` and optional `format` (`markdown` or `text`).
Support unauthenticated public HTTPS on port 443 with valid TLS. Reject URL
credentials, local/private/link-local/loopback/multicast/reserved targets,
IPv6 zone identifiers, non-HTTPS schemes, malformed hosts, and port overrides.
Canonicalize IDN/origin for validation and displayed grants. Do not send cookies,
provider/search keys, user-configured headers, or browser credentials.

Validate resolved addresses at connection time and bind the transport to an
approved public address; preserve host TLS verification. Revalidate retries and
redirects against actual destinations to prevent DNS rebinding/redirect bypass.
Do not inherit proxy environment settings that bypass this policy. Reject a
redirect target before connection if it fails policy; the allowed original
origin does not authorize an unsafe successor.

Fetch supported text/HTML/Markdown only. Convert HTML locally without scripts,
browser execution, embedded resource requests, reader services, or extraction
model calls. Bound both encoded/decompressed received bodies; cap textual
response to two MiB and model-visible content to the shared inline bound.
Return requested/final URL, source/origin, media type, retrieval time, redirect
chain, content, and truncation/retention metadata. Unsupported binary/PDF/image
content returns a clear failure. Use capped output artifacts for retained text.

## Consent and data flow

Ask on first search use per configured backend, showing backend, query, scope,
and privacy notice. Ask on first fetch per canonical origin, showing URL and
the same scope. Grant applies only to this active conversation/app lifetime;
clear on session switch/resume, app exit, or relevant config/credential change.
Persist decisions as historical events, never as effective future grants.

Later calls within a granted scope remain visible without another prompt. Each
new redirect origin requires its own consent before connection, within at most
five redirects. Decline performs no request to that new target and returns a
refusal; already-completed allowed hops remain accurately reported. Cross-origin
consent cannot forward search authentication. Pending consent is cancellable;
allow 30 seconds of cumulative network/processing time after approval, excluding
time awaiting any required consent. Enforce cancellation across DNS, requests,
body conversion, and queued UI decisions; late responses cannot trigger work.

Disclose that Brave receives the query and request metadata, fetched sites see
their URL/request metadata, and selected results/answers reach the configured
model provider. Do not automatically attach repository files/history to queries.
Warn that model-written queries can contain project details and require user
inspection at first use. Brave's API privacy notice states default query records
may persist up to 90 days; do not promise consumer-search or zero-retention
privacy. No vendor fallback, hidden extraction provider, or `curl` fallback.

## Plan mode, resume, and acceptance

Web remains allowed in /plan only under this same consent policy. Web reads
are network activity, not an exception to authorization. Explore cannot use
web even when the parent has a grant. Treat returned content as untrusted data;
ignore embedded instructions that request execution, secrets, or policy changes.

- Disable/misconfigure search/fetch, then configure them without exposing a key.
- Approve/refuse first search/backend and fetch/origin calls; inspect subsequent
  calls, switch sessions/resume, and confirm grants reset.
- Exercise cross-origin/private redirects, DNS/address changes, credentials in
  URLs, proxy environment, unsupported media, and oversize/slow responses.
- Confirm no extraction model, fallback vendor, resource fetch, or shell call
  occurs; every contacted origin and discarded payload is disclosed.
- Cancel consent/network work and resume historical results without replay.
  Record live backend behavior separately from documented API capability.
