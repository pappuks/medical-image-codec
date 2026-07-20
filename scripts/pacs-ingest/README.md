# PACS web-viewer — dataset ingest

Downloads free open-access DICOM sources for the PACS web-viewer S3 benchmark,
verifies each object is genuinely lossless before it can serve as codec ground
truth, and emits the S3 key layout + `manifest.json`.

Source catalog and licensing: [`docs/pacs-s3-datasets.md`](../../docs/pacs-s3-datasets.md).

## Why the Tier split matters

The bucket stores `raw/` + one compressed object per codec (`mic`, `pics`,
`htj2k`, `jls`, `jxl`). If a `raw/` object is a **lossy** source, every ratio
and PSNR number computed against it is meaningless. So the script reads each
file's `TransferSyntaxUID` and:

- **Tier A** (uncompressed / JPEG-LS / J2K-reversible / RLE / HTJ2K lossless) →
  written under `raw/`, gets codec prefixes. Valid ground truth.
- **Tier B** (JPEG baseline/extended, J2K irreversible, MPEG, etc.) → written
  under `demo/`, **never** `raw/`. Viewer-UI/decode-speed demo only.

The classifier trusts the actual transfer syntax over the declared tier: a
declared-A file that is secretly lossy is **quarantined** to B; a declared-B
file that is actually lossless is **promoted** to A.

## Run it

```bash
# from repo root; uses the repo .venv (pydicom 3.x)
.venv/bin/python scripts/pacs-ingest/pacs_ingest.py --out ./pacs-data --bucket mic-pacs-demo

# dry run — list what would be fetched, download nothing
.venv/bin/python scripts/pacs-ingest/pacs_ingest.py --plan
```

Downloads land in `./pacs-data/raw-src/<id>/` (gitignored). Metadata +
S3 key plan is written to `./pacs-data/manifest.json`.

The default seed set is small on purpose — genuine enhanced multi-frame CT/MR,
lossless US, an uncompressed MR, plus lossy XA/US cines to exercise the Tier-B
path. It validates the full pipeline without large egress.

## Volumetric data at scale — NCI Imaging Data Commons

`select_idc.py` queries the full IDC index (**1,032,911 series**) and writes a
modality-balanced `idc-selection.json`; `pacs_ingest.py --with-idc` downloads it.

```bash
# 1. select (no download) — prints per-modality totals
.venv/bin/python scripts/pacs-ingest/select_idc.py --out ./pacs-data
.venv/bin/python scripts/pacs-ingest/select_idc.py --out ./pacs-data --scale 2

# 2. download + verify + manifest
.venv/bin/python scripts/pacs-ingest/pacs_ingest.py --out ./pacs-data --with-idc
```

Downloads are **resumable**: each series dir gets a `.complete` marker, so
re-running skips what's already fetched.

### Filters applied (both are load-bearing)

- **Lossless transfer syntax only** → 922,311 of 1,032,911 series survive.
- **CC BY 4.0 / 3.0 only — all CC BY-NC excluded.** This matters: the obvious
  tomosynthesis collection `breast_cancer_screening_dbt` is **CC BY-NC 4.0**
  (non-commercial), so tomosynthesis is sourced from **`ea1141`** (CC BY 4.0,
  uncompressed, ~576 MB/series) instead.

Default selection: **73 series / 8.91 GB** — CT 16, MG 19, MR 14, PT 7, SM 5,
CR 4, DX 4, US 4. `SM` = slide microscopy, which feeds the MIC3 / WSI pipeline.

### Disk budgeting

Raw is one third of the story — five codec variants per Tier-A study roughly
**triple** the footprint. Budget ~3× the raw selection before using `--scale`.

No credentials are needed; IDC's bucket is public
(`aws s3 ls --no-sign-request s3://idc-open-data/`).

## Upload to S3

`upload_s3.py` reads `manifest.json` and pushes each study to its planned
prefix, attaching `license` / `attribution` / `tier` / `transfer-syntax` as S3
user-metadata so CC-BY credit travels with the data.

```bash
# credentials come from the environment -- never commit them
export AWS_PROFILE=<profile>
.venv/bin/python scripts/pacs-ingest/upload_s3.py \
    --bucket <bucket> --region us-west-1 --dry-run   # verify key layout
.venv/bin/python scripts/pacs-ingest/upload_s3.py \
    --bucket <bucket> --region us-west-1
```

Layout: `<id>/raw/` for Tier A, `<id>/demo/` for Tier B (lossy data is never
placed under `raw/`), plus `manifest.json` at the bucket root for viewer
discovery. `aws s3 sync` makes re-runs incremental.

### Credential hygiene

Use a **scoped IAM user** limited to the target bucket — never root account
access keys (root keys cannot be restricted, and a leak compromises the whole
account). Prefer a named profile or instance role over inline keys, and rotate
anything that has been pasted into a terminal, chat, or CI log.

## Next stage (compress)

For each Tier-A `raw/` object the pipeline still needs to:

1. Compress into all codecs → `<id>/{mic,pics,htj2k,jls,jxl}/`
   (MIC via `cmd/mic-compress`; HTJ2K/JLS/JXL via `cmd/mic-refgen`).
2. Attach `license`/`attribution` (from the manifest) as S3 object metadata —
   CC-BY requires visible credit in the viewer.
3. Upload:
   ```bash
   aws s3 sync ./pacs-data/raw-src s3://mic-pacs-demo/
   ```
4. Serve behind CloudFront with COOP/COEP headers so the WASM-threads decoders
   (MIC-C-WASM-PICS, CharLS, OpenJPH) run — same requirement as the local
   `web/pacs-dashboard.html`.
