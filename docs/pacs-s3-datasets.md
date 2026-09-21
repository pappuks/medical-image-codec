# PACS Web Viewer — S3 Dataset Download Manifest

Curated list of **free, open-access** DICOM sources to seed the S3 bucket for the
PACS web-viewer benchmark. For each study we store a `raw/` original plus one
compressed object per codec (`mic/`, `pics/`, `htj2k/`, `jls/`, `jxl/`).

## Ground rule: "raw" must be truly lossless

Because the bucket holds a lossless-codec comparison, the **source pixel data must
be uncompressed or losslessly compressed**. If the source DICOM is *lossy* JPEG,
the "raw" object is already degraded and every ratio/PSNR number computed against
it is meaningless. Sources are therefore split into two tiers:

- **Tier A — codec ground-truth grade**: uncompressed or lossless source. Use for
  the actual benchmark (raw + all-codec compressed).
- **Tier B — viewer-demo only**: lossy source. Fine for exercising the viewer UI
  and decode-speed feel; **exclude from ratio/fidelity tables.**

Sizes are approximate per-study (not whole-collection). TCIA collections are
downloaded per-study via the NBIA client / IDC, not as one blob. **Confirm the
license on each TCIA collection page before re-hosting** — most are CC-BY but a
few sub-collections differ.

---

## Tier A — Codec ground-truth grade (uncompressed / lossless)

### Volumetric CT / MR — true 3D series & enhanced multi-frame

| ID | Modality | Source / URL | Approx size | Dims × frames | License |
|----|----------|--------------|-------------|---------------|---------|
| ENH-CT-NEMA | CT (enhanced multi-frame, single file) | NEMA WG04 — `ftp://medical.nema.org/medical/Dicom/Multiframe/CT/` (also mirrored via dclunie.com) | 5–30 MB | 512×512 × tens of frames | Public / DICOM reference |
| ENH-MR-NEMA | MR (enhanced multi-frame, single file) | NEMA WG04 — `ftp://medical.nema.org/medical/Dicom/Multiframe/MR/` | 5–30 MB | 256–512² × tens | Public / DICOM reference |
| CT-LIDC-01..05 | CT chest volume (series) | TCIA **LIDC-IDRI** — https://www.cancerimagingarchive.net/collection/lidc-idri/ | 60–160 MB/scan | 512×512 × 120–300 | CC-BY 3.0 |
| CT-NSCLC-01..03 | CT thorax volume (series) | TCIA **NSCLC-Radiomics** — https://www.cancerimagingarchive.net/collection/nsclc-radiomics/ | 50–200 MB/scan | 512×512 × 100–300 | CC-BY 3.0 |
| MR-DUKE-01..03 | MR breast volume (multi-sequence) | TCIA **Duke-Breast-Cancer-MRI** — https://www.cancerimagingarchive.net/collection/duke-breast-cancer-mri/ | 50–200 MB/series | 320–512² × 100–200 | CC-BY 4.0 (TCIA-Restricted variants exist — verify) |
| IDC-CT/MR-* | Any CT/MR volume | NCI **Imaging Data Commons** — https://portal.imaging.datacommons.cancer.gov/ (data already in public AWS/GCS buckets) | varies | varies | Per-collection CC-BY |

### Mammography — 2D FFDM

| ID | Modality | Source / URL | Approx size | Dims | License |
|----|----------|--------------|-------------|------|---------|
| MG-CMMD-01..08 | MG full-field digital (2 views × 2 sides) | TCIA **CMMD** — https://www.cancerimagingarchive.net/collection/cmmd/ | ~9 MB/image, ~35 MB/study | ~2294×1914, 12–14 bit | CC-BY 4.0 |
| MG-CBIS-01..05 | MG scanned film (curated DDSM) | TCIA **CBIS-DDSM** — https://www.cancerimagingarchive.net/collection/cbis-ddsm/ | 5–30 MB/image | large, 16-bit | CC-BY 3.0 |
| MG-NEMA-1..4 | MG (already in repo as MG1–MG4) | NEMA compsamples (dclunie compression sample sets) | 5–25 MB | large | Public reference | 

### Mammography — 3D digital breast tomosynthesis (volumetric MG)

> **Correction (verified against the IDC index).** `breast_cancer_screening_dbt`
> is **CC BY-NC 4.0**, i.e. *non-commercial* — not CC-BY as first listed here.
> 22,032 of its series are NC. Tomosynthesis is therefore sourced from
> **`ea1141`** instead: CC BY 4.0, Explicit VR LE (uncompressed), ~576 MB
> median per series, 4,260 tomosynthesis series available.

| ID | Modality | Source / URL | Approx size | Dims × frames | License |
|----|----------|--------------|-------------|---------------|---------|
| mg-tomo-ea1141 | MG tomo (3D) | IDC collection **`ea1141`** | ~300–1200 MB/series | tomosynthesis stack | **CC BY 4.0** ✅ |
| ~~DBT-BCS~~ | MG tomo | TCIA Breast-Cancer-Screening-DBT | 0.3–2 GB/case | — | ⚠️ **CC BY-NC 4.0 — excluded** |
| DBT-UPMC-01..02 | MG tomo projections + recon slices | dclunie **UPMC Breast Tomo & FFDM** — http://www.dclunie.com/pixelmedimagearchive/upmcdigitalmammotomocollection/index.html | 100–800 MB/case | Hologic Selenia Dimensions | Public download (research) |
| MG-TOMO (repo) | MG tomo 69-frame | already in repo (`testdata/`, MIC2) | — | 2457×1890 × 69, 10-bit | — |

### PET / NM volumetric

| ID | Modality | Source / URL | Approx size | Dims × frames | License |
|----|----------|--------------|-------------|---------------|---------|
| PET-QIN-01..02 | PET whole-body volume | TCIA **QIN-HEADNECK** / **ACRIN-FMISO** — https://www.cancerimagingarchive.net/browse-collections/ (filter PT) | 30–120 MB | 128–256² × 100–400 | CC-BY 3.0/4.0 |
| PETCT-Aliza | PET (from TCIA, via Aliza mirror) | https://www.aliza-dicom-viewer.com/download/datasets (CT Image Storage → "PET study (TCIA)") | tens MB | series | TCIA attribution |

### CR / DX projection (single-frame, 16-bit — good ratio stress test)

| ID | Modality | Source / URL | Approx size | Dims | License |
|----|----------|--------------|-------------|------|---------|
| CR/XR (repo) | CR 2140×1760, XR 2048×2577 | already in repo `testdata/` | — | 16-bit | — |
| DX-SAGA-* | DX/CR chest | Saga IT — https://saga-it.com/dicom/samples | 1–15 MB | large 16-bit | CC-BY |

### Enhanced multi-frame edge cases (format coverage)

| ID | Modality | Source / URL | Format note | License |
|----|----------|--------------|-------------|---------|
| ENH-CT-ALIZA | CT enhanced, non-uniform / rotation | Aliza "Enhanced CT Image Storage" | tests non-uniform frame geometry | verify |
| ENH-CT-PERF | CT perfusion 3D+t + color palette | Aliza "Perfusion 3D+t" | 4D + palette | verify |
| ENH-US-VOL | US 3D+t enhanced volume | Aliza "Enhanced US Volume Storage" (GE) | **lossy JPEG inside → Tier B** | verify |

---

## Tier B — Viewer-demo only (lossy source — exclude from fidelity tables)

| ID | Modality | Source / URL | Size | Frames | Why Tier B |
|----|----------|--------------|------|--------|-----------|
| XA-RUBO-0002 | XA angiogram cine | https://www.rubomedical.com/dicom_files/ (`dicom_viewer_0002.zip`) | 1.7 MB | 512×512 × 96 | Lossy JPEG; no formal license |
| XA-RUBO-0009 | XA angiogram cine | Rubomedical `dicom_viewer_0009.zip` | 2.6 MB | 512×512 × 137 | Lossy JPEG |
| XA-RUBO-0012 | XA angiogram cine | Rubomedical `dicom_viewer_0012.zip` | 1.4 MB | 512×512 × 70 | Lossy JPEG |
| US-RUBO-0020 | US cine | Rubomedical `dicom_viewer_0020.zip` | 276 KB | 600×430 × 11 | RLE (ok-ish) but tiny; demo only |
| MR-RUBO-BRAIN | MR single slice | Rubomedical `dicom_viewer_Mrbrain.zip` | 225 KB | 512×512, 12-bit | Uncompressed but single-frame; UI smoke test |
| US-ALIZA-* | US 2D+t cine (Philips/Acuson/GE) | Aliza "Ultrasound Multi-frame" | small | multi | Lossy JPEG / RLE |
| XA-CINE (repo) | XA cine | already fetched via `testdata/multiframe/fetch-cine-sources.sh` | — | — | source was JPEG-Lossless→transcoded |

---

## Automated selection from the IDC index (implemented)

`scripts/pacs-ingest/select_idc.py` replaces hand-picking: it loads the full IDC
index (**1,032,911 series**), applies the hard filters, and emits a
modality-balanced selection.

- lossless transfer syntax only → **922,311** series survive
- **CC BY 4.0 / 3.0 only**; all CC BY-NC variants excluded (28,783 NC-4.0 +
  5,851 NC-3.0 series in the index)
- per-modality size bands keep studies genuinely volumetric while avoiding the
  150 GB slide-microscopy outliers

Default selection: **73 series / 8.91 GB** — CT 16, MG 19, MR 14, PT 7, SM 5,
CR 4, DX 4, US 4. Scale with `--scale`.

**Slide microscopy (SM)** was added after the index showed 592 lossless CC-BY SM
series — these feed the existing MIC3 / WSI pipeline directly. Median SM series
is 4.6 GB (max 150 GB), so the quotas target the `htan_wustl` / `htan_ohsu`
small-to-mid range.

### Disk budgeting

Raw is only the first third: five codec variants per Tier-A study roughly
triple the footprint. Budget **~3× the raw selection size** before scaling up.

## Recommended seed set (minimal but full-coverage)

For a first S3 load that covers every modality class without ballooning storage:

| Slot | Pick | Rationale |
|------|------|-----------|
| Volumetric CT | 2× LIDC-IDRI scans | canonical 16-bit CT volume, CC-BY |
| Volumetric MR | 2× Duke-Breast-Cancer-MRI series | 3D MR, CC-BY |
| Enhanced multi-frame | ENH-CT-NEMA + ENH-MR-NEMA | true single-file multi-frame ground truth |
| MG 2D | 4× CMMD studies | high-res FFDM, best-ratio modality |
| MG 3D | 1× Breast-Cancer-Screening-DBT case | volumetric mammography |
| PET | 1× QIN PET volume | dynamic-range + PET decorrelation case |
| CR/DX | 1× Saga DX chest | large single-frame projection |
| Cine (Tier B) | 1× Rubomedical XA | viewer motion/loop demo only |

## Notes for the download pipeline

- **TCIA / IDC**: use the NBIA Data Retriever or `s5cmd`/`gsutil` against the IDC
  public buckets — do **not** scrape the website. IDC data is already DICOM in
  cloud object storage, so S3→S3 copy avoids egress pain.
- **Attribution**: store per-object `license` + `attribution` (+ collection DOI)
  as S3 object metadata so the viewer can display it; CC-BY *requires* credit.
- **De-lossy check**: before ingesting, read `TransferSyntaxUID`; reject/flag any
  lossy syntax (1.2.840.10008.1.2.4.50/51/80-… JPEG lossy) out of Tier A.
- **Complement, don't duplicate**: repo already has MR/CT/CR/XR/MG1–4/MG_TOMO and
  cine sources — this manifest adds volumetric CT/MR, DBT, PET, and enhanced
  multi-frame that the repo lacks.
