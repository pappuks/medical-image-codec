# PACS Demo — Access Control Design

How the public PACS web-viewer demo keeps bots and scripts out of the 17 GB
dataset. Companion to
[pacs-lambda-service-design.md](pacs-lambda-service-design.md), which describes
the hosting stack this attaches to.

Status: **design, not built.** Tracked in
[pacs-demo-roadmap.md](pacs-demo-roadmap.md).

---

## 1. The requirement, and what it rules out

**The only goal is to stop automated clients from pulling the dataset.** Not
confidentiality, and not knowing who the visitors are.

Both exclusions are deliberate and both simplify the design enormously:

*Confidentiality is not a goal* because there is nothing to protect. The corpus
is public-domain and CC-BY imaging that anyone can already download from NCI
IDC. A gate that stopped humans would inconvenience the audience without
protecting anything.

*Identity is not a goal.* No sign-up, no email, no tracking.

Together these delete the entire authentication layer that earlier drafts of
this document proposed — no login page, no CloudFront signed cookies or key
group, no auth Lambda, no DynamoDB, no SES, no Cognito. The demo stays open to
every human who visits, and the enforcement is a single WAF rule.

The identity options are preserved in the appendix in case that requirement ever
changes.

---

## 2. The mechanism: AWS WAF Challenge

The `Challenge` rule action makes CloudFront serve a silent JavaScript
interrogation before it serves content. A real browser runs the script, receives
a token cookie, and never sees anything. A client that cannot execute JavaScript
— `curl`, `wget`, `requests`, `httpx`, and every trivial scraper — cannot obtain
a token and is refused.

That is precisely the threat, and it costs zero application code. It is a rule
in `infra/waf.yaml` and nothing else.

Mechanically, on a request with a missing, invalid, or expired token, AWS WAF
stops evaluating and returns:

- HTTP status **`202 Request Accepted`**
- the header **`x-amzn-waf-action: challenge`**
- a JavaScript interstitial in the body **only if** the request carried
  `Accept: text/html`

When the interstitial runs, it initializes the token and then *resubmits the
original request* with it, so for a page navigation the whole exchange is
invisible.

**Immunity time** — how long a solved token stays valid — defaults to 300
seconds, has a floor of 300 for Challenge, and a ceiling of **259,200 seconds
(72 hours)**. Set it to the maximum. A longer window means fewer challenge
events, lower cost, and far fewer opportunities for the expiry problem in §4.
The usual argument for short immunity is limiting token replay, which does not
apply when the protected content is public anyway.

Billing is per terminating challenge, so legitimate visitors who solve once and
browse for hours cost almost nothing.

---

## 3. Where to apply it — minting order matters

Apply the Challenge rule to **all paths**, including the dashboard HTML.

This is the one subtle part. Only requests carrying `Accept: text/html` get the
interstitial that *mints* a token; a bare `fetch()` for a `.mic` blob sends
`Accept: */*` and would receive a naked 202 with no way to solve anything. So
the token has to already exist before the first data fetch, and the only request
that can create it is the document load.

Challenging the document request handles this naturally: the browser loads
`/pacs-dashboard.html`, solves the challenge, gets the token, and every
subsequent `/data/*` and `/api/*` request rides along on the same cookie.
Challenging only `/data/*` would break the viewer for everyone.

---

## 4. The failure mode this application must handle

A token that expires mid-session turns `/data/*` fetches into **202 responses
with empty bodies**. Left unhandled, the dashboard would feed empty buffers into
the decoders and report checksum mismatches or decode failures — indistinguishable,
on screen, from a codec bug.

That is unacceptable here specifically. This tool's entire output is correctness
and throughput claims about the codec. A infrastructure hiccup that presents as
"MIC produced wrong pixels" is worse than an outage.

The fix is small and belongs in one place — the shared fetch helper:

```js
// A challenged request is unambiguous: 202 + the WAF action header.
if (resp.status === 202 && resp.headers.get('x-amzn-waf-action') === 'challenge') {
  throw new ChallengeExpired();   // never reaches a decoder
}
```

This detection works **because the whole demo is one origin**. AWS notes that
challenge responses carry no CORS headers, so `x-amzn-waf-action` is unreadable
cross-domain — but the single-distribution architecture that already solved the
cross-origin-isolation problem (see the hosting design) makes every one of these
requests same-origin, so JavaScript can read the header directly. No guessing
from body length.

On catching `ChallengeExpired`, the dashboard should abort the run, show
"re-verifying, one moment," and reload the page — a document navigation, which
gets the interstitial and re-mints the token. Benchmark results from a partial
run must be discarded rather than displayed.

---

## 5. Cross-origin isolation — verify at first deploy

The dashboard is served with `Cross-Origin-Embedder-Policy: require-corp`
because it needs `SharedArrayBuffer` for the PICS Web Workers and the WASM
decoders. Under that header, any cross-origin subresource without a `CORP`
header is blocked.

**Unknown, and to be checked on the first deploy: whether the challenge
interstitial's script is inline or loaded from an AWS-hosted domain.** If it is
cross-origin and does not send CORP, the interstitial will fail to run on the
dashboard page, and the site will appear broken to every visitor.

The fallback, if that happens, is a small `/bootstrap.html` served through a
second Response Headers Policy that omits the COEP header. The bootstrap page
gets challenged, mints the token, and redirects to the dashboard. Tokens are
scoped to the origin, so the dashboard inherits it. This is the same "the login
path doesn't need isolation headers" trick from earlier drafts, repurposed.

Build the second Response Headers Policy into the template from the start; it
costs nothing and converts a potential launch-day emergency into a one-line
behavior change.

---

## 6. What Challenge does not stop

**Headless browsers.** Playwright, Puppeteer, and Selenium drive a real
JavaScript engine, so they solve the challenge and get through. Anyone willing
to script headless Chromium can still mirror the corpus.

This is worth accepting rather than escalating. The realistic threats are a
crawler that wanders in and a casual `wget -r`, and both are stopped cold.
Defeating a determined scraper would require CAPTCHA (friction for every human
visitor) or a login (identity, explicitly not wanted) — to protect data that is
freely downloadable from NCI IDC. The cost/benefit does not support it.

The remaining exposure is bounded by three cheap layers, all of which should be
in place regardless:

- **Tighten the WAF rate rule.** The current 2000 requests per 5 minutes per IP
  was sized for anonymous browsing. A single viewer session loading a large CT
  series legitimately needs several hundred, so there is room to lower it.
- **Keep the egress budget alarm.** The 50 GB / 5 min tripwire is the backstop
  against any mirroring that does get through.
- **Add a `robots.txt`.** Well-behaved crawlers are the highest-volume
  automated traffic on the open web and they leave voluntarily. It is two lines
  and it works before any request costs money.

---

## 7. Optional: shrink the prize

Independent of the above, and still worth considering: publish a curated subset
— eight or so studies spanning the modality range, one to two GB — as the demo
corpus, and keep the full 17 GB in the bucket unlinked.

The demo's argument is about codec behaviour per image, not corpus size, so
visitors lose nothing, and the worst-case egress bill drops by an order of
magnitude. This is a cost decision, not a security one.

---

## 8. Interaction with the existing Playwright suite

`web/` ships a headless CI runner that drives the dashboard and asserts
pixel-correctness. Today it runs against the local `python3 serve.py`, so
Challenge does not affect it.

If it is ever pointed at the deployed CloudFront URL, it will be challenged.
Headless Chromium will *probably* solve it, but "probably" is a poor foundation
for a correctness gate — a flaky CI failure that actually means "the bot
mitigation fired" would waste real debugging time. Keep the suite pointed at the
local server, or add an explicit WAF rule exempting the CI source by IP or a
shared header.

---

## 9. Infrastructure changes

Small, and confined to two files.

`infra/waf.yaml` gains one rule with `Action: Challenge`, a statement matching
all requests, a priority after the existing managed rule groups, and an
`ImmunityTimeProperty` of 259200. The existing rate-based rule gets a lower
limit.

`infra/template.yaml` gains the second Response Headers Policy without COEP (for
the §5 fallback) and a `robots.txt` in the app bucket. Nothing else changes —
no new Lambda, no new bucket, no key group, no table.

---

## Appendix: if identity is ever needed

Should the requirement change — gated early access, a signup list, per-user
quotas — the enforcement mechanism to reach for is **CloudFront signed cookies**
with a trusted key group, not a JWT check at the edge. CloudFront validates
signed cookies natively, before cache lookup, with no function invocation and no
added latency. That matters unusually much here, because the dashboard displays
fetch and decode timings, and a Lambda@Edge check on every blob request would
land directly in the numbers the demo exists to show. CloudFront holds only the
RSA public key; the private key stays in Secrets Manager.

Credential options, cheapest first:

- **Access codes** — a code per audience (paper reviewers, a talk, a post), each
  with a label, expiry, and max-use count, checked by a small Lambda that issues
  the cookies. Coarse attribution and per-channel revocation without any PII.
- **Social sign-in** (Google, GitHub) — self-serve, yields a verified email
  address, and sends no mail. Needs a Cognito user pool plus a Lambda that
  trades the JWT for signed cookies.
- **Email one-time codes** — the heaviest option. Note that Cognito's email OTP
  is *not* a way to avoid Amazon SES: the Cognito documentation requires
  "Essentials feature plan or higher **and** Amazon SES email configuration" for
  OTP, and SES must be moved out of its sandbox before it can mail strangers.
  Cognito's free built-in sender covers only signup and password-reset messages,
  at roughly 50 per day. A third-party sender (Resend, Postmark) skips the
  sandbox entirely.

Prefer a typed code over a magic link in any of these: corporate mail scanners
follow links in email and would silently burn single-use tokens before the
recipient clicks.
