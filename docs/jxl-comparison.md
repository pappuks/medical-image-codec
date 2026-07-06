# JPEG XL Comparison — Methodology and Results

## Overview

JPEG XL (ISO/IEC 18181) is the newest royalty-free image codec, offering a
lossless "modular" mode that is highly competitive on continuous-tone and
medical imagery. It has a defined DICOM transfer syntax family and is
increasingly discussed as a successor to JPEG 2000 / JPEG-LS in imaging
workflows. This document describes our in-process comparison of MIC (2-state and
4-state) against JPEG XL lossless using the reference [libjxl](https://github.com/libjxl/libjxl)
library.

Bottom line up front: **JPEG XL produces the smallest files of any codec we test
(geomean 4.18× vs MIC 3.36×, +24%), but MIC decodes ~8× faster and encodes ~50×
faster.** JPEG XL is the ratio champion; MIC is the throughput champion. Which
one wins depends entirely on whether your workload is bounded by storage/network
or by decode latency.

## Why JPEG XL?

JPEG XL is relevant because:

1. **State-of-the-art lossless ratio**: Its modular mode combines an adaptive
   predictor (a weighted, self-correcting blend of several spatial predictors)
   with a context-modeled ANS entropy coder — a strictly richer model than MIC's
   fixed `avg(left, top)` predictor + single FSE distribution, and than JPEG-LS's
   MED predictor + Golomb-Rice coding.
2. **Royalty-free and standardized**: Like MIC's goals, JPEG XL is unencumbered,
   which matters for open medical-imaging pipelines.
3. **Reference maturity**: libjxl is the reference implementation and the basis
   for the `cjxl`/`djxl` tools, so it is the fair thing to benchmark against.

It is therefore the strongest available *ratio* baseline — the natural upper
bound to measure MIC's speed-vs-size trade-off against.

## Benchmark Setup

### Library

- **libjxl 0.11** — installed via `brew install jpeg-xl` (headers in
  `/opt/homebrew/include/jxl`, libs in `/opt/homebrew/lib`)
- Linked via CGO with `#cgo LDFLAGS: -ljxl`
- Build tag: `cgo_ojph` (shared with the HTJ2K and JPEG-LS comparison
  infrastructure)
- Encoder effort: **7** (libjxl's own default; pinned via `JXLDefaultEffort` for
  reproducibility). Distance 0 / `JxlEncoderSetFrameLossless` selects
  mathematically lossless modular mode.

### Bit-depth handling (important)

libjxl rescales sub-16-bit `UINT16` samples by `65535/((1<<depth)-1)` under its
default `JXL_BIT_DEPTH_FROM_PIXEL_FORMAT` setting, which breaks the lossless
roundtrip for the many <16-bit medical images (MR, MG, PET, …). The wrapper
therefore pins **`JXL_BIT_DEPTH_FROM_CODESTREAM` on both sides** —
`JxlEncoderSetFrameBitDepth` on encode and `JxlDecoderSetImageOutBitDepth` on
decode (the latter must be called *after* `JxlDecoderSetImageOutBuffer`). This
passes the true integer samples through unscaled and preserves the actual bit
depth for best compression.

### Methodology

All codecs are invoked as **in-process library calls** via CGO. There is no
subprocess launch, no file I/O, and no serialization overhead. This ensures an
apples-to-apples comparison.

**Compression:**
- MIC 2-state: `mic.CompressSingleFrame(pixels, cols, rows, maxShort)` — Delta+RLE+FSE 2-state pipeline
- MIC 4-state: `DeltaRleCompressU16.Compress` → `mic.FSECompressU16FourState` — Delta+RLE+FSE 4-state pipeline
- JPEG XL: `JXLCompressU16(pixels, cols, rows, bitDepth, JXLDefaultEffort)` — modular lossless

**Decompression:**
- MIC 2-state: `mic.DecompressSingleFrame(compressed, cols, rows)`
- MIC 4-state: `mic.FSEDecompressU16FourState` → `DeltaRleDecompressU16.Decompress`
- JPEG XL: `JXLDecompressU16(compressed, cols, rows)`

**Timing protocol:**
- `BenchmarkAllCodecs` / `BenchmarkAllCodecsEncode` with `-benchtime=10x`
- Throughput reported as MB/s over uncompressed pixel bytes (width × height × 2 bytes)

**Lossless verification:**
Every decompressed output is compared pixel-by-pixel against the original to
confirm bit-exact roundtrip. `TestJXLRoundtrip` provides a standalone roundtrip
check for JPEG XL on all images.

### Test Images

The full 39-image 16-bit greyscale corpus (NEMA WG-04 + GDCM samples + TCIA
diagnostic slices) described in the README "Test Dataset" section. Reference
hardware for throughput: Apple M2 Max (ARM64). Compression ratios are
CPU-independent.

## Results

All numbers below are from `BenchmarkAllCodecs` (decode + ratio) and
`BenchmarkAllCodecsEncode` (encode), `-tags cgo_ojph -benchtime=10x`, on an Apple
M2 Max. MIC 2-state and 4-state produce identical compressed streams, so they
share one ratio column; the 4-state variant is shown for throughput.

### Summary (geometric mean over 39 images)

| Metric | MIC-4state | JPEG-LS | HTJ2K | JPEG XL |
|--------|:----------:|:-------:|:-----:|:-------:|
| Compression ratio | 3.36× | 3.84× | 3.42× | **4.18×** |
| Decompression (MB/s) | **325** | 138¹ | — | 39 |
| Encoding (MB/s) | **226** | — | — | 4.6 |

¹ JPEG-LS decode geomean from [jpegls-comparison.md](./jpegls-comparison.md) on the same machine class.

- **Ratio:** JPEG XL is +24% over MIC and +9% over JPEG-LS (geomean). It has the
  single best ratio on **36 of 39** images among {MIC, HTJ2K, JPEG-LS, JPEG XL}.
- **Decode:** MIC-4state is **8.2× faster** than JPEG XL (per-image range
  3.7–11.9×); MIC-4state-SIMD widens this to ~13× (geomean 496 MB/s).
- **Encode:** MIC-4state is **~50× faster** than JPEG XL's effort-7 encoder
  (per-image range 14–95×).

### Per-image detail (all 39 images)

`JPEG-XL/MIC` is the ratio of compressed-size advantage (higher = JPEG XL packs
tighter). Decode/encode are MB/s over raw pixel bytes.

| Image | MIC ratio | JPEG-XL ratio | JPEG-XL/MIC | MIC-4state dec (MB/s) | JPEG-XL dec (MB/s) | MIC-4state enc (MB/s) | JPEG-XL enc (MB/s) |
|-------|:---------:|:-------------:|:-----------:|:---------------------:|:------------------:|:---------------------:|:------------------:|
| MR | 2.35× | 2.56× | 1.09× | 282 | 29 | 159 | 2.8 |
| CT | 2.24× | 2.61× | 1.17× | 238 | 33 | 179 | 3.6 |
| CR | 3.69× | 4.06× | 1.10× | 341 | 44 | 226 | 6.2 |
| XR | 1.74× | 2.08× | 1.20× | 330 | 30 | 249 | 2.6 |
| MG1 | 8.79× | 9.42× | 1.07× | 500 | 55 | 369 | 7.9 |
| MG2 | 8.77× | 9.41× | 1.07× | 510 | 55 | 368 | 8.0 |
| MG3 | 2.24× | 2.34× | 1.05× | 340 | 29 | 254 | 3.2 |
| MG4 | 3.47× | 4.01× | 1.16× | 440 | 41 | 333 | 4.0 |
| CT1 | 2.79× | 3.29× | 1.18× | 298 | 39 | 209 | 4.9 |
| CT2 | 3.48× | 4.92× | 1.41× | 287 | 42 | 199 | 4.6 |
| MG-N | 2.24× | 2.34× | 1.05× | 348 | 29 | 254 | 3.2 |
| MR1 | 2.09× | 2.34× | 1.12× | 306 | 32 | 224 | 3.9 |
| MR2 | 3.28× | 3.63× | 1.11× | 366 | 42 | 274 | 4.1 |
| MR3 | 3.92× | 4.82× | 1.23× | 438 | 47 | 301 | 6.4 |
| MR4 | 4.12× | 4.86× | 1.18× | 366 | 41 | 242 | 5.5 |
| NM1 | 5.15× | 6.72× | 1.30× | 368 | 46 | 237 | 6.3 |
| RG1 | 1.70× | 1.72× | 1.01× | 293 | 35 | 244 | 2.9 |
| RG2 | 4.23× | 5.33× | 1.26× | 390 | 44 | 273 | 6.7 |
| RG3 | 6.08× | 7.77× | 1.28× | 395 | 55 | 286 | 4.4 |
| SC1 | 3.71× | 5.08× | 1.37× | 397 | 39 | 296 | 5.8 |
| XA1 | 5.01× | 5.58× | 1.11× | 371 | 48 | 247 | 4.7 |
| CT_BRAIN | 3.06× | 4.80× | 1.57× | 239 | 38 | 169 | 4.5 |
| CT_ANKLE | 9.48× | 19.91× | 2.10× | 473 | 56 | 333 | 8.1 |
| CT_ORT | 2.68× | 3.04× | 1.13× | 296 | 37 | 222 | 4.4 |
| CT_CHEST | 2.82× | 3.30× | 1.17× | 251 | 38 | 178 | 4.5 |
| MR_HEAD | 2.61× | 2.96× | 1.13× | 312 | 32 | 169 | 3.5 |
| MR_INTERA | 3.68× | 6.56× | 1.78× | 312 | 40 | 201 | 4.7 |
| DX_HAND | 2.24× | 2.61× | 1.16× | 293 | 36 | 244 | 3.2 |
| DX_CHEST | 1.66× | 1.70× | 1.02× | 287 | 34 | 243 | 3.0 |
| CR_THORAX | 1.82× | 1.91× | 1.05× | 311 | 29 | 249 | 3.1 |
| PET1 | 2.74× | 3.56× | 1.30× | 296 | 37 | 192 | 4.0 |
| PET2 | 3.39× | 5.41× | 1.60× | 285 | 44 | 182 | 5.2 |
| CT_LUNG | 2.73× | 3.28× | 1.20× | 288 | 37 | 215 | 4.4 |
| CT_PANCREAS | 2.36× | 2.72× | 1.15× | 229 | 33 | 189 | 4.0 |
| MR_BRAIN | 7.27× | 8.58× | 1.18× | 389 | 48 | 234 | 6.5 |
| MR_BREAST | 4.01× | 4.64× | 1.16× | 304 | 37 | 202 | 4.5 |
| MR_PROSTATE | 2.30× | 2.82× | 1.23× | 315 | 32 | 201 | 3.7 |
| PET_PSMA | 10.12× | 17.85× | 1.76× | 313 | 53 | 178 | 7.2 |
| PET_LUNG | 2.97× | 6.74× | 2.27× | 175 | 47 | 89 | 6.1 |

### Where JPEG XL's ratio lead is largest

The advantage over MIC ranges from +1% (RG1, DX_CHEST — already near the
noise floor) to **+127%** (PET_LUNG). The biggest wins are on smooth,
low-entropy studies where MIC's fixed predictor and single entropy distribution
leave the most on the table:

- **CT_ANKLE**: 19.91× vs 9.48× (2.10×) — large uniform background.
- **PET_LUNG**: 6.74× vs 2.97× (2.27×), **PET_PSMA**: 17.85× vs 10.12× (1.76×) — PET frames with large near-constant regions and quantized activity.
- **MR_INTERA**: 6.56× vs 3.68× (1.78×), **PET2**: 5.41× vs 3.39× (1.60×), **CT_BRAIN**: 4.80× vs 3.06× (1.57×).

On dense, high-detail images (mammography MG1/MG2, chest DX/CR) the two codecs
are within ~5–10%, because there is less redundancy for the richer model to
exploit.

### Where MIC still wins ratio

Among all codecs in the README ratio table (which also includes the wavelet
pipeline and PICS), JPEG XL has the top ratio on 34/39; JPEG-LS still edges it on
**CT, MG3, MG-N**, and the MIC wavelet pipeline wins **DX_CHEST**. MIC and
JPEG XL tie on RG1 (1.72×).

## Why MIC Is Faster

1. **Table-driven FSE decoder**: MIC performs a single ANS table lookup per
   symbol. JPEG XL's modular decoder runs a per-pixel adaptive predictor plus a
   context-modeled ANS decode with a much larger, adaptively-updated context
   model — many more operations and data dependencies per pixel.
2. **4-state parallel decode (ILP)**: MIC's 4-state FSE decoder interleaves 4
   independent ANS state machines to hide latency via instruction-level
   parallelism. JPEG XL's decode is inherently more sequential.
3. **Branch-free delta + RLE fast-path**: MIC's interior loop is branchless and
   long constant runs skip the entropy decoder entirely — common in smooth
   medical regions.
4. **Asymmetric by design**: JPEG XL's effort-7 encoder *analyzes* the image
   (predictor selection, context clustering) to reach its ratio, which is why
   encoding is ~50× slower than MIC. MIC's encoder is a near-symmetric single
   pass.

## Why JPEG XL Compresses Better

1. **Adaptive weighted predictor**: JPEG XL's modular mode blends several spatial
   predictors with self-correcting per-pixel weights, adapting to local edge
   orientation far better than MIC's fixed `avg(left, top)`.
2. **Context modeling**: Its ANS coder conditions symbol probabilities on a rich
   local context (many clusters), versus MIC's single global FSE distribution —
   a large advantage on heterogeneous images (PET, CT with big uniform regions).
3. **Larger neighborhood + squeeze**: The modular transform can exploit
   longer-range redundancy than a 2-neighbor delta, which is why its lead is
   largest on smooth, low-entropy studies.

## When to Use Which

- **Choose MIC** when decode latency or throughput dominates: interactive DICOM
  rendering, PACS retrieval, streaming/scroll, browser/WASM viewers, or any
  encode-once-decode-many workload. MIC decodes ~8× faster (single-thread) and
  its PICS strips scale to multiple GB/s.
- **Choose JPEG XL** when minimizing stored/transferred bytes is paramount and
  decode/encode cost is acceptable: cold archival, bandwidth-constrained
  transfer, or when +9–24% smaller files justify ~8× slower decode and ~50×
  slower encode.

## Source Files

| File | Purpose |
|------|---------|
| `ojph/jxl_wrapper.h` | C header for the libjxl wrapper functions |
| `ojph/jxl_wrapper.c` | C wrapper around the libjxl encoder/decoder API (bit-depth pinning) |
| `ojph/jxl.go` | Go CGO bindings (`JXLCompressU16`, `JXLDecompressU16`, `JXLDefaultEffort`) |
| `ojph/jxl_comparison_test.go` | `TestJXLComparison`, `TestJXLRoundtrip`, `BenchmarkJXLDecomp`, `BenchmarkJXLEncode` |
| `ojph/mic_c_test.go` | `BenchmarkAllCodecs` / `BenchmarkAllCodecsEncode` — full multi-codec benchmark including the `JXL` column |
| `paper-tables.py` | Renders the `JPEG-XL` column into the ratio / encode / decode tables |

## Running the Comparison

```bash
# Prerequisite: libjxl installed
brew install jpeg-xl          # macOS; headers in /opt/homebrew/include/jxl

# Standalone comparison: MIC 2-state + 4-state + JPEG XL (ratio + speed + verification)
go test -tags cgo_ojph -v -run TestJXLComparison ./ojph/ -timeout 600s

# Verify lossless roundtrip on all test images
go test -tags cgo_ojph -v -run TestJXLRoundtrip ./ojph/

# Per-image decode / encode benchmarks
go test -tags cgo_ojph -run=^$ -bench=BenchmarkJXLDecomp ./ojph/ -benchtime=10x
go test -tags cgo_ojph -run=^$ -bench=BenchmarkJXLEncode ./ojph/ -benchtime=10x

# Full codec comparison (JPEG XL appears as the JXL column alongside HTJ2K/JPEG-LS)
go test -tags cgo_ojph -run=^$ -bench=BenchmarkAllCodecs ./ojph/ -benchtime=10x
go test -tags cgo_ojph -run=^$ -bench=BenchmarkAllCodecsEncode ./ojph/ -benchtime=10x
```

> Note: JPEG XL effort-7 encoding is slow (~2 min for the full 39-image suite at
> `-benchtime=1x`; ~20 min at `-benchtime=10x`). This is included in
> `run-paper-benchmarks.sh` and dominates its encode-side runtime.

## Conclusion

JPEG XL is the strongest lossless *ratio* baseline available for 16-bit medical
imaging: geomean 4.18× (+24% over MIC, +9% over JPEG-LS), and up to 2.3× smaller
than MIC on smooth low-entropy studies. That ratio comes from an adaptive
weighted predictor and context-modeled ANS coder — and it is paid for in speed.
MIC decodes ~8× faster (single-thread; ~13× with SIMD, far more with PICS
strips) and encodes ~50× faster. For throughput-sensitive clinical workflows —
real-time rendering, PACS retrieval, browser delivery — MIC remains the better
choice; for storage- or bandwidth-bound archival where files are written once and
read rarely, JPEG XL's smaller output is compelling.
