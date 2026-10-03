# Feature: pluggable web search and public HTTPS fetch

**Status:** planned. **Phase:** 3. **Product requirements:** FR-34.
Revised 2026-10-04: pluggable search providers approved by the user.
Optional main-agent tools; explore remains offline/repository-read-only.

## Configuration and availability

Support one explicitly configured search backend at a time: Brave, Tavily,
Exa, or DuckDuckGo. Keep direct fetch separate. Read nonsecret enablement from
private `<stateDir>/tools.json`:

```json
{"web":{"search":{"enabled":true,"backend":"brave"},"fetch":{"enabled":true}}}
```

`backend` accepts exactly `"brave"`, `"tavily"`, `"exa"`, or `"duckduckgo"`;
any other value disables the affected web tools with a visible error. Exactly
one backend is configured and used at a time; there is no fallback to another
vendor, ever, on failure, rate limiting, or misconfiguration.

Firecrawl, Parallel, and TinyFish are deliberately out of scope for this
milestone: Firecrawl is a scraping/extraction service rather than a search
API, and Parallel/TinyFish are niche with less-documented public contracts.
Adding a provider later is an additive config enum value plus an adapter; it
does not change this contract.

Search credentials resolve the same way for every keyed provider: environment
wins over the private `<stateDir>/tool-keys.json` file.

| Backend | Environment variable | `tool-keys.json` entry |
| --- | --- | --- |
| `brave` | `BRAVE_SEARCH_API_KEY` | `{"brave":"<key>"}` |
| `tavily` | `TAVILY_API_KEY` | `{"tavily":"<key>"}` |
| `exa` | `EXA_API_KEY` | `{"exa":"<key>"}` |
| `duckduckgo` | none — keyless | none |

Only the configured backend's entry is read. Store secret files 0600 in a
private 0700 directory. Do not read repo `.env`, reuse model credentials,
include keys in prompts/history/errors, or accept an arbitrary endpoint/proxy
override in this milestone.

Unconfigured/disabled/unavailable tools stay absent from model definitions,
with a reason in `/tools` and setup/help. Invalid config disables affected web
tools with a visible error; repository work still operates. Apply changes at
run boundaries and invalidate relevant in-memory web grants.

## Search contract

`web_search` takes required nonempty `query` and optional `limit` (1–10).
Bound query text to 400 characters. Each backend uses its documented API
shape below; every backend's wire behavior is unverified until a live probe:

- Brave (unchanged): `GET https://api.search.brave.com/res/v1/web/search`,
  `q`, `count`, `result_filter=web`, and `X-Subscription-Token`
  authentication. Request ordinary web results, not Brave Answers or
  summary-key generation.
- Tavily: `POST https://api.tavily.com/search` with `Authorization: Bearer`
  authentication and JSON body `{"query","max_results"}`; results are read
  from `results[].title/url/content`.
- Exa: `POST https://api.exa.ai/search` with `x-api-1` header authentication
  and JSON body `{"query","numResults"}`; results are read from
  `results[].title/url` plus optional `text`/`snippet` fields.
- DuckDuckGo: keyless `GET https://html.duckduckgo.com/html/?q=`. This is an
  unofficial HTML endpoint, not a documented public API: its markup may change
  without notice, automated use may be blocked or rate-limited at any time,
  and its use sits in a gray zone of DuckDuckGo's terms. Disclose this
  fragility in setup/help and consent copy; it is never offered as a silent
  fallback for a keyed provider.

Reject search redirects without forwarding authentication or reissuing the
query. Do not silently retry another vendor; expose timeout/rate-limit/auth
failures and safe retry guidance.

Every backend returns the same normalized result shape: provider name, sent
query, ranked titles/URLs/available snippets in backend order, retrieval time,
and completeness. Snippets come only from what the backend supplies (Brave
`description`, Tavily `content`, Exa optional `text`/`snippet`, DuckDuckGo
result excerpts); missing snippets are never invented, result URLs are never
fetched automatically, and search snippets are never described as verified
facts. Sanitize presentation/terminal control sequences without executing
content.

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
and a per-provider honest privacy notice. Ask on first fetch per canonical
origin, showing URL and the same scope. Grant applies only to this active
conversation/app lifetime; clear on session switch/resume, app exit, or
relevant config/credential change — so switching backends always requires
fresh consent.

Per-provider privacy copy:

- Brave: Brave receives the query and request metadata; its API privacy
  notice states default query records may persist up to 90 days. Do not
  promise consumer-search or zero-retention privacy.
- Tavily and Exa: the provider receives the query and request metadata, with
  retention per that provider's own privacy policy. Do not fabricate specific
  retention numbers.
- DuckDuckGo: the query and request metadata reach DuckDuckGo over the
  unofficial HTML endpoint described above, with its markup/blocking/ToS
  fragility disclosed alongside.

Later calls within a granted scope remain visible without another prompt. Each
new redirect origin requires its own consent before connection, within at most
five redirects. Decline performs no request to that new target and returns a
refusal; already-completed allowed hops remain accurately reported. Cross-origin
consent cannot forward search authentication. Pending consent is cancellable;
allow 30 seconds of cumulative network/processing time after approval, excluding
time awaiting any required consent. Enforce cancellation across DNS, requests,
body conversion, and queued UI decisions; late responses cannot trigger work.

Disclose that the configured search provider receives the query and request
metadata, fetched sites see their URL/request metadata, and selected
results/answers reach the configured model provider. Do not automatically
attach repository files/history to queries. Warn that model-written queries
can contain project details and require user inspection at first use. No
vendor fallback, hidden extraction provider, or `curl` fallback.

## Plan mode, resume, and acceptance

Web remains allowed in /plan only under this same consent policy. Web reads
are network activity, not an exception to authorization. Explore cannot use
web even when the parent has a grant. Treat returned content as untrusted data;
ignore embedded instructions that request execution, secrets, or policy changes.

- Disable/misconfigure search/fetch for each backend, then configure them
  without exposing a key; confirm an unsupported `backend` value fails
  visibly.
- Configure and switch among `brave`, `tavily`, `exa`, and `duckduckgo` at run
  boundaries; confirm each switch clears prior grants and requires fresh
  consent.
- Approve/refuse first search use per backend with that backend's consent copy
  (Brave 90-day retention, Tavily/Exa policy-referenced retention, DuckDuckGo
  unofficial-endpoint fragility); keyless DuckDuckGo first use still prompts.
- Inspect subsequent calls, switch sessions/resume, and confirm grants reset.
- Exercise cross-origin/private redirects, DNS/address changes, credentials in
  URLs, proxy environment, unsupported media, and oversize/slow responses.
- Confirm no extraction model, fallback vendor, resource fetch, or shell call
  occurs; every contacted origin and discarded payload is disclosed.
- Cancel consent/network work and resume historical results without replay.
- Treat each provider's wire behavior as unverified until a live probe;
  record live backend behavior separately from documented API capability.

---

Revision note, 2026-10-04: pluggable search providers approved by the user.
Replaced the Brave-only contract with one configured backend chosen among
`brave`, `tavily`, `exa`, and `duckduckgo` in `tools.json`. Added per-provider
credentials (`TAVILY_API_KEY`/`tavily`, `EXA_API_KEY`/`exa`; DuckDuckGo is
keyless) under the same 0600/0700 private-file and env-wins rules, per-provider
wire contracts each labeled unverified until a live probe, a shared normalized
result shape, and per-provider honest consent copy. Firecrawl, Parallel, and
TinyFish are out of scope for this milestone: Firecrawl is a scraping/
extraction service rather than a search API, and Parallel/TinyFish are niche
with less-documented public contracts; adding a provider later is an additive
enum value plus adapter. The no-fallback rule, fetch contract, address policy,
conversation-scoped grants, explore offline rule, plan-mode consent, and the
hidden-extraction-provider prohibition are unchanged. Original title:
"Feature: explicit Brave search and public HTTPS fetch."
