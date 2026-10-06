# DuckDuckGo keyless web search — research

**Date:** 2026-10-05 · **Status:** research note for the `duckduckgo` backend of
[spec.md](spec.md). Read-only investigation; no code was changed.
**Method:** primary sources only — DuckDuckGo's own pages (live and, where the
page is gone, the Internet Archive copy), pinned upstream source code, and a
handful of live HTTP probes made from one residential IP on 2026-10-05
(12:50–12:56 UTC). Anything not confirmed by one of these is marked
**unverified**.

## TL;DR

- There is **no official, keyless DuckDuckGo web-results API**. The only
  documented API (Instant Answer API, `api.duckduckgo.com/?format=json`) is, in
  DuckDuckGo's words, "not for full search results". Full results are only
  available by reading the human HTML pages `html.duckduckgo.com/html/` or
  `lite.duckduckgo.com/lite/`, which are undocumented as an interface.
- DuckDuckGo's bot defence answers with **HTTP 202** and an "anomaly" image
  challenge page ("Unfortunately, bots use DuckDuckGo too."), not with 429 or
  3xx. Likha's adapter does not recognise this case specifically.
- Live probes: `POST /html/` (form body) returned results 3/3; `GET /html/`
  returned 200 with likha's honest UA (1/1) but 202 challenge with a bare
  Chrome UA string (2/2); `GET /lite/` with Chrome UA was challenged (1/1),
  `POST /lite/` returned results (1/1). Small sample; treat as indicative.
- Every captured results page contained a **sponsored result first**, with the
  same `result__a` class as organic results and an href to
  `https://duckduckgo.com/y.js?ad_domain=…&ad_provider=…`. Likha's current
  parser returns that ad as rank 1 (reproduced against the captured pages).
- A genuine "no results" page is distinguishable (`result--no-result`,
  `no-results__message`); likha currently reports it as "endpoint may have
  changed or blocked this request".

## 1. Endpoints usable without a key

### 1.1 `html.duckduckgo.com/html/` (JS-free results page)

| Aspect | Observed / sourced | Source |
| --- | --- | --- |
| Method | The page's own search form and "Next" form are `<form action="/html/" method="post">`. GET with `?q=` is also served (see probes). | Live page, 2026-10-05 (§7) |
| `q` | Query text. | Live form `<input name="q">` |
| `b` | Submit-button field, empty value (`<input name="b" … value="" type="submit">`). Sent by browsers and by ddgs; not required for results (unverified whether required). | Live form |
| `kl` | Region, e.g. `us-en`, `wt-wt` ("No region"). Echoed as a hidden `<input name="kl" value="wt-wt">` when omitted. | [Help: URL parameters](https://duckduckgo.com/duckduckgo-help-pages/settings/params/); live page |
| `kp` | Safe search: `1` on, `-1` moderate, `-2` off. | Help: URL parameters |
| `kd` | "Redirect": `1` on, `-1` off — the documented switch for the `/l/?uddg=` click-redirect wrapper (behaviour on the html endpoint **unverified**). | Help: URL parameters |
| `df` | Time filter (`d`/`w`/`m`/`y`). **Not** on DuckDuckGo's params help page; used by ddgs (§5). Treat as unofficial. | Absence on help page, 2026-10-05 |
| Pagination | Next-page form posts `q`, `s` (offset, `10` on page 1), `nextParams` (empty), `v=l`, `o=json`, `dc` (`12`), `api=d.js`, `vqd` (`4-3182…`), `kl`. | Live page "nav-link" form |

The help page adds a usage condition for these parameters: "These parameters
are intended for individual use. If using them for any other purpose beyond
individual use (e.g. for apps/extensions), please do not remove our branding
(ko, kr params etc.) or advertising (k1, k4 params etc.) as we have contracts
in place that we would be violating if you did so." —
[duckduckgo.com/duckduckgo-help-pages/settings/params/](https://duckduckgo.com/duckduckgo-help-pages/settings/params/)
(fetched 2026-10-05). Implication for likha: do not send `k1=-1` (ads off) or
branding-stripping parameters; filter ads client-side instead.

Response: `200`, `content-type: text/html; charset=UTF-8`, `x-robots-tag:
noindex`, ~37–42 KB for one page of ~10 organic results plus one ad. No
`Set-Cookie` was returned.

### 1.2 `lite.duckduckgo.com/lite/` (table layout)

Same parameters; its forms are `<form action="/lite/" method="post">` and
`<form class="next_form" action="/lite/" method="post">` with the same hidden
fields (`q`, `s`, `nextParams`, `v`, `o`, `dc`, `api`, `vqd`, `kl`). ~26 KB per
page. Markup in §2.5.

### 1.3 Instant Answer API — `api.duckduckgo.com/?q=…&format=json`

DuckDuckGo's own documentation page (`duckduckgo.com/api`) now 302-redirects to
a search for "api" (`location: https://duckduckgo.com/?q=api&&rpl=1`, observed
2026-10-05). The archived page (present at
[2022-09-22](http://web.archive.org/web/20220922150038/http://duckduckgo.com/api),
already a redirect by
[2024-05-14](http://web.archive.org/web/20240514220254/https://duckduckgo.com/api))
states, verbatim from the
[2019-12-07 capture](http://web.archive.org/web/20191207062005/https://duckduckgo.com/api):

- "An API for some of our Instant Answers, not for full search results."
- "This API does not include all of our links, however. That is, it is not a
  full search results API or a way to get DuckDuckGo results into your
  applications beyond our instant answers. Because of the way we generate our
  search results, we unfortunately do not have the rights to fully syndicate
  our results, free or paid."
- Requirements: attribution to DuckDuckGo and the underlying source;
  "Non-commercial use unless you get email approval from us"; "Use a
  descriptive t parameter, i.e. append &t=nameofapp to your requests."
- Parameters: `q`, `format` (`json`/`xml`), `callback`, `pretty`,
  `no_redirect`, `no_html`, `skip_disambig`.
- Fields: `Abstract*`, `Heading`, `Image`, `Answer`/`AnswerType`,
  `Definition*`, `RelatedTopics[]`, `Results[]`, `Type` (A/D/C/N/E),
  `Redirect`. "As this is an instant answer API, most deep queries (non topic
  names) will be blank."

Live probe (`?q=golang&format=json&no_html=1&skip_disambig=1`, 2026-10-05):
`200`, `content-type: application/x-javascript` (not `application/json`),
header `x-duckduckgo-results: 1`. Body: Wikipedia abstract for "Go
(programming language)", `Type: "A"`, 12 `RelatedTopics` whose `FirstURL`s are
DuckDuckGo-internal topic links (`https://duckduckgo.com/Fyne_(software)`), and
`Results: []`. Conclusion: unsuitable as a `web_search` backend; at most an
optional "instant answer" supplement, and only with attribution and `t=`.
`api.duckduckgo.com/robots.txt` is `User-agent: * / Disallow: /`.

## 2. Response format

### 2.1 html endpoint — result items (captured 2026-10-05)

```html
<div id="links" class="results">
  <div class="result results_links results_links_deep result--ad ">     <!-- ad -->
    <div class="links_main links_deep result__body">
      <h2 class="result__title">
        <a rel="nofollow" class="result__a"
           href="https://duckduckgo.com/y.js?ad_domain=udemy.com&amp;ad_provider=bingv7aa&amp;ad_type=txad&amp;click_metadata=…&amp;rut=…&amp;u3=https%3A%2F%2Fwww.bing.com%2Faclick%3F…&amp;vqd=…">Golang Online Course …</a>
        <div class="result__badge-wrap"><button class="badge--ad">Ad</button> …
  …
  <div class="result results_links results_links_deep web-result ">     <!-- organic -->
    <div class="links_main links_deep result__body">
      <h2 class="result__title">
        <a rel="nofollow" class="result__a" href="https://go.dev/">The Go Programming Language</a>
      </h2>
      <div class="result__extras"><div class="result__extras__url">
        <span class="result__icon"><a rel="nofollow" href="https://go.dev/"><img class="result__icon__img" …></a></span>
        <a class="result__url" href="https://go.dev/">go.dev</a>
      </div></div>
      <a class="result__snippet" href="https://go.dev/">Go is an open source programming language …</a>
```

- Container: `div.result` inside `div#links.results`. Organic: also
  `web-result`. Ad: `result--ad` plus a `button.badge--ad` "Ad" badge.
- Title + URL: `a.result__a` (inside `h2.result__title`).
- Display URL: `a.result__url` (text is the bare host/path).
- Snippet: `a.result__snippet` (an anchor, not a div, on this endpoint).
- Instant-answer box above results: `div.zci-wrapper > div.zci` with
  `h1.zci__heading` and `div#zero_click_abstract.zci__result` — not a result;
  ignore (or surface separately).
- HTML comments present in the template: `<!-- If zero click results are
  present -->`, `<!-- Web results are present -->`, `<!-- This is the visible
  part -->` per result. Do not depend on comments.

### 2.2 Redirect wrapping (`/l/?uddg=`)

Whether hrefs are wrapped differed between requests on the same day:

| Request | `result__a` href form |
| --- | --- |
| `GET /html/?q=golang`, UA `likha-web-search` | `//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F&amp;rut=baf47f…` (44 `uddg` occurrences) |
| `POST /html/`, Chrome UA or `likha-web-search` UA | direct `https://go.dev/` (0 `uddg`) |

Unwrap rule (consistent with the observed markup): HTML-entity-decode the
href (`&amp;` → `&`), resolve protocol-relative `//` to `https:`, and if host
is `duckduckgo.com` and path is `/l/`, take the `uddg` query value — which
`net/url`'s `Query()` already percent-decodes once — and discard `rut`
(an opaque tracking/verification hash). Do not decode twice. The ad href
was **also** wrapped on the GET page:
`//duckduckgo.com/l/?uddg=https%3A%2F%2Fduckduckgo.com%2Fy.js%3Fad_domain%3D…`,
so ad detection must run **after** unwrapping (or on the container class).

### 2.3 Ads

- Container class `result--ad`; badge `button.badge--ad`.
- Destination: `https://duckduckgo.com/y.js?ad_domain=<domain>&ad_provider=<e.g. bingv7aa>&ad_type=txad&click_metadata=…&u3=<bing aclick URL>…`.
- Lite: `<tr class="result-sponsored">` rows and the same `y.js` href; the
  "(Sponsored link - more info)" anchor next to it **also has class
  `result-link`** and points to
  `https://duckduckgo.com/duckduckgo-help-pages/company/ads-by-microsoft-on-duckduckgo-private-search/`.
- Robust rule: drop a result if its container has `result--ad`
  (html) / its row has `result-sponsored` (lite), **or** if the (unwrapped)
  target host is `duckduckgo.com` / `*.duckduckgo.com` (covers `y.js`,
  help-page links, internal topic links). No organic result legitimately
  points at duckduckgo.com in practice; dropping them is the safe default.

### 2.4 No-results page (html endpoint)

`POST q="qzxv7kplmwq9fjtz3rr8bnn"` → `200`, 9.3 KB:

```html
<div class="result results_links results_links_deep web-result result--no-result">
  <div class="links_main links_deep result__body">
    <div class="no-results__container result__title">
      <span class='no-results'><div class="no-results__message">
        <h1>No results found for <strong>&quot;qzxv7kplmwq9fjtz3rr8bnn&quot;</strong></h1>
```

Markers: `result--no-result`, `no-results__message`, text `No results found
for`. No `result__a` anchors.

### 2.5 lite endpoint table

Each result is three `<tr>` rows plus a spacer, inside a `<table>`:

```html
<tr><td valign="top">2.&nbsp;</td>
    <td><a rel="nofollow" href="https://go.dev/" class='result-link'>The Go Programming Language</a></td></tr>
<tr><td>&nbsp;&nbsp;&nbsp;</td><td class='result-snippet'>Go is an open source …</td></tr>
<tr><td>&nbsp;&nbsp;&nbsp;</td><td><span class='link-text'>go.dev</span></td></tr>
```

Single-quoted class attributes; `a.result-link`, `td.result-snippet`,
`span.link-text`; `tr.result-sponsored` for ad rows (the numbering counts the
ad as "1."). Snippet is in a following sibling row, so a parser must associate
rows positionally. Slightly smaller than `/html/` but more brittle to parse;
`/html/` has per-result containers, which make ad filtering local.

## 3. Bot detection and rate limiting

### 3.1 What a block looks like (observed)

`GET https://html.duckduckgo.com/html/?q=golang` with a bare Chrome UA, first
request of the session, 2026-10-05 12:50:55 UTC:

- Status **`HTTP/2 202`**, `content-type: text/html; charset=UTF-8`, ~14 KB.
- Body text: "Unfortunately, bots use DuckDuckGo too." / "Please complete the
  following challenge to confirm this search was made by a human." / "Select
  all squares containing a duck:" plus a fallback "Please email the following
  code to: error-lite+4a8a@duckduckgo.com".
- Markers: `class="anomaly-modal__…"` (`anomaly-modal__title`,
  `__puzzle`, `__submit`, …), `<form id="challenge-form"
  action="//duckduckgo.com/anomaly.js?sv=html&cc=sre&…" method="POST">`,
  `id="img-form"`, `challenge-submit`. The lite variant is identical with
  `sv=lite`. No `Retry-After`.
- Same result repeated at 12:55:27 for a different query (2/2). Same
  Chrome UA via **POST** got 200 + results.

Not observed today: 403, 429, or 3xx. The adapter's comment that DuckDuckGo
"sometimes 302s — including back to itself — when it throttles"
(`internal/webtools/duckduckgo.go:46-50`) is **unverified** by these probes;
ddgs treats 202 as its rate-limit signal (§5).

### 3.2 Reliable detection

In order, cheapest first:

1. `status == 202` → blocked/challenged (never a results page in any probe).
2. Any status with body containing `anomaly-modal`, `id="challenge-form"`,
   `anomaly.js`, or `bots use DuckDuckGo too` → blocked.
3. `status == 200` with `result--no-result` / `no-results__message` → genuine
   empty result set (success with 0 hits, distinct message).
4. `status == 200`, none of the above, and zero `result__a` anchors → markup
   changed (current named error is right for this case only).
5. `429`, `403`, other 4xx/5xx, 3xx → explicit status errors (keep).

### 3.3 Backoff

DuckDuckGo publishes nothing about rate limits (no help page found; the Terms
and Acceptable Use Policy have no numeric limits). Practical guidance, all
**inferred**: do not auto-retry a 202 challenge (retrying a challenge is
the "evade security" pattern and tends to extend the block); report it and
let the user/model try later. Serialize DuckDuckGo requests per process with a
minimum spacing (≥1–2 s) — `web_search` is registered `ParallelSafe: true`
(`internal/tools/web_search.go:47`), so a model can fan out several searches at
once, which is exactly what triggers anomaly pages. Cache identical
(query, region) results for the conversation to avoid repeats.

### 3.4 `vqd` tokens

- The html and lite endpoints did **not** need a `vqd` on the first page: the
  probes sent only `q` (plus `b`, `kl` empty on one POST).
- Pages emit a `vqd` (`4-<digits>`) in the "Next" form and inside ad URLs; the
  same value appeared on html and lite pages for the same query.
- The JSON endpoints (`links.duckduckgo.com/d.js` for web, `duckduckgo.com/i.js`
  images, `news.js`, …) used by the main JS SERP require a `vqd` scraped from
  `duckduckgo.com/?q=…`. See §5 for how ddgs handled this historically.
  Page 2+ of the html endpoint: replay the hidden fields of the "Next" form
  (`s`, `dc`, `v`, `o`, `api`, `vqd`, `kl`, `nextParams`) — **unverified**
  whether `vqd` is enforced there. Likha only needs page 1 (limit ≤ 10).

## 4. Terms of use / legality

Stated (verbatim, fetched 2026-10-05):

- [Terms of Service](https://duckduckgo.com/terms) ("Last updated: 01-07-2025"):
  "We expect you to use our Services as authorized, or we may otherwise suspend
  access. In order to have authorization to use our Services: You must comply
  with these Terms and any additional service-specific terms … You must comply
  with the DuckDuckGo Acceptable Use Policy." The Terms contain no clause that
  mentions scraping, bots, crawling, automated access, or an API.
- [Acceptable Use Policy](https://duckduckgo.com/acceptable-use): you agree not
  to "Attempt to access, interfere with, or connect to the services and/or any
  computer without authorization (that is, any form of “hacking”)", "Frame,
  inline link, or similarly display any portion of the services within another
  service", "Sell or resell any portion of the services", "Interfere with or
  disrupt the integrity or performance of the services". "This list is not
  exhaustive. We reserve the right to suspend or terminate access for any
  users who we deem in violation of the spirit of these conditions."
- [URL-parameters help page](https://duckduckgo.com/duckduckgo-help-pages/settings/params/):
  parameters "are intended for individual use"; apps/extensions must not strip
  branding or advertising parameters (quoted in §1.1).
- [Partnerships help page](https://duckduckgo.com/duckduckgo-help-pages/company/partnerships):
  "Please don't include our search results in any sort of frame … We simply
  don't have the rights to allow framing based on how we generate our search
  results."
- [Sources help page](https://duckduckgo.com/duckduckgo-help-pages/results/sources):
  traditional links "we largely source from Bing".
- Instant Answer API (archived): not a full-results API; "we unfortunately do
  not have the rights to fully syndicate our results, free or paid";
  non-commercial unless approved; attribution and `t=` required.
- robots.txt: `html.duckduckgo.com` and `lite.duckduckgo.com` both say
  `User-agent: * Allow: /` with the comment "Ensure all paths are crawled so
  their noindex tags/headers are respected"; `duckduckgo.com/robots.txt`
  disallows `/lite` and `/html` on the main host; `api.duckduckgo.com` disallows
  everything. robots.txt governs crawlers, not a per-user tool, and grants no
  licence.

Inferred (not stated anywhere found): DuckDuckGo does not explicitly prohibit
or permit a user's agent fetching the HTML page on the user's behalf. The
"rights to syndicate" statement and the AUP's "interfere … performance" and
"spirit of these conditions" language make heavy or commercial automated use
risky; low-volume, user-initiated, consented, non-evasive requests are the
defensible shape. Defeating the challenge (CAPTCHA solving, TLS-fingerprint
impersonation, IP rotation) would move toward "access … without
authorization". The spec's "gray zone" wording is accurate; the
spec/consent copy can cite the AUP and the syndication statement directly.

## 5. How other open-source projects do it

Pinned commits (all read from source on 2026-10-05):

- **ddgs** — `deedy5/ddgs` (the old `deedy5/duckduckgo_search` repo resolves
  here) @ [`70a5635`](https://github.com/deedy5/ddgs/tree/70a5635510fb8d5b15d5ba6ceced6a67e212149b) (HEAD 2026-08-27); tags v8.1.1 @ `3e0e023`, v6.4.2 @ `efe15e4`.
- **smolagents** @ `c30b115`; **langchain-community** @ `f425a3e`.
- **opencode** (`sst/opencode` → `anomalyco/opencode`) @ `907b3bc`; **crush** @ `8da3490`; **aider** @ `5dc9490`; **continue** @ `5522c6f`.
- Go: **langchaingo** @ `039fbb6`, **picoclaw** @ `bbf6893`, **eino-ext** @ `3603a39`.

### 5.1 ddgs (Python; the de facto reference)

- Current DuckDuckGo engine
  ([`ddgs/engines/duckduckgo.py#L10-L42`](https://github.com/deedy5/ddgs/blob/70a5635510fb8d5b15d5ba6ceced6a67e212149b/ddgs/engines/duckduckgo.py#L10-L42)):
  `search_url = "https://html.duckduckgo.com/html/"`,
  `search_method = "POST"`; payload `{"q": query, "b": "", "l": region}`, `s =
  10 + (page-2)*15` for page > 1, `df = timelimit`; no `vqd`, no `kl`.
  Selectors: items `//div[contains(@class, 'body')]` (i.e. `result__body`),
  title `.//h2//text()`, href `./a/@href` (the direct-child anchor, which is
  `result__snippet`), body `./a//text()`. Ads dropped by
  `href.startswith("https://duckduckgo.com/y.js?")` (L40-42). **No `uddg`
  unwrapping anywhere** — consistent with POST returning direct hrefs (§7).
- `provider = "bing"`; `backend="auto"` is a shuffled metasearch that skips
  engines whose provider already answered
  ([`ddgs/ddgs.py#L88-L98`](https://github.com/deedy5/ddgs/blob/70a5635510fb8d5b15d5ba6ceced6a67e212149b/ddgs/ddgs.py#L88-L98),
  [`#L184-L204`](https://github.com/deedy5/ddgs/blob/70a5635510fb8d5b15d5ba6ceced6a67e212149b/ddgs/ddgs.py#L184-L204)),
  so "DuckDuckGo search" via ddgs often is not DuckDuckGo. The lite backend no
  longer exists.
- Client: `primp.Client(impersonate="random", impersonate_os="random", …)` —
  browser TLS/HTTP2 fingerprint impersonation
  ([`ddgs/http_client.py#L53-L60`](https://github.com/deedy5/ddgs/blob/70a5635510fb8d5b15d5ba6ceced6a67e212149b/ddgs/http_client.py#L53-L60));
  the engine sets no headers of its own. Any non-200 returns `None` silently
  ([`ddgs/base.py#L65-L70`](https://github.com/deedy5/ddgs/blob/70a5635510fb8d5b15d5ba6ceced6a67e212149b/ddgs/base.py#L65-L70));
  `RatelimitException` is defined but never raised; no retry/backoff.
- History worth knowing: the DuckDuckGo engine was disabled on 2025-07-20
  ("disable duckduckgo until ratelimit is fixed", commit `6b5aec3`) and
  re-enabled in `6fd63d9`; v8.1.0/8.1.1 hard-disabled html and lite
  (`backends = ["bing"]  # temporaly disable html and lite backends`,
  [v8.1.1 `duckduckgo_search.py#L180-L182`](https://github.com/deedy5/ddgs/blob/3e0e023abdd5313bcdba718609cf4b7585654b2e/duckduckgo_search/duckduckgo_search.py#L180-L182)).
  v8.1.1's html backend sent `Referer: https://html.duckduckgo.com/` and
  `Sec-Fetch-User: ?1`, POSTed `q`, `b=""`, `kl`, `df`, stopped on `No  results.`,
  skipped `https://duckduckgo.com/y.js?ad_domain` hrefs, paginated by replaying
  the last `div.nav-link` form's hidden inputs, slept 0.75 s between close
  requests, and raised `RatelimitException` on status 202, 301, 403, 400, 429, 418
  ([v8.1.1 `#L99-L139`, `#L200-L273`](https://github.com/deedy5/ddgs/blob/3e0e023abdd5313bcdba718609cf4b7585654b2e/duckduckgo_search/duckduckgo_search.py#L99-L139)).
  v6.4.2 defaulted to the JSON `links.duckduckgo.com/d.js` endpoint with a
  `vqd` scraped from `duckduckgo.com`, and treated 202/301/403 as rate limits
  ([v6.4.2 `#L135-L136`, `#L253-L335`](https://github.com/deedy5/ddgs/blob/efe15e473592904c152ef7ca5b3e2edd2f374241/duckduckgo_search/duckduckgo_search.py#L253-L335)).
  Current `vqd` use is limited to images/news/videos (`i.js`, `news.js`,
  `v.js`), extracted from `GET https://duckduckgo.com?q=…`
  ([`ddgs/engines/duckduckgo_images.py#L19-L45`](https://github.com/deedy5/ddgs/blob/70a5635510fb8d5b15d5ba6ceced6a67e212149b/ddgs/engines/duckduckgo_images.py#L19-L45)).

### 5.2 smolagents and LangChain

- smolagents `DuckDuckGoSearchTool` wraps `ddgs` (`DDGS().text(query,
  max_results=…)`, no backend → ddgs metasearch) with a client-side
  `rate_limit=1.0` qps sleep; empty → `Exception("No results found! …")`
  ([`default_tools.py#L104-L159`](https://github.com/huggingface/smolagents/blob/c30b115286e000e98711fae5e85993547b73d826/src/smolagents/default_tools.py#L104-L159)).
  Its separate `WebSearchTool(engine="duckduckgo")` does
  `requests.get("https://lite.duckduckgo.com/lite/", params={"q": query},
  headers={"User-Agent": "Mozilla/5.0"})`, parses `a.result-link`,
  `td.result-snippet`, and builds the link as `"https://" +` the
  `span.link-text` display text (so it never sees `uddg`, but also loses the
  path for long URLs)
  ([`#L374-L431`](https://github.com/huggingface/smolagents/blob/c30b115286e000e98711fae5e85993547b73d826/src/smolagents/default_tools.py#L374-L431)).
- LangChain `DuckDuckGoSearchAPIWrapper` (now in `langchain-community`) wraps
  `ddgs` with defaults `region="wt-wt"`, `safesearch="moderate"`, `time="y"`,
  `max_results=5`, `backend="auto"`; its documented `html`/`lite` backend
  names are stale
  ([`utilities/duckduckgo_search.py#L12-L178`](https://github.com/langchain-ai/langchain-community/blob/f425a3ed1933173fb3694b81359d1519c4f82d36/libs/community/langchain_community/utilities/duckduckgo_search.py#L12-L178)).
  No rate-limit handling.

### 5.3 Coding agents

| Agent | Web search? | Backing |
| --- | --- | --- |
| opencode | `websearch` tool | Hosted MCP: `https://mcp.exa.ai/mcp` or `https://search.parallel.ai/mcp`, A/B by session; gated to opencode providers or env flags ([`tool/mcp-websearch.ts#L4-L7`](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/tool/mcp-websearch.ts#L4-L7), [`tool/websearch.ts#L30-L97`](https://github.com/anomalyco/opencode/blob/907b3bc518fa48e90e8ec24dd327d13eee71c36c/packages/opencode/src/tool/websearch.ts#L30-L97)). No DuckDuckGo. |
| crush | `web_search` (sub-agent tool under `agentic_fetch`) | **DuckDuckGo Lite scraping**, details below. |
| aider | none | `/web <url>` scrape only ([`commands.py#L219-L249`](https://github.com/Aider-AI/aider/blob/5dc9490bb35f9729ef2c95d00a19ccd30c26339c/aider/commands.py#L219-L249)). |
| continue | `search_web` | `POST` JSON `{query, n}` to Continue's own proxy `https://proxy-server-blue-l6vsfbzhba-uw.a.run.app/web` ([`WebContextProvider.ts#L9-L36`](https://github.com/continuedev/continue/blob/5522c6f44ca0ac3528b37244818fbfa39b5af470/core/context/providers/WebContextProvider.ts#L9-L36)); upstream engine unknown. |

**crush** (`internal/agent/tools/search.go` @ `8da3490`, re-read for this note):

- `GET https://lite.duckduckgo.com/lite/?q=` + `url.QueryEscape(query)`; no
  `kl`/`df`
  ([`#L66-L82`](https://github.com/charmbracelet/crush/blob/8da349060b7df148d209979be0a5e9c9281d1f15/internal/agent/tools/search.go#L66-L82)).
- Randomized browser headers per request: UA from 11 desktop browser strings,
  `Accept-Language` from 5, `Sec-Fetch-*`, `Upgrade-Insecure-Requests: 1`,
  `Accept-Encoding: identity`, `DNT: 1` half the time
  ([`#L27-L47`, `#L115-L130`](https://github.com/charmbracelet/crush/blob/8da349060b7df148d209979be0a5e9c9281d1f15/internal/agent/tools/search.go#L115-L130)).
- Block detection: `if resp.StatusCode == http.StatusAccepted { return nil,
  errSearchRateLimited }`, plus body markers `"anomaly-modal"`,
  `"/anomaly.js"`, `"Unfortunately, bots use DuckDuckGo too"` on a 200 body
  (its comment says Lite can serve the challenge with HTTP 200); error text
  "DuckDuckGo is rate-limiting this machine. Do not retry or rephrase; wait a
  few minutes or fetch known URLs directly"
  ([`#L49-L111`](https://github.com/charmbracelet/crush/blob/8da349060b7df148d209979be0a5e9c9281d1f15/internal/agent/tools/search.go#L49-L111));
  fixture `internal/agent/tools/testdata/ddg_anomaly_202.html`.
- Parser: `golang.org/x/net/html` tree walk; `a.result-link` (title/href),
  `td.result-snippet`; whitespace-token class match
  ([`#L132-L191`](https://github.com/charmbracelet/crush/blob/8da349060b7df148d209979be0a5e9c9281d1f15/internal/agent/tools/search.go#L132-L191)).
  No sponsored-row filter in the Lite parser — by §2.3, it can return the
  `y.js` ad and the "more info" help link as results (inference from today's
  markup; not run).
- Unwrap: only if href starts with `//duckduckgo.com/l/?uddg=`; cut after
  `uddg=`, truncate at first `&` (drops `rut`), `url.QueryUnescape`
  ([`#L208-L221`](https://github.com/charmbracelet/crush/blob/8da349060b7df148d209979be0a5e9c9281d1f15/internal/agent/tools/search.go#L208-L221)).
- Pacing: global mutex with a random 500–2000 ms minimum gap between searches
  ([`#L238-L254`](https://github.com/charmbracelet/crush/blob/8da349060b7df148d209979be0a5e9c9281d1f15/internal/agent/tools/search.go#L238-L254)).
  Body is read with an unbounded `io.ReadAll`.
- History: the first version (`db22f2f`, PR #1565) POSTed
  `https://html.duckduckgo.com/html` with `q`, `b=""`, `kl=""` and
  `Content-Type: application/x-www-form-urlencoded`, parsed `div.result` →
  `a.result__a` / `a.result__snippet`, and dropped `y.js` links
  ([`db22f2f search.go#L29-L80`](https://github.com/charmbracelet/crush/blob/db22f2f0a9dd1d340edd4546bb807e1d99524c59/internal/agent/tools/search.go#L29-L80));
  switched to Lite GET in `ab6d971` (#1779, 2026-01-15, "try to make the search
  tool more reliable"); 202/anomaly detection added in `de67123` (2026-07-31).

### 5.4 Other Go implementations

- **cloudwego/eino-ext** `components/tool/duckduckgo/v2` (ported from
  ddgs/searxng): `POST https://html.duckduckgo.com/html/`, form `q`, `b=""`,
  `kl`, `df`; headers `Referer: https://html.duckduckgo.com/`,
  `Sec-Fetch-Site: same-origin`, `Sec-Fetch-Dest: document`,
  `Sec-Fetch-Mode: navigate`, `Sec-Fetch-User: ?1`,
  `Content-Type: application/x-www-form-urlencoded`, random UA (`uarand`);
  goquery `div#links div.web-result` → `h2.result__title > a`,
  `a.result__snippet`; skips `https://duckduckgo.com/y.js?ad_domain`; dedups by
  href; paginates by re-posting the last form's hidden inputs; sleeps 3 s
  between pages ("request too fast may cause 202"); no uddg unwrap; any non-200
  is a generic error
  ([`text_search.go#L93-L221`](https://github.com/cloudwego/eino-ext/blob/3603a39473c3e7b2aa3bfc11216487c94b8c7fd9/components/tool/duckduckgo/v2/text_search.go#L93-L221)).
- **tmc/langchaingo** `tools/duckduckgo`: `GET html/?q=`, caller UA; goquery
  `.web-result` → `.result__a`, `.result__snippet`; unwraps by attribute
  *position* (`Attr[2].Val`) and `TrimPrefix("/l/?kh=-1&uddg=")` — which does
  not match the `//duckduckgo.com/l/?uddg=` form seen today, and keeps `&rut=`;
  no 202/ad handling
  ([`internal/client.go#L51-L121`](https://github.com/tmc/langchaingo/blob/039fbb6c6469a8ffcdae615ec7bdc465c83abadc/tools/duckduckgo/internal/client.go#L51-L121)).
- **sipeed/picoclaw**: `GET html/?q=…[&df=]` with a fixed Chrome 120 UA; regex
  parsing (`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"…`);
  snippets paired by index; unwrap leaves `&rut=` attached; no status check
  for DDG, no ad or anomaly handling
  ([`pkg/tools/integration/web.go#L50-L56`, `#L977-L1066`](https://github.com/sipeed/picoclaw/blob/bbf6893ca7afad27f1d00a0f5a45982a549c6ed6/pkg/tools/integration/web.go#L977-L1066)).

Patterns across implementations: everyone treats 202 as "blocked"
(crush, ddgs ≤ v8, eino-ext comment); every html-endpoint implementation that
filters ads does it by `y.js` href prefix; the hardened ones (ddgs, crush,
eino-ext) evade detection with browser impersonation, which likha's spec
rules out ("no browser impersonation" in `duckduckgo.go:9-10`).

## 6. Go implementation guidance

- **Dependencies.** `go.mod` (module `likha`, `go 1.24.0`) has neither
  `golang.org/x/net` nor `github.com/PuerkitoBio/goquery`; it has
  `golang.org/x/text v0.3.8`. Go's stdlib has no HTML tokenizer
  (`html` only escapes/unescapes). Options: (a) keep the stdlib token scan
  already in `duckduckgo.go` and make it container-aware (track the enclosing
  `div.result` class list); (b) add `golang.org/x/net/html` (official Go
  sub-repo; `html.NewTokenizer` streaming or `html.Parse` tree) — a small,
  well-understood dependency that matches browser parsing; (c) goquery adds
  cascadia + x/net and CSS selectors — more than needed. Recommended: (b) if a
  new dependency is acceptable, else (a). With x/net, a tokenizer loop that
  tracks a stack of open `div` class lists is enough: start a result on
  `div.result`, mark it ad on `result--ad`, capture `a.result__a`
  href/text and `a.result__snippet` text, emit on the container's end tag.
- **HTTP.** One `http.Client` with `CheckRedirect` returning
  `http.ErrUseLastResponse` (already done). Set
  `Transport` explicitly (clone of `http.DefaultTransport` with
  `ResponseHeaderTimeout` ~10 s, `TLSHandshakeTimeout` 10 s) so a hung server
  cannot hold the full 30 s. Go's transport adds `Accept-Encoding: gzip` and
  transparently decompresses when the caller did not set it — keep it that way
  so the `io.LimitReader` cap applies to decompressed bytes. The default
  transport honours `HTTPS_PROXY`; decide explicitly whether search should
  (the spec's proxy exclusion is written for fetch).
- **POST form.** `http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
  strings.NewReader(url.Values{"q": {q}, "b": {""}, "kl": {region}}.Encode()))`
  with `Content-Type: application/x-www-form-urlencoded`.
- **Cancellation.** `NewRequestWithContext` covers DNS/connect/headers/body
  read. When the internal 30 s timeout fires, the outer `ctx.Err()` is nil, so
  check the derived context too, to report "timed out after 30s" rather than a
  raw `context deadline exceeded` string.
- **Body bound.** Pages are 9–42 KB; 4 MiB (`maxSearchResponseBytes`) is
  generous. A 512 KiB cap would still be 12× the largest page observed.
  Read `limit+1` bytes and error on overflow (already done).
- **Charset.** All observed pages declare `charset=UTF-8`; validate with
  `utf8.ValidString` and replace invalid sequences rather than trusting it.

## 7. Live probe log (2026-10-05, one IP, curl 8.x, HTTP/2)

"Chrome UA" = `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)
AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36` with no
other browser headers. Nine search/API requests total, ≥4 s apart. Times
marked ≈ are derived from the command start plus its sleeps.

| UTC | Request | Status | Observed |
| --- | --- | --- | --- |
| 12:50:55 | `GET html/?q=golang`, Chrome UA | **202** | anomaly challenge page, 14 KB |
| 12:51:07 | `POST html/` `q=golang&b=&kl=`, Chrome UA, `Referer: https://html.duckduckgo.com/`, form content type | 200 | 37 KB; 11 `result__a` (1 ad + 10 organic); direct hrefs; `vqd` in Next form |
| ≈12:51:12 | `GET lite/?q=golang`, Chrome UA | **202** | anomaly challenge (`sv=lite`) |
| 12:51:38 | `POST lite/` `q=golang`, Chrome UA, Referer | 200 | 26 KB; 12 `result-link` (incl. ad and "more info"); 3 `result-sponsored` rows |
| 12:51:43 | `GET api.duckduckgo.com/?q=golang&format=json&no_html=1&skip_disambig=1` | 200 | `application/x-javascript`; abstract + 12 RelatedTopics; `Results: []` |
| 12:52:03 | `GET html/?q=golang`, UA `likha-web-search`, `Accept: text/html` (likha's exact request) | 200 | 42 KB; results; hrefs `//duckduckgo.com/l/?uddg=…&rut=…`; ad wrapped too |
| ≈12:52:09 | `POST html/` `q="qzxv7kplmwq9fjtz3rr8bnn"`, Chrome UA | 200 | no-results page (§2.4) |
| 12:55:27 | `GET html/?q=golang+generics`, Chrome UA | **202** | anomaly challenge |
| ≈12:55:37 | `POST html/` `q=golang+generics`, UA `likha-web-search` | 200 | 11 `result__a`, direct hrefs |

Not tested: sustained request rates, other IPs/regions, `kl`/`df`/`kd`
effects, page 2. Hypothesis (unverified): the GET block keys on a client that
claims to be Chrome without Chrome's other headers/TLS fingerprint; an honest
non-browser UA was not challenged. One IP and nine requests cannot establish
block thresholds.

Likha's own parser (`ddgParseResults`, run unmodified on copies of the
captured pages in a scratch module) returned for `limit=5`:

- `GET` page (likha UA): 6 hits, **#1 = `https://duckduckgo.com/y.js?ad_domain=udemy.com&ad_provider=bingv7aa…`** (ad, unwrapped from `uddg`), then go.dev, go.dev/doc/install, github.com/golang/go, Wikipedia.
- `POST` page: same, ad first.
- No-results page and 202 challenge page: 0 anchors (indistinguishable).

## 8. Recommendations for likha

### 8.1 Endpoint and request shape

Primary: **`https://html.duckduckgo.com/html/`**. The endpoint is not the
question. The question is GET vs POST:

- Spec today (`spec.md`, Search contract) fixes `GET …/html/?q=`. Likha's exact
  GET (UA `likha-web-search`, `Accept: text/html`) returned results in the one
  probe; a GET claiming to be Chrome was challenged 2/2.
- `POST` with a form body is what DuckDuckGo's own page submits (`<form
  action="/html/" method="post">`), what ddgs, eino-ext and early crush use,
  and returned results 3/3 including with likha's UA. It also yields direct
  hrefs (no `uddg` to unwrap).
- Recommendation: **switch to POST** (needs a spec revision and user
  approval), keep the honest UA, and keep "no fallback between methods":

```
POST https://html.duckduckgo.com/html/
Content-Type: application/x-www-form-urlencoded
Accept: text/html
User-Agent: likha-web-search            (honest; consider "likha/<version> (+repo URL)")
Referer: https://html.duckduckgo.com/   (optional; matches the site's own form)

q=<query>&b=&kl=<region or omit>
```

Do not send `k1`/branding-stripping params (help-page condition). Do not use
lite as primary: positional row parsing, ad "more info" links sharing
`result-link`, and the Chrome-UA GET was challenged. Do not use the Instant
Answer API for `web_search`: no web results, non-commercial terms.

### 8.2 Parsing

1. Iterate `div` elements whose class list contains `result` inside
   `div#links`.
2. Skip the container if its class list has `result--ad` or
   `result--no-result`.
3. Title + href: first `a.result__a` in the container (`h2.result__title > a`).
4. Snippet: `a.result__snippet` text if present; never invent.
5. Unwrap (§2.2), then **drop** any target whose host is `duckduckgo.com` or a
   subdomain (catches `y.js` ads even if the container class changes, and
   `uddg`-wrapped ads).
6. Count organic results only toward `limit`.

### 8.3 Status classification and messages

| Condition | Result | Suggested model-facing message |
| --- | --- | --- |
| 202, or body has `anomaly-modal` / `challenge-form` / `anomaly.js` / `bots use DuckDuckGo too` | error, no retry | "DuckDuckGo blocked this automated search with a bot challenge (HTTP 202). Do not retry or rephrase now; try again in a few minutes or ask the user to configure a keyed backend." |
| 200 + `result--no-result` / `no-results__message` | success, 0 hits | "DuckDuckGo found no results for this query." |
| 200, no result containers, no markers | error (current named error) | keep "duckduckgo returned no parsable results; the HTML endpoint may have changed" — drop "or blocked" once blocks are detected separately |
| 429 / 403 / 3xx / other | error | keep current messages; 403 can say "refused (HTTP 403)" |

Pace requests: serialize DuckDuckGo calls with a ≥1 s minimum gap (crush uses
0.5–2 s random), or mark the DuckDuckGo-backed tool not parallel-safe.

### 8.4 Gaps and bugs in the current draft

> **Status, 2026-10-05:** items 1–8 and 11 were addressed in `internal/webtools/duckduckgo.go` (POST request, container-scoped parsing, ad/duckduckgo.com filtering, 202/challenge → `ErrDuckDuckGoBlocked`, no-results success, one-at-a-time pacing with a 1 s gap, named timeout) with fixture tests in `internal/webtools/duckduckgo_test.go`. Item 9 (proxy/transport) and the `kl` region (item 6) are unchanged. The text below describes the draft as it was before that change.

1. **Ads returned as results (bug, reproduced).**
   `internal/webtools/duckduckgo.go:249-277` accepts every `result__a`;
   `ddgResultURL` (`:306-336`) keeps `https://duckduckgo.com/y.js?ad_domain=…`
   because it is a valid http(s) URL (`:332-333`), and also after unwrapping a
   `uddg`-wrapped ad (`:322-329`). On both captured pages, hit #1 is the ad, so
   `limit=5` yields 4 organic results. Fix per §8.2 steps 2 and 5.
2. **202 bot challenge not recognized.** `:151-168` sends 202 to the default
   branch: "duckduckgo web search returned HTTP 202; retry later or verify
   availability". That invites retries and does not say "blocked". There is
   no body-marker check for a 200-status challenge either (crush reports Lite
   can do this).
3. **Genuine no-results reported as breakage.** `:190-196` returns "no
   parsable results; the HTML endpoint may have changed or blocked this
   request" for the real "No results found" page (§2.4). Detect
   `result--no-result` first.
4. **Comment claims not supported by probes.** `:46-50` and `:80-82` say
   DuckDuckGo throttles with 302 "back to itself"; observed blocking was 202
   only. `:11-13` and `:99-101` say links are "usually" `/l/?uddg=` wrapped;
   true for likha's GET, false for POST. Keep the 3xx guard, fix the wording.
5. **No pacing for parallel calls.** `internal/tools/web_search.go:47` sets
   `ParallelSafe: true`, and `duckduckgoClient` (`:51-55`) has no
   serialization. Parallel bursts are the classic anomaly trigger.
6. **Region not sent.** No `kl` (`:127-129`); the page defaults to `wt-wt`.
   Optional, but it is the only documented lever for result locale.
7. **Snippet association is document-order, not per container.**
   `:278-290` attaches the next `result__snippet` to the last `result__a`.
   It is correct on today's markup, but a result without a snippet followed
   by an ad's snippet would mis-attach. Container-scoped parsing (§8.2)
   removes this class of error.
8. **Timeout error wording.** `:137-148`: when the internal 30 s cap fires,
   the outer `ctx.Err()` is nil, so the error is the generic "request failed:
   … context deadline exceeded" rather than a named timeout.
9. **Transport.** `duckduckgoClient` uses `http.DefaultTransport`: inherits
   `HTTPS_PROXY` and has no `ResponseHeaderTimeout`. Decide on purpose
   (spec's proxy exclusion currently covers only fetch).
10. **Spec wording.** `spec.md` says DuckDuckGo use "sits in a gray zone of
    DuckDuckGo's terms". This could cite the actual texts: the AUP
    ("interfere with or disrupt the integrity or performance of the
    services"; "spirit of these conditions"), the params page ("intended for
    individual use"), and the archived API page ("do not have the rights to
    fully syndicate our results").
11. **No fixtures.** `internal/webtools` has no tests. The pages captured for
    this note (results with ad, no-results, 202 challenge) are the obvious
    golden inputs if tests are ever added. Per `tasks.md`, none are added in
    this milestone.
