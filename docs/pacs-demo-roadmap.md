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

Pipeline docs: [pacs-s3-datasets.md](pacs-s3-datasets.md) ·
[pacs-encode-design.md](pacs-encode-design.md) ·
[scripts/pacs-ingest/README.md](../scripts/pacs-ingest/README.md).

## Headline TODO — the Lambda / online demo service

📋 **Host the PACS dashboard as a public online demo against the S3 dataset.**
Full architecture in [pacs-lambda-service-design.md](pacs-lambda-service-design.md).

Recommended shape (one CloudFront distribution, path-routed, bucket stays
private):

| Task | Status | Notes |
|------|--------|-------|
| Cross-origin isolation strategy (COOP/COEP/CORP) | ✅ designed | single-distribution same-origin trick avoids per-object CORP; see design §The key insight |
| Static front-end origin (`/*` → app bucket via OAC) | 📋 | host `web/` HTML/JS/WASM |
| Dataset origin (`/data/*` → studies bucket via OAC) | 📋 | keep all public-access blocks on |
| Thin API Lambda (`/api/studies`, `/api/health`) | 📋 | browser can't `ListObjects` a private bucket |
| Front-end `StudySource` indirection | 📋 | `pacs-model.mjs`/`pacs-runner.mjs` hardcode local `testdata/` naming; point at S3 study IDs via existing `baseUrl` |
| IaC (AWS SAM) + scoped IAM role | 📋 | read-only, this bucket; WAF ACL must live in us-east-1 |
| Abuse guardrails (CloudFront cache + WAF rate rule + budget alarm) | 📋 | public demo serving 17 GB |

Est. cost: ~$15–55/month (CloudFront egress-dominated).

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
