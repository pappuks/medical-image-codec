# PACS Batch Encode Pipeline — Design

Status: **design implemented (Phases 1–4)**. Target: turn the 80 Tier-A studies
already sitting in `pacs-data/raw-src/` (and mirrored to
`s3://mic-studies-594551578651-us-west-1-an/<id>/raw/`) into MIC, PICS, HTJ2K,
JPEG-LS and JPEG-XL artifacts under the S3 prefixes already reserved in
`pacs-data/manifest.json` (`entries[].s3.codec_prefixes`), so a browser PACS
viewer can benchmark real-time decode across codecs. The batch encoders
(`cmd/mic-pacs-encode`, `cmd/mic-pacs-refgen`) and the upload pipeline
(`scripts/pacs-ingest/upload_s3.py`) are built; 67 of 80 Tier-A studies are
fully encoded and in S3. See [pacs-demo-roadmap.md](pacs-demo-roadmap.md) for
current status and [infra/README.md](../infra/README.md) for the hosting
stack. This document remains the design reference for the encoder routing and
manifest schema.

---

## 0. Corpus reality check (read before trusting the task's assumptions)

The task brief assumes a fairly clean split: "grayscale 16-bit" vs. "RGB/color
(SM + some US)". Reading `pacs-data/manifest.json` and probing several studies
with `pydicom` directly shows the real corpus is messier in ways that change
the design. These are load-bearing findings, not trivia:

1. **Only Tier A is in scope, and it's 80/83, not all 83.** 3 entries are Tier
   B (lossy source, e.g. `us-lossy-probe`). `upload_s3.py` already routes
   Tier B to `demo/`, never `raw/`. The codec-encode pipeline must only ever
   touch `tier == "A"` entries — encoding a lossy source and presenting it as
   ground truth for a compression benchmark would be meaningless.

2. **78/80 Tier-A files are already native-readable; 2 are not.**
   Transfer-syntax distribution across the 80 Tier-A entries:
   `Explicit VR LE` 69, `Implicit VR LE` 9, `JPEG 2000 Lossless` 1
   (`us-lossless`), `RLE Lossless` 1 (`us-cine-0020`, PALETTE COLOR).
   The Go DICOM library's `Frame.GetNativeFrame()` only works on
   `NativeData`; encapsulated frames (`Frame.Encapsulated == true`) return an
   error. `readDicomMultiFrame`/`readDicomSeries` in `cmd/mic-compress/main.go`
   will **fail** on those 2 studies as written today. This is small in count
   (2/80) but must be designed for explicitly, not discovered at run time.

3. **`PhotometricInterpretation` in the wild includes more than
   MONOCHROME/RGB.** Across Tier A + B: `MONOCHROME2` (78), `YBR_RCT` (1),
   `YBR_ICT` (1), `PALETTE COLOR` (1), `MONOCHROME1` (1), `RGB` (1). None of
   the existing codec entry points accept YBR-anything or palette-indexed
   pixels — they all expect either `[]uint16` grayscale or interleaved RGB
   bytes. `SamplesPerPixel` is also not reliably populated in the manifest's
   `representative` summary (it only reflects the *first* file of a
   series) — routing must read tags from the actual file being encoded, not
   trust the manifest.

4. **The 5 "SM" (slide microscopy) studies are not gigapixel RGB WSI.** All
   5 are `MONOCHROME2`, multi-file (27–72 files/study). Probing
   `sm-wsi-small-9959.4.0`: every instance has `TotalPixelMatrixColumns ==
   Columns` and `TotalPixelMatrixRows == Rows`, `NumberOfFrames == 1` — i.e.
   each file *is* the full plane, not a tile. `OpticalPathSequence` shows
   "Epifluorescence illumination" — these are multi-channel/Z-stack
   immunofluorescence planes, architecturally identical to a CT/MR series
   (a directory of same-size single-frame grayscale files), not a tiled
   pyramid. `sm-wsi-mid-9632.4.0` is a genuine mix *within one study
   directory*: some instances have `TotalPixelMatrix` (1252×1242) `>` frame
   size (1024×1024) with `NumberOfFrames == 4` (truly tiled), others in the
   same directory are untiled single planes (`ImageType: DERIVED, PRIMARY,
   VOLUME, RESAMPLED` — almost certainly a lower-resolution pyramid level or
   thumbnail stored as a separate SOP instance).
   → **The CLAUDE.md rule "SM should use `CompressWSI`" is correct only for
   the small subset of *instances* that are actually tiled, not for the
   modality as a whole.** Routing must be decided per DICOM instance from
   measured tags (`TotalPixelMatrixColumns/Rows` vs `Columns/Rows`,
   `NumberOfFrames`), never from the `SM` modality label.

5. **`readDicomSeries`'s existing width/height mismatch check is real
   protection and should be reused as-is** — it already errors out if a
   series directory contains frames of differing dimensions, which is
   exactly the failure mode a mixed-tiling SM directory could otherwise hit
   silently.

These findings drive §5 (routing) and §10 (risks) below. Where the task brief
and the observed corpus disagree, the design follows the corpus.

---

## 1. Problem & Assumptions

**Goal.** For every Tier-A study, produce roundtrip-verified MIC, PICS,
HTJ2K, JPEG-LS, and JPEG-XL artifacts (where the codec applies), upload them
to the pre-planned S3 prefixes with the same license/attribution metadata as
the source, and emit a manifest the browser viewer can use to fetch, decode,
and checksum-verify each artifact.

**Assumptions** (stated explicitly since some materially affect the design):
- A1: `pacs-data/raw-src/<id>/` is the authoritative local input; S3 `raw/`
  is already populated and is not re-derived here.
- A2: The repo's `.venv` (pydicom 3.0.1, `gdcm` bound, `pylibjpeg` 2.1.0) is
  available on the machine doing the encode — it already is, and is the
  established mechanism for DICOM transcoding in this repo
  (`testdata/multiframe/fetch-cine-sources.sh`).
- A3: "ALL codecs" means: every codec applies to a study unless the codec is
  structurally incapable of representing that study's pixel format (e.g. the
  `ojph` package is `[]uint16`-grayscale-only, so RGB studies do not get
  HTJ2K/JLS/JXL artifacts — see §5). This is recorded in the manifest as
  `applicable: false`, not silently omitted.
- A4: Deep volumetric CT/MR series (up to 658 slices) do not additionally
  need a per-slice `.mic` file fan-out the way the small (≤16-frame) built-in
  cine corpus does — see §6 for why.
- A5: One machine, one run at a time (no distributed workers) — 8.5 GB / 80
  studies fits comfortably in a single-host worker-pool design; a distributed
  design would be over-engineering for this scale.

---

## 2. High-Level Architecture

```
pacs-data/manifest.json ──┐
pacs-data/raw-src/<id>/   │
                           ▼
                 ┌───────────────────┐
                 │ 0. normalize       │  Python (.venv), only for the ~2/80
                 │  (pydicom/gdcm)    │  encapsulated-TS / palette studies.
                 └─────────┬──────────┘  Writes pacs-data/normalized-src/<id>/,
                           │             never touches raw-src/.
                           ▼
                 ┌───────────────────┐
                 │ 1. classify        │  Per DICOM instance/series: read real
                 │  (Go, no cgo)      │  tags → routing bucket (§5).
                 └─────────┬──────────┘
                           ▼
                 ┌───────────────────┐
                 │ 2. mic-pacs-encode │  Go, no cgo. MIC1/MIC2/MIC3/MICR +
                 │  (worker pool)     │  PICS. encode → verify → write.
                 └─────────┬──────────┘  Emits pacs-data/encoded/<id>/{mic,pics}/
                           │             + pacs-data/encoded/<id>/mic-manifest.json
                           ▼
                 ┌───────────────────┐
                 │ 3. mic-pacs-refgen │  Go, -tags cgo_ojph. HTJ2K/JLS/JXL,
                 │  (grayscale only)  │  grayscale artifacts only.
                 └─────────┬──────────┘  Emits pacs-data/encoded/<id>/{htj2k,jls,jxl}/
                           │             + pacs-data/encoded/<id>/ref-manifest.json
                           ▼
                 ┌───────────────────┐
                 │ 4. manifest-merge  │  Go, no cgo. Combines per-study
                 └─────────┬──────────┘  fragments → pacs-data/codec-manifest.json
                           ▼
                 ┌───────────────────┐
                 │ 5. upload_s3.py    │  Extended: syncs raw/ + every non-empty
                 │  (extended)        │  codec/ prefix with the SAME license
                 └────────────────────┘  metadata dict per study.
```

Stages 2 and 3 are separate binaries/processes (see §3) but share the same
on-disk layout convention (`pacs-data/encoded/<id>/<codec>/...`) so stage 4
only ever reads, never re-derives.

---

## 3. Binary/package layout — the cgo split decision

**Decision: keep the existing two-binary split, add one binary to each
side.**

- `cmd/mic-pacs-encode/` — no cgo build tag. Produces MIC1/MIC2/MIC3/MICR +
  PICS artifacts for all 80 studies.
- `cmd/mic-pacs-refgen/` — `//go:build cgo_ojph`, requires
  libopenjph/libcharls/libjxl. Produces HTJ2K/JLS/JXL for the grayscale
  subset.
- A thin shell driver, `scripts/pacs-encode/run.sh`, documents/automates the
  two-command sequence + merge, the same way `CLAUDE.md`'s Build & Test
  section already documents multi-step recipes (e.g. the WASM build steps)
  rather than hiding them behind one orchestrator binary.

**Why not a single binary with runtime-gated cgo code (`-refcodecs` flag
behind the same build tag)?** Rejected: it would force every operator —
including someone who only wants MIC/PICS artifacts for a quick local test —
to have libopenjph/libcharls/libjxl installed just to `go build` the tool at
all. That breaks the property `cmd/mic-compress` deliberately protects today
("mic-compress intentionally has no cgo build tag so it stays buildable
without native libs installed" — from `cmd/mic-refgen/main.go`'s own doc
comment). Splitting is also simply consistent with the established
`mic-compress` / `mic-refgen` precedent; a new tool that behaves differently
from its closest sibling for no functional reason is a maintenance surprise.

**Why not have the no-cgo binary `exec.Command` the cgo binary?** Rejected as
unnecessary indirection. Nothing in the existing pipeline (`mic-compress
-testdata` + `mic-refgen` are already run as two independent, sequential
commands, not chained by a wrapper) shells out between the two worlds, and
introducing that here would add a process-boundary failure mode (PATH
resolution, partial-build detection, exit-code plumbing) for no benefit over
"run command 1, then command 2, then command 3" — which is also trivially
resumable per-stage, whereas a wrapper binary would need to reimplement that
resumability itself.

**Consequence:** `mic-pacs-encode` and `mic-pacs-refgen` each read the same
manifest and (where relevant) the same normalized/raw pixel source
independently — each re-parses/re-decodes the source DICOM rather than
one process handing decoded pixels to the other over IPC. This duplicates
DICOM parsing cost (cheap — DICOM parse is not the bottleneck; entropy coding
is) but keeps the two binaries fully decoupled, matching how `testImages`/
`cineDatasets` are already independently duplicated across
`cmd/mic-compress/main.go` and `cmd/mic-refgen/main.go` today (see the
`TODO(mic-refgen)` comment there — same rationale, already an accepted
tradeoff in this codebase).

---

## 4. Normalize pre-pass (Python, `.venv`)

Scope: only the small number of studies whose transfer syntax is not one of
`{1.2.840.10008.1.2, .1.2.1, .1.2.2}` (implicit/explicit/big-endian
uncompressed) — 2 of the current 80, but the rule is general so it stays
correct if the corpus grows.

Mirrors the exact pattern already proven in
`testdata/multiframe/fetch-cine-sources.sh`:

```python
import pydicom
from pydicom.uid import ExplicitVRLittleEndian
from pydicom.pixel_data_handlers.util import apply_color_lut

ds = pydicom.dcmread(src_path)
arr = ds.pixel_array                      # gdcm/pylibjpeg decode JPEG2000/RLE transparently
if ds.PhotometricInterpretation == "PALETTE COLOR":
    arr = apply_color_lut(arr, ds)        # materialize true RGB from the palette
    ds.PhotometricInterpretation = "RGB"
    ds.SamplesPerPixel = 3
    ds.PlanarConfiguration = 0
ds.PixelData = arr.tobytes()
ds.file_meta.TransferSyntaxUID = ExplicitVRLittleEndian
ds['PixelData'].VR = 'OW' if ds.BitsAllocated == 16 else 'OB'
ds.save_as(dst_path, enforce_file_format=True)
```

Rules:
- Writes to `pacs-data/normalized-src/<id>/`, **never** mutates
  `raw-src/` (which is the exact byte content already uploaded to S3 under
  `raw/` — it must stay untouched as ground truth).
- Idempotent: skip a file if the normalized output already exists and its
  mtime is newer than the source.
- `mic-pacs-encode`/`mic-pacs-refgen` read from `normalized-src/<id>/` when
  present, else `raw-src/<id>/` — a one-line "prefer normalized" resolution
  rule, not a fork in the rest of the pipeline.
- This step's own roundtrip is not separately re-verified — it's superseded
  by the mandatory encode→decode verification in §9, which operates on
  whatever bytes end up feeding the encoder either way.

---

## 5. Routing decision tree

Routing decides, **per DICOM instance-group** (a single multi-frame file, or
a directory of single-frame files sharing `SeriesInstanceUID`), which codec
path applies. Every classification reads actual tags from the (possibly
normalized) file — the manifest's `representative` block is only used as a
cheap pre-flight hint for disk-space estimation (§7), never as the routing
ground truth, because it only reflects one file per series (see §0.3).

```
                         read SamplesPerPixel + PhotometricInterpretation
                                          │
                 ┌────────────────────────┴────────────────────────┐
                 │                                                   │
         SamplesPerPixel==1                                  SamplesPerPixel==3
      Photometric ∈ {MONOCHROME1,2}                    or Photometric ∈ {RGB, YBR_*}
                 │                                      or Photometric == PALETTE COLOR
                 │  GRAYSCALE                            (normalize pre-pass already
                 │                                        converted PALETTE COLOR → RGB)
                 │                                                   │
                 │                                              COLOR (RGB bytes)
                 ▼                                                   ▼
   TotalPixelMatrixCols>Cols                          single file, single frame,
   or TotalPixelMatrixRows>Rows                        not tiled
   AND NumberOfFrames>1?                                       │
     │                │                                        ▼
    yes               no                              CompressRGB → MICR
     │                │
     ▼                ▼                              directory of single-frame
  WSI_TILED     NumberOfFrames>1                       color files (color US series)
  CompressWSI    in one file?                                  │
  (MIC3,          │        │                                   ▼
  channels=1)    yes       no                          CompressRGB per frame,
                  │        │                            named <id>_f%03d
                  ▼        ▼                            (mirrors existing cine
           MULTIFRAME   directory of                     per-frame convention)
             FILE       single-frame files
           CompressMulti  sharing SeriesInstanceUID,
           Frame (MIC2)   matching W/H?
                            │         │
                           yes        no (=1 file)
                            │         │
                            ▼         ▼
                      SERIES_DIR   SINGLE_FRAME
                     CompressMulti  CompressSingleFrame
                     Frame (MIC2)   (MIC1) + PICS via
                     via readDicom  writeMICVariants
                     Series          (both reused verbatim)

  Anything else (e.g. YBR_PARTIAL_*, unrecognized photometric) → UNSUPPORTED:
  logged with reason, counted in a "skipped" summary, never silently dropped.
```

Applied to the actual corpus:
- CT(18)/MR(17)/PT(7)/CR(4)/DX(4)/most MG(19) → GRAYSCALE, mostly
  SINGLE_FRAME or SERIES_DIR, tomosynthesis (`ea1141`) → MULTIFRAME_FILE.
- 4 of 5 SM studies → GRAYSCALE SERIES_DIR (treated exactly like a CT/MR
  series — multi-file grayscale volume, not WSI).
- `sm-wsi-mid-9632.4.0` → **mixed within one study**: the 4 instances with
  `NumberOfFrames==4` and `TotalPixelMatrix > frame size` → WSI_TILED
  (MIC3); the rest of that study's instances → GRAYSCALE SERIES_DIR/
  SINGLE_FRAME, same as any other SM study. This is why routing must be
  per-instance, not per-study: a single manifest entry can legitimately
  produce artifacts under more than one codec shape.
- `us-series-36501166` (RGB, 41 files) → COLOR, per-frame CompressRGB.
- `us-series-*` (MONOCHROME2, 14–26 files) → GRAYSCALE SERIES_DIR (plain
  grayscale ultrasound is common and unremarkable — no special-casing
  needed beyond the generic rule).
- `us-lossless` (YBR_RCT/JPEG2000) → normalize pre-pass → COLOR
  SINGLE_FRAME/CompressRGB.
- `us-cine-0020` (PALETTE COLOR/RLE) → normalize pre-pass (palette → RGB) →
  COLOR SINGLE_FRAME/CompressRGB.

**RGB and reference codecs (goal item 2's open question).** `ojph.CompressU16`/
`CharlsCompressU16`/`JXLCompressU16` all take `[]uint16` grayscale. There is
no RGB entry point anywhere in the `ojph` package. Decision: **skip**
HTJ2K/JLS/JXL for every COLOR-routed and WSI_TILED artifact; record
`"applicable": false` (not merely "missing") for those codec slots in the
manifest so the future viewer renders "N/A — reference codec has no RGB
support" instead of a broken fetch. This is a real, structural capability
gap, not a corner someone cut — adding RGB support to three vendored native
codec wrappers is out of scope for a batch-encode pipeline.

---

## 6. Volumetric series decision (goal item 3)

**Deep CT/MR series (up to 658 slices) → one `CompressMultiFrame` (MIC2)
artifact in *independent* mode as the primary artifact. No per-slice `.mic`
fan-out.**

Rationale, grounded in what `DecompressFrame` actually does
(`multiframecompress.go:266`):
```go
// DecompressFrame decompresses a single frame from a MIC2 file.
// For independent mode, any frame can be decoded directly.
// For temporal mode, frames 0..frameIdx are decoded sequentially.
```
Independent mode gives real O(1)-ish single-slice access — exactly what a
scroll-through PACS viewer benchmarks (fetch/decode one slice at a time).
Temporal mode is explicitly *not* random-access: reaching slice 400 means
decoding slices 0–400 first. So:
- **Independent-mode MIC2 is the artifact the viewer actually exercises for
  scroll/render benchmarking.**
- Temporal mode is compression-ratio-interesting but decode-benchmark-hostile
  for anything beyond a handful of frames. Emit it as a **second, optional**
  artifact only for series ≤100 frames (bounds the extra encode/storage
  cost); for the two 600+-frame series (`mr-ispy2-52934946` 658,
  `mr-ispy2-69584714` 602) emit independent-mode only, and record the reason
  in the manifest (`temporalSkipped: "frameCount>100"`) rather than silently
  omitting it.
- Per-slice single-frame `.mic` files (the pattern used for the *small*,
  ≤16-frame built-in cine corpus) are deliberately **not** produced for deep
  series: 658 extra files per study × 80 studies would multiply output file
  count by orders of magnitude for a capability (`DecompressFrame`) the MIC2
  container already provides natively. This is a scope narrowing relative to
  the existing cine convention, justified by the two-orders-of-magnitude
  difference in frame count (16 vs. up to 658).

**Reference codecs have no multi-frame container** — `ojph` wraps single 2D
images only. So `mic-pacs-refgen` necessarily encodes volumetric series
slice-by-slice (`<id>_s%03d.jph/.jls/.jxl`), which is actually the right
comparison anyway (a PACS viewer decodes one slice at a time regardless of
codec, so per-slice reference numbers are what the viewer needs to compare
against MIC2's `DecompressFrame`).

**Cap on reference-codec fan-out for deep series**: encoding 658 slices ×
3 reference codecs × verify-roundtrip for every one of the largest series is
expensive and produces a lot of small files for limited additional benchmark
value beyond what a good sample already shows. Decision: for series >100
frames, `mic-pacs-refgen` reference-encodes a bounded, evenly-spaced sample
(default 32 slices) and records `"refSlicesSampled": "32 of 658"` in the
manifest, so the viewer never assumes full per-slice coverage where it
doesn't exist. `mic-pacs-encode`'s MIC2 artifact still covers the full
series — this cap applies only to the reference-codec fan-out.

---

## 7. Concurrency model

- **Unit of parallelism: one study per worker**, not one file per worker.
  A worker pool sized `min(GOMAXPROCS, -workers flag)`, default `-workers 4`
  on the current dev machine (12 logical cores, 32 GB RAM) — bounded well
  below core count because peak memory per worker is driven by the largest
  series in flight (658 × 512×512×2B ≈ 345 MB of decoded `[]uint16` pixels
  per in-progress study, before compression buffers), and DICOM series
  parsing itself does a fair amount of small-file I/O that doesn't scale
  linearly with core count.
- Within a study, frame decode stays **sequential** (matches
  `readDicomSeries`'s existing structure — reuse verbatim); the *encode* step
  for a single frame can still benefit from PICS's own internal
  strip-parallel goroutines without adding a second layer of study-level
  fan-out to reason about.
- `mic-pacs-refgen` (cgo pass) uses the same per-study worker-pool shape,
  run as a **separate process invocation** after `mic-pacs-encode` completes
  (or independently, since it reads from the same normalized/raw source and
  writes to disjoint output prefixes) — see §3 for why these stay separate
  processes.
- Progress reporting: one `stderr` line per completed study,
  `[i/80] <id> tier=A modality=CT files=532 → mic/pics: 41.2 MB (3.4x) in 8.7s`,
  matching `upload_s3.py`'s existing `[i/N] id ...` convention so the two
  tools read consistently in a combined log.

---

## 8. Resumability + verification strategy (goal items 4 & 6)

**Resumability.** Before encoding any artifact, check for an existing
`pacs-data/encoded/<id>/<codec>-manifest.json` fragment entry for that exact
`(instance-group, codec, variant)` key with `verified: true` and a recorded
source hash matching the current source file's `sha256` (reuse
`sha256_representative`-style hashing already established in
`pacs_ingest.py`). If it matches, skip — this makes a killed/interrupted run
cheap to resume by just re-invoking the same command. A `-force` flag bypasses
the skip for deliberate re-encodes (e.g. after a codec bug fix).

**Verification.** Every artifact is encode → decode → compare → *then*
write, reusing the `encodeVerifyWrite` pattern from `cmd/mic-refgen/main.go`
verbatim for the reference-codec pass, and the equivalent for MIC/PICS/MIC2/
MIC3/MICR: decompress what was just compressed and compare against the
source pixel buffer before the file is considered good. Specifics:
- **MIC2 multi-frame**: verify *every* frame's roundtrip, not just frame 0,
  before the artifact is marked verified. This directly targets the bug
  class this repo has already hit once (commit `210e140`, "Close cine
  benchmark gaps: PICS strip coverage, stale ref-codec manifest, raw-strip
  decode bug") — a decode bug that only manifested on specific
  frames/strips, not frame 0. A per-frame verify pass is the cheap insurance
  against a repeat.
- **WSI_TILED (MIC3)**: verify every tile via `DecompressWSITile`.
- A roundtrip mismatch is a hard error for that artifact — log it, do not
  write the file, count it in a failure summary, continue to the next
  artifact/study rather than aborting the whole run (mirrors
  `mic-refgen`'s existing "failures accumulate, exit 1 at the end" pattern).
- Record both the source `sha256` and the decoded-pixel `fnv1a32` checksum
  (bit-identical algorithm to `web/pacs-model.mjs`'s `fnv1a32`, per
  `cmd/mic-compress/main.go`'s existing `fnv1a32LE`) in the manifest fragment,
  so the browser viewer can verify decoded pixels against the manifest
  without ever downloading the original DICOM — same mechanism already
  proven for the local `web/testdata` corpus, extended to the S3 corpus.

---

## 9. Disk footprint estimate + guard (goal item 5)

Free space at design time: **~68 GB**; raw corpus: **8.5 GB**.

**Rough footprint model.** Per-modality lossless ratios observed elsewhere in
this repo (MIC ≈ HTJ2K ≈ JPEG-LS ≈ JPEG-XL, within ~20% of each other on the
same source — see `docs/compression-results.md`, `web/pacs-model.mjs`
`REFERENCE_NATIVE`): MR ~2.3–2.4×, CT ~1.8–2.2×, CR ~3.5–4×, MG ~8–9×,
PT/DX similar to CT/CR respectively, RGB US ~1.6–6.2× (`docs/…` `CompressRGB`
NEMA numbers). Using a conservative blended ratio of **~3×** per codec:

| Prefix | Contents | Rough multiplier vs. one codec's `raw/ratio` |
|---|---|---|
| `mic/` | MIC1/2/3/R × {1-state, 4-state, 8-state} | ~3× (three near-identical-size copies) |
| `pics/` | 2 strip-counts × 2 FSE states (4/8) | ~4× |
| `htj2k/`, `jls/`, `jxl/` | one codestream each | ~1× each |

So each Tier-A study's total derived footprint is very roughly
`raw_bytes/3 × (3 + 4 + 1 + 1 + 1)` ≈ `raw_bytes × 3.3`. Applied to 8.5 GB:
**≈28 GB** for the full corpus across all five prefixes — comfortably under
68 GB, but not by a margin that tolerates being sloppy, especially since the
two 600+-slice CT/MR series and the tomosynthesis studies are large outliers
that could locally spike usage during a single study's processing.

**Guard, not just an estimate.** Before starting each study,
`mic-pacs-encode`/`mic-pacs-refgen`:
1. Compute an expected-bytes estimate for that specific study from its
   `representative.bytes` × the modality's prior ratio × the applicable
   prefix multipliers (table above, narrowed to whichever routing bucket
   applies — e.g. a COLOR study skips the htj2k/jls/jxl multiplier).
2. Read current free space (`syscall.Statfs` on the output filesystem).
3. If `estimate > free - 5 GB safety margin`, **stop the run cleanly**
   (not skip-and-continue) with a clear error naming the offending study and
   the shortfall — continuing until `ENOSPC` mid-write risks a truncated,
   silently-corrupt artifact that would only be caught later by the
   verification step, after wasting the encode time.
4. Recommend running `mic-pacs-encode` (no-cgo, ~raw×2.1 bytes per the table)
   to completion **before** `mic-pacs-refgen` (adds ~raw×1) as two separate
   passes, so a disk-full abort after pass 1 still leaves a fully usable
   MIC/PICS dataset rather than an all-or-nothing failure.

---

## 10. Manifest schema

Per-study fragment (`pacs-data/encoded/<id>/mic-manifest.json`,
`ref-manifest.json`), merged by stage 4 into `pacs-data/codec-manifest.json`:

```jsonc
{
  "id": "enh-ct-multiframe",
  "tier": "A",
  "license": "pydicom test data (public sample)",
  "attribution": "pydicom / DICOM WG",
  "artifacts": [
    {
      "codec": "mic",
      "variant": "1state",           // 1state | 4state | 8state | pics4 | pics4_8s | pics8 | pics8_8s
      "container": "MIC2",           // MIC1 | MIC2 | MIC3 | MICR
      "route": "MULTIFRAME_FILE",    // routing bucket from §5, for debugging/audits
      "key": "enh-ct-multiframe/mic/enh-ct-multiframe.mic",
      "bytes": 612480,
      "rawBytes": 1052672,
      "ratio": 1.72,
      "verified": true,
      "sourceSha256": "0a4c3aa0...",
      "pixelChecksum": "fnv1a32:9f2c11ab",   // per-frame array for MIC2/MIC3
      "applicable": true
    },
    {
      "codec": "htj2k",
      "variant": "default",
      "applicable": false,
      "reason": "RGB not supported by reference codec wrapper"
    }
  ],
  "notes": {
    "temporalSkipped": null,
    "refSlicesSampled": null
  }
}
```

Design points:
- `applicable: false` entries are always present (never a missing array
  slot) so the viewer can render a deterministic "N/A" cell instead of
  inferring absence-means-unsupported vs. absence-means-not-yet-encoded.
- `pixelChecksum` is an array (one per frame/tile) for MIC2/MIC3 containers,
  a scalar for MIC1/MICR — matches the per-frame verification granularity
  from §8.
- `route` is carried through purely for operability (answers "why did this
  study end up as MIC3 instead of MIC2" without re-deriving it from the raw
  DICOM tags by hand).

---

## 11. Upload integration (goal item 8)

**Decision: extend `upload_s3.py` in place rather than writing a second
script.** The per-study `meta` dict it already builds (lines 63–70:
`tier`, `modality`, `license`, `attribution`, `transfer-syntax`, `lossy`) is
exactly the metadata that must travel with every derived artifact too — CC-BY
requires attribution on the derived object, not just the source. Concretely:

```python
CODEC_KEYS = ["mic", "pics", "htj2k", "jls", "jxl"]

for i, e in enumerate(entries, 1):
    ...
    meta_arg = ",".join(f"{k}={v}" for k, v in meta.items() if v)   # unchanged, built once

    # existing raw/ sync
    sync(src=raw_src / e["id"], dest=f"s3://{bucket}/{e['id']}/{sub}/", meta_arg=meta_arg)

    # NEW: one sync per non-empty codec prefix, same meta_arg
    for codec in CODEC_KEYS:
        local = encoded_dir / e["id"] / codec
        if local.is_dir() and any(local.iterdir()):
            sync(src=local, dest=f"s3://{bucket}/{e['s3']['codec_prefixes'][codec]}",
                 meta_arg=meta_arg)
```

Reusing the same `meta_arg` string for every sync call (raw + all 5 codec
prefixes) is the whole mechanism that satisfies "derived works carry the same
license" — there is no separate metadata path to keep in sync or forget to
update. `codec-manifest.json` is uploaded to the bucket root alongside the
existing `manifest.json` (same pattern as the existing `manifest.json`
upload at the end of the script), so the viewer discovers both with one
fetch.

A second script was considered and rejected: it would duplicate the `meta`
construction logic, and drift between "how raw/ gets tagged" and "how mic/
gets tagged" is exactly the kind of bug that silently breaks attribution
compliance without failing any test.

---

## 12. Risks, edge cases, mitigations

| Risk | Mitigation |
|---|---|
| Encapsulated transfer syntax (2/80 studies) breaks the Go reader | §4 normalize pre-pass, gated on `TransferSyntaxUID`, not modality |
| PALETTE COLOR pixels compressed as raw indices (wrong semantics) | §4 `apply_color_lut` before handoff, converts to true RGB |
| SM series with mixed tiled/untiled instances in one directory | Routing is per-instance (§5), not per-study; manifest keys artifacts by instance, not just study id |
| MONOCHROME1 (inverted display polarity) mis-handled by codec | No encoder change needed — MIC operates on raw sample values regardless of display polarity; flag `photometric: MONOCHROME1` in the manifest so the *viewer* applies correct windowing |
| Series directory has duplicate/missing `InstanceNumber`s | Add an explicit validation pass (count matches file count, no duplicates) before treating a directory as one ordered volume; fall back to filename-lexical order + warning, don't silently misorder slices |
| Disk exhaustion mid-run | §9 pre-study estimate + hard stop with safety margin; two-pass ordering (MIC/PICS before reference codecs) so a partial run is still useful |
| Peak memory from deep series (658 × 512² × 2B ≈ 345 MB/study) | Worker pool bounded by study count, not file count (§7); default `-workers 4` |
| Reference-codec fan-out on 658-slice series (658×3 files, expensive) | §6 caps reference-codec sampling at 32 evenly-spaced slices for series >100 frames, recorded explicitly in the manifest |
| Roundtrip bug on a specific frame/strip, not frame 0 (already happened once, commit `210e140`) | §8 mandates per-frame/per-tile verification, not just frame/tile 0 |
| A future corpus refresh adds a truly tiled RGB WSI (not present today) | §5's routing tree already defines the COLOR+WSI_TILED branch (`CompressWSI`, channels=3, YCoCg-R) even though no current study exercises it |

---

## 13. Implementation notes — phased rollout

1. **Phase 1** — `mic-pacs-encode` grayscale paths only: SINGLE_FRAME,
   SERIES_DIR, MULTIFRAME_FILE. Covers ~73/80 studies. Zero new codec code —
   pure reuse of `writeMICVariants`, `CompressMultiFrame`,
   `readDicomSeries`/`readDicomMultiFrame`. Highest value, lowest risk;
   ship this first.
2. **Phase 2** — normalize pre-pass (2 studies) + COLOR routing
   (`CompressRGB`/MICR, ~6–7 US studies, including per-frame fan-out for
   color US series).
3. **Phase 3** — WSI_TILED per-instance path (the handful of genuinely
   tiled SM instances in `sm-wsi-mid-9632.4.0`) — the one genuinely new
   piece of logic (assembling a tiled instance's frames via
   `PerFrameFunctionalGroupsSequence` plane positions into one canvas for
   `CompressWSI`). Smallest in scope, but the only phase touching code this
   repo doesn't already have a working example of.
4. **Phase 4** — `mic-pacs-refgen` cgo pass (grayscale artifacts + capped
   per-slice sampling for deep series).
5. **Phase 5** — manifest merge + `upload_s3.py` extension.

Each phase produces a runnable, resumable artifact set on its own — phase 1
alone is already a useful partial dataset for the viewer, which is the point
of ordering by corpus coverage / risk rather than by pipeline stage number.
