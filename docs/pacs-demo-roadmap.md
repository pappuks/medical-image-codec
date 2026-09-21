# PACS Web Viewer — Demo Roadmap & TODO

Tracks the work to turn the MIC codec into a public, online PACS web-viewer
demo backed by the S3 dataset. Companion to the design docs it links.

Status legend: ✅ done · 🚧 in progress · 📋 todo · 🐛 bug · ⚠️ risk

## Where we are

The end-to-end **dataset pipeline is built and live**: DICOM ingest → lossless
/ license verification → all-codec encoding → S3 upload. Bucket
`mic-studies-594551578651-us-west-1-an` (us-west-1, private) holds **17.2 GB /
17,274 objects**, with **67 of 80 studies fully benchmark-ready** (MIC + PICS +
HTJ2K + JPEG-LS + JPEG-XL, every artifact roundtrip-verified).

The **AWS hosting infrastructure is implemented** in `infra/` — SAM template
(CloudFront + OAC + Lambda + WAF), the thin `/api/*` Lambda, the S3-backed
study source (`web/pacs-study-source.mjs`), and a one-command `deploy.sh`.
Deploy with `bash infra/deploy.sh`; the dashboard runs in S3 mode at
`https://<dist>.cloudfront.net/pacs-dashboard.html?source=s3`. See
[`infra/README.md`](../infra/README.md) for the deploy guide and security/DDoS
posture.

Pipeline docs: [pacs-s3-datasets.md](pacs-s3-datasets.md) ·
[pacs-encode-design.md](pacs-encode-design.md) ·
[infra/README.md](../infra/README.md) ·
[scripts/pacs-ingest/README.md](../scripts/pacs-ingest/README.md).

**The demo is open to humans, closed to scripts.** No login and no identity —
bots and scripted clients are stopped by an AWS WAF Challenge rule. Design in
[pacs-access-control-design.md](pacs-access-control-design.md), tasks below.
Prerequisite for the first public deploy, not a follow-up.

## Headline TODO — the Lambda / online demo service

🚧 **Host the PACS dashboard as a public online demo against the S3 dataset.**
Infrastructure is implemented in `infra/` (SAM template, Lambda, WAF, deploy
script) and the front-end S3 mode is wired in (`web/pacs-study-source.mjs`).
Full architecture in [pacs-lambda-service-design.md](pacs-lambda-service-design.md).

Recommended shape (one CloudFront distribution, path-routed, bucket stays
private):

| Task | Status | Notes |
|------|--------|-------|
| Cross-origin isolation strategy (COOP/COEP/CORP) | ✅ done | Response Headers Policy in `infra/template.yaml`; single-distribution same-origin trick (design §The key insight) |
| Static front-end origin (`/*` → app bucket via OAC) | ✅ done | `AppBucket` + `S3OriginAccessControl` + `AppBucketPolicy` in template.yaml |
| Dataset origin (`/data/*` → studies bucket via OAC) | ✅ done | `StudiesBucketPolicy` + `/data/*` behavior + `DataPathRewrite` CloudFront Function (strips `/data` prefix) |
| Thin API Lambda (`/api/studies`, `/api/health`) | ✅ done | `infra/api/index.js` — manifest-proxy variant, read-only, 256 MB, 10s timeout |
| Lambda OAC (Function URL not directly callable) | ✅ done | `LambdaOriginAccessControl` + `AWS_IAM` auth type |
| Front-end `StudySource` indirection | ✅ done | `web/pacs-study-source.mjs` — `?source=s3` gates it; `pacs-runner.mjs` gains `resolvePath`/`rawManifest`/`refManifest`/`cineFrameFn` opts (defaults preserve dev/CI flow) |
| IaC (AWS SAM) + scoped IAM role | ✅ done | `infra/template.yaml` + `infra/waf.yaml`; `ApiFunctionRole` scoped to manifest GetObject only |
| Abuse guardrails (CloudFront cache + WAF rate rule + budget alarm) | ✅ done | `waf.yaml` rate rule (2000 req/5min) + managed rule sets; `EgressBudgetAlarm` (50 GB/5min tripwire) |
| WAF WebACL (us-east-1, CLOUDFRONT scope) | ✅ done | `infra/waf.yaml` — separate stack, ARN passed into the us-west-1 stack |
| Access logs bucket | ✅ done | `AccessLogsBucket` with 30-day lifecycle |
| **First real deploy + smoke test** | 📋 todo | `bash infra/deploy.sh` against the live account; verify `?source=s3` loads a study and decodes |
| `/api/presign` (deferred — download-original-study feature) | 📋 todo | Not wired into the default viewer path (design §6.2) |

Est. cost: ~$15–55/month (CloudFront egress-dominated). The bot mitigation below
removes the scripted-mirroring scenario that dominates that range.

## Bot mitigation — required before the demo is public

Design: [pacs-access-control-design.md](pacs-access-control-design.md).
**Decided: no login, no identity, no tracking.** An AWS WAF Challenge rule
silently verifies that the client runs JavaScript; `curl`/`wget`/HTTP libraries
can't and are refused. Humans see nothing.

This deletes the whole authentication layer from earlier drafts — no login page,
no signed cookies or key group, no auth Lambda, no DynamoDB, no SES/Cognito.
Identity options are kept in the design's appendix if the requirement ever
changes.

| Task | Status | Notes |
|------|--------|-------|
| WAF `Challenge` rule matching **all** paths, immunity 259200 (72 h max) | 📋 todo | Must cover the dashboard HTML: only an `Accept: text/html` request gets the interstitial that *mints* the token. Challenging only `/data/*` breaks the viewer (design §3) |
| 202 guard in the front-end fetch helper | 📋 todo | `status===202 && x-amzn-waf-action: challenge` → distinct error, never reaches a decoder. Without it an expired token reads as a **codec checksum mismatch** (§4) |
| Second Response Headers Policy without COEP + `/bootstrap.html` fallback | 📋 todo | Insurance for §5: if the interstitial's script turns out to be cross-origin, `require-corp` blocks it and the site breaks for everyone |
| **Deploy check: does the interstitial run under `COEP: require-corp`?** | 📋 todo | The one genuine unknown. Verify before announcing the URL (§5) |
| Lower the WAF rate-rule limit from 2000/5 min | 📋 todo | Sized for anonymous browsing; a real session needs a few hundred (§6) |
| `robots.txt` in the app bucket | 📋 todo | Two lines; well-behaved crawlers leave before costing anything (§6) |
| Keep the Playwright suite on the local server | 📋 todo | Pointed at CloudFront it gets challenged; headless Chromium *probably* passes, which is a bad basis for a correctness gate (§8) |
| Optional: publish a curated ~1–2 GB subset as the demo corpus | 📋 todo | Cost decision, not security — cuts worst-case egress by ~10× (§7) |

## Cross-cutting open items

### ⚠️ Security — do first
📋 **Rotate the AWS keys.** The upload used **root-account access keys** pasted
in plaintext. Replace with a bucket-scoped IAM user / role
(`s3:GetObject`,`s3:ListBucket` on this bucket only) and delete the root pair.

### 🐛 Core-codec robustness bugs (found on real data, not in the test corpus)
These blocked 5 studies during the batch encode. See
[pacs-encode-design.md](pacs-encode-design.md) and the batch-encode failure
analysis.

- 🐛 **All-zero-frame panic** — `deltarlecompressu16.go:26`: `maxValue==0`
  (blank slices at volume ends) → `1 << -1` negative-shift panic. Guarded in
  `cmd/mic-pacs-encode` (clamp to ≥1); the **core codec should clamp
  `pixelDepth` to ≥1** so `mic-compress` is safe too.
- 🐛 **PET FSE failures** — `FSE compress: weight < 1` /
  `input is not compressible` on high-entropy PET (`pt-psma` ×4). The
  incompressible case should **fall back to raw storage**, not error.
- 🐛 **WSI roundtrip mismatch** — `sm-wsi-small-5475.4.0` frame 13 compressed
  but decoded to different pixels. The encode→decode→compare guard caught it
  and refused to write. Needs a minimal repro from that frame.

### 📋 Dataset coverage gaps
- 📋 **RGB / colour path** — US/VL and palette studies fail the grayscale-only
  encoder. Route to `CompressRGB` (MICR) / `CompressWSI` (MIC3). Currently
  3–4 studies skipped by design.
- 📋 **Heterogeneous IDC "series"** — some series dirs bundle *different images*
  (mammogram + ROI crop + mask; mixed WSI tile sizes), not volume slices, so
  the dimension check rejects them (CBIS-DDSM ×2, DX ×1, SM ×2). Handle by
  emitting each distinct-geometry image as its own single-frame study.
- 📋 **Scale up** via `select_idc.py --scale N` once the above are handled
  (disk ceiling ~`--scale 2` locally; unbounded on the CloudFront side).

## Reproduce the pipeline

```bash
# 1. select + download + verify (see scripts/pacs-ingest/README.md)
.venv/bin/python scripts/pacs-ingest/select_idc.py --out ./pacs-data
.venv/bin/python scripts/pacs-ingest/pacs_ingest.py --out ./pacs-data --with-idc
# 2. encode all codecs (roundtrip-verified)
go build -o /tmp/mpe ./cmd/mic-pacs-encode && /tmp/mpe --workers 4
go run -tags cgo_ojph ./cmd/mic-pacs-refgen --workers 4
# 3. upload (scoped creds via env; never in repo)
.venv/bin/python scripts/pacs-ingest/upload_s3.py \
    --bucket mic-studies-594551578651-us-west-1-an --region us-west-1
```
