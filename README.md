# MIC — Medical Image Codec

A **lossless compression codec for 16-bit DICOM images** with implementations in Go, C/SIMD assembly, and browser-native JavaScript/WebAssembly. MIC beats HTJ2K (High-Throughput JPEG 2000) in decompression speed on all tested modalities while matching or exceeding its compression ratio on 7 of 8 — packaged in ~500 lines of core Go and a ~19 KB zero-dependency JS decoder.

The key insight: DICOM stores pixels at 10–16 bits per sample, but every mainstream codec (Zstandard, JPEG 2000, JPEG-LS) was designed for 8-bit byte streams. Treating a 16-bit residual as two bytes discards the mutual information between them, inflating compressed output by 10–22%. MIC's 16-bit-native RLE+FSE pipeline closes that gap directly.

| Branch | Status |
|--------|--------|
| main | ![CI](https://github.com/pappuks/medical-image-codec/actions/workflows/go.yml/badge.svg) |

## Why MIC?

| Property | Value |
|----------|-------|
| Compression ratio | 1.7× – 8.9× greyscale; 3–5× RGB tissue tiles (lossless) |
| vs. HTJ2K ratio | Matches or exceeds on 7/8 modalities (wavelet pipeline) |
| Peak decompression speed | up to 16 GB/s (ARM64, 64 cores); ~7.5 GB/s geometric mean |
| vs. HTJ2K speed | Faster on all modalities (single-thread); PICS-C-8 exceeds HTJ2K on all 39 test images |
| vs. JPEG-LS | Consistently faster decompression; better or equal ratio |
| vs. Delta+Zstandard | ~10% better compression ratio (geometric mean; 16-bit alphabet advantage) |
| Implementations | Pure Go · C + pthreads + BMI2/NEON SIMD · JavaScript ES module · Go WebAssembly |
| Browser throughput | up to 559 MB/s (14-core M4 Pro, PICS parallel strips via `worker_threads`) |
| Supported formats | 8–16 bit greyscale; 8-bit RGB (single-frame US/VL + WSI/pathology tiled) |
| Multi-frame support | MIC2 container (random access or temporal prediction) |
| WSI support | MIC3 tiled container with pyramid levels and parallel encode/decode |
| Footprint | ~19 KB JS decoder (zero dependencies); no native libs required for browser use |

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Compression Pipeline](#compression-pipeline)
3. [Formats](#formats)
   - [MIC2 — Multi-Frame](#mic2--multi-frame)
   - [MIC3 — Whole Slide Imaging](#mic3--whole-slide-imaging)
   - [PICS — Parallel Single-Image Compression](#pics--parallel-single-image-compression)
4. [Compression Results](#compression-results)
5. [Performance](#performance)
6. [Browser Decoder](#browser-decoder)
7. [CLI Reference](#cli-reference)
8. [Documentation](#documentation)
9. [Roadmap](#roadmap)

---

## Quick Start

```bash
# Build the CLI tool
go build -o mic-compress ./cmd/mic-compress/

# Compress a single-frame DICOM
./mic-compress -dicom scan.dcm -output scan.mic

# Compress a multi-frame DICOM (e.g., Breast Tomosynthesis)
./mic-compress -dicom tomo.dcm -output tomo.mic

# Run all tests
go test -v ./...
```

See the [Developer Guide](./docs/developer-guide.md) for the full test and benchmark command reference.

---

## Compression Pipeline

MIC chains four stages to compress 16-bit medical images:

```
Raw 16-bit Pixels
       │
       ▼
┌──────────────────────────────────────────┐
│           Delta Encoding                 │
│  Each pixel → value − avg(top, left)     │
│  Large diffs stored with an escape code  │
│  derived from the image bit depth        │
└─────────────────┬────────────────────────┘
                  │
                  ▼
┌──────────────────────────────────────────┐
│               RLE                        │
│  Same runs:  count + one repeated value  │
│  Diff runs:  count + N distinct values   │
│  Min run = 3 → output never larger       │
└─────────────────┬────────────────────────┘
                  │
          ┌───────┴───────┐
          │               │
          ▼               ▼
   ┌────────────┐  ┌────────────────┐
   │    FSE     │  │  Can. Huffman  │
   │ (ANS-based)│  │  (depth ≤ 14)  │
   │ Best speed │  │  Best ratio    │
   └─────┬──────┘  └───────┬────────┘
         └────────┬────────┘
                  │
                  ▼
          Compressed .mic file
```

> **Recommended:** Use `Delta + RLE + FSE` for production — it gives the best decompression throughput.
> Use `Delta + RLE + Huffman` if you need the smallest possible file size.

An alternative 5/3 integer wavelet pipeline (`WaveletV2SIMDRLEFSECompressU16`) is also available. It achieves better compression ratios than Delta+RLE+FSE on 7 of 8 modalities and matches or beats HTJ2K on the same set. See [docs/wavelet-fse-analysis.md](./docs/wavelet-fse-analysis.md).

---

## Formats

### MIC2 — Multi-Frame

MIC2 is a container format for multi-frame DICOM images (e.g., Breast Tomosynthesis).

**Two compression modes:**

| Mode | Frame 0 | Frames 1..N | Use when |
|------|---------|-------------|----------|
| Independent | Delta+RLE+FSE | Delta+RLE+FSE | Default; random access to any frame |
| Temporal | Delta+RLE+FSE | ZigZag(residual)+RLE+FSE | High inter-frame correlation (cine MRI, fluoroscopy) |

**Go API:**

```go
// Compress a multi-frame image
compressed, err := mic.CompressMultiFrame(frames, width, height, maxValue, mic.ModeIndependent)

// Random access to a single frame
frame, err := mic.DecompressFrame(compressed, frameIndex)
```

For format specification and benchmark results, see [docs/architecture.md](./docs/architecture.md).

---

### MIC3 — Whole Slide Imaging

MIC3 is a tiled container format for whole slide images (WSI) used in digital pathology. It extends the pipeline to handle RGB images via a reversible YCoCg-R color transform, with tiled random access and multi-resolution pyramid levels.

**Key features:**

- **Tiled architecture** — 256×256 tiles for O(1) random access
- **Pyramid levels** — multi-resolution levels via 2×2 box filter downsampling
- **Parallel compression** — goroutine worker pool for concurrent tile encode/decode
- **Constant-plane optimization** — background tiles compress to 15–17 bytes total

**Go API:**

```go
// Compress a full-resolution RGB image
compressed, err := mic.CompressWSI(pixels, width, height, 3, 8, mic.WSIOptions{
    TileWidth:  256,
    TileHeight: 256,
    Workers:    8,
})

// Decompress a single tile (O(1) random access)
tile, err := mic.DecompressWSITile(compressed, level, tileX, tileY)

// Decompress an arbitrary region across tiles
region, err := mic.DecompressWSIRegion(compressed, level, x, y, w, h)
```

For format specification, see [docs/architecture.md](./docs/architecture.md).

---

### Single-Frame RGB — `CompressRGB` / `DecompressRGB`

For single-frame RGB images (Ultrasound, Visible Light, fluoroscopy overlays) that don't need the tiled WSI container, MIC provides a lightweight direct API:

```go
// Compress an 8-bit interleaved RGB image (RGBRGB...)
compressed, err := mic.CompressRGB(rgbBytes, width, height)

// Decompress back to RGBRGB... bytes
rgbBytes, err := mic.DecompressRGB(compressed, width, height)
```

The pipeline is identical to WSI tile compression — YCoCg-R color transform followed by Delta+RLE+FSE on each of the three resulting planes — but without any tiling, pyramid, or container overhead. The output blob is a plain three-plane stream:

```
[Y_len  uint32 LE]
[Co_len uint32 LE]
[Cg_len uint32 LE]
[Y  plane blob  ]  (Delta+RLE+FSE)
[Co plane blob  ]  (Delta+RLE+FSE, ZigZag-mapped chrominance)
[Cg plane blob  ]  (Delta+RLE+FSE, ZigZag-mapped chrominance)
```

**Important:** `CompressRGB` operates on the full image without tiling. Using `CompressWSI` for US/VL images (which tiles into 256×256 blocks) breaks spatial correlation across tile boundaries — the delta predictor restarts at each tile corner — and reduces compression ratios by 30–45%. Always use `CompressRGB` for single-frame images.

For browser delivery, the `mic-compress -testdata` tool wraps the blob in a lightweight **MICR container** (magic `MICR`, 12-byte header) that the JS/WASM decoder can identify:

```
MICR format:
  Bytes 0-3:  Magic "MICR" (0x4D 0x49 0x43 0x52)
  Bytes 4-7:  Width  (uint32 LE)
  Bytes 8-11: Height (uint32 LE)
  Bytes 12+:  CompressRGB blob ([Y_len][Co_len][Cg_len][Y_data][Co_data][Cg_data])
```

**Compression ratios on NEMA compsamples RGB images** (lossless, Delta+RLE+FSE with YCoCg-R):

| Image | Dimensions | Raw (MB) | Compressed (MB) | Ratio |
|-------|:----------:|:--------:|:---------------:|:-----:|
| US1 (Ultrasound) | 640×480 | 0.88 | 0.14 | **6.24×** |
| VL1 (Visible Light) | 756×486 | 1.05 | 0.31 | **3.41×** |
| VL2 | 756×486 | 1.05 | 0.33 | **3.23×** |
| VL3 | 756×486 | 1.05 | 0.30 | **3.46×** |
| VL4 | 2226×1868 | 11.9 | 6.41 | **1.86×** |
| VL5 | 2670×3340 | 25.5 | 16.3 | **1.56×** |
| VL6 | 756×486 | 1.05 | 0.54 | **1.93×** |

US1 (ultrasound) achieves 6.24× because ultrasound frames have large uniform regions and limited color range. VL images are full-color natural photography and compress at 1.6–3.5×, consistent with typical photographic content. YCoCg-R alone contributes ~1.2–1.5× uplift by decorrelating the RGB channels before entropy coding.

Benchmarks across all 8 pipeline variants for US/VL images are in `BenchmarkRGBDeltaRLEFSECompress` et al. in [rgbbench_test.go](rgbbench_test.go):

```bash
# Single best pipeline (Delta+RLE+FSE with YCoCg-R)
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkRGBDeltaRLEFSECompress$ mic

# All 8 pipeline variants on RGB images
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkRGB mic
```

---

### PICS — Parallel Single-Image Compression

PICS (Parallel Image Compressed Strips) divides a single image into N horizontal strips, each compressed and decompressed independently using all available CPU cores.

The Delta+RLE+FSE pipeline has a sequential dependency within rows, but rows in one strip are independent of all other strips, so strips compress fully in parallel:

```
┌─────────────────────────────┐
│  Strip 0: rows   0 …  H/N  │  goroutine 0
├─────────────────────────────┤
│  Strip 1: rows H/N … 2H/N  │  goroutine 1
├─────────────────────────────┤
│           ⋮                  │  ⋮
└─────────────────────────────┘
       ↓ concurrent compress / decompress ↓
       PICS blob  →  pixel-exact output
```

**Go API:**

```go
// Compress with N goroutines (0 = auto, uses GOMAXPROCS)
blob, err := mic.CompressParallelStrips(pixels, width, height, maxValue, 0)

// Decompress (all strips run concurrently)
pixels, width, height, err := mic.DecompressParallelStrips(blob)
```

For throughput scaling numbers, the C/pthreads API, and format specification, see [docs/parallel-strips.md](./docs/parallel-strips.md).

---

## Compression Results

All images are 16-bit greyscale DICOM (39-image corpus). Compression ratios are CPU-independent; values below are from the `BenchmarkAllCodecs` run (`-tags cgo_ojph`, Apple M4 Pro reference).

| Image | Raw (MB) | MIC | MIC-4state | Wavelet | PICS-4 | PICS-8 | HTJ2K | JPEG-LS |
|-------|:--------:|:---:|:----------:|:-------:|:------:|:------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 2.35× | 2.35× | 2.38× | 2.28× | 2.21× | 2.38× | **2.52×** |
| CT (512×512) | 0.50 | 2.24× | 2.24× | 1.67× | 2.15× | 1.96× | 1.77× | **2.68×** |
| CR (2140×1760) | 7.18 | 3.69× | 3.69× | 3.81× | 3.70× | 3.71× | 3.77× | **3.96×** |
| XR (2048×2577) | 10.1 | 1.74× | 1.74× | **1.76×** | 1.75× | 1.75× | 1.67× | **1.76×** |
| MG1 (2457×1996) | 9.35 | 8.78× | 8.78× | 8.66× | 8.84× | 8.87× | 8.25× | **8.91×** |
| MG2 (2457×1996) | 9.35 | 8.77× | 8.77× | 8.65× | 8.83× | 8.85× | 8.24× | **8.90×** |
| MG3 (4774×3064) | 27.3 | 2.24× | 2.24× | 2.31× | 2.31× | 2.33× | 2.22× | **2.38×** |
| MG4 (4096×3328) | 26.0 | 3.47× | 3.47× | 3.59× | 3.58× | 3.62× | 3.51× | **3.71×** |
| CT1 (512×512) | 0.50 | 2.79× | 2.79× | 2.48× | 2.54× | 2.29× | 2.70× | **3.19×** |
| CT2 (512×512) | 0.50 | 3.48× | 3.48× | 2.87× | 3.11× | 2.72× | 3.29× | **4.54×** |
| MG-N (3064×4664) | 27.3 | 2.24× | 2.24× | 2.32× | 2.31× | 2.34× | 2.23× | **2.38×** |
| MR1 (512×512) | 0.50 | 2.09× | 2.09× | 2.14× | 2.10× | 2.08× | 2.13× | **2.30×** |
| MR2 (1024×1024) | 2.00 | 3.28× | 3.28× | 3.34× | 3.31× | 3.31× | 3.35× | **3.52×** |
| MR3 (512×512) | 0.50 | 3.92× | 3.92× | 4.09× | 3.89× | 3.83× | 4.33× | **4.51×** |
| MR4 (512×512) | 0.50 | 4.12× | 4.12× | 4.18× | 4.09× | 4.03× | 4.21× | **4.49×** |
| NM1 (256×1024) | 0.50 | 5.15× | 5.15× | 5.01× | 5.25× | 5.28× | 5.76× | **6.28×** |
| RG1 (1841×1955) | 6.86 | 1.70× | 1.70× | 1.70× | 1.70× | 1.69× | 1.63× | **1.72×** |
| RG2 (1760×2140) | 7.18 | 4.23× | 4.23× | 4.32× | 4.28× | 4.30× | 4.32× | **4.51×** |
| RG3 (1760×1760) | 5.91 | 6.08× | 6.08× | 6.82× | 6.11× | 6.12× | 6.99× | **7.31×** |
| SC1 (2048×2487) | 9.71 | 3.71× | 3.71× | 3.70× | 3.73× | 3.73× | 3.85× | **4.73×** |
| XA1 (1024×1024) | 2.00 | 5.01× | 5.01× | 4.94× | 5.04× | 5.03× | 4.88× | **5.39×** |
| CT_BRAIN (512×512) | 0.50 | 3.06× | 3.06× | 2.89× | 2.77× | 2.47× | 3.39× | **4.28×** |
| CT_ANKLE (512×512) | 0.50 | **9.48×** | **9.48×** | 5.02× | 9.30× | 9.14× | 4.97× | 6.87× |
| CT_ORT (512×512) | 0.50 | 2.68× | 2.68× | 2.38× | 2.51× | 2.26× | 2.56× | **3.00×** |
| CT_CHEST (400×512) | 0.39 | 2.82× | 2.82× | 2.10× | 2.52× | 2.21× | 2.23× | **3.22×** |
| MR_HEAD (256×256) | 0.13 | 2.61× | 2.61× | 2.65× | 2.57× | 2.53× | 2.67× | **2.86×** |
| MR_INTERA (1024×1024) | 2.00 | 3.68× | 3.68× | 4.05× | 3.75× | 3.78× | 5.04× | **5.08×** |
| DX_HAND (1480×1410) | 3.98 | 2.24× | 2.24× | 2.35× | 2.26× | 2.25× | 2.37× | **2.48×** |
| DX_CHEST (1416×1420) | 3.84 | 1.66× | 1.66× | **1.82×** | 1.65× | 1.63× | 1.63× | 1.70× |
| CR_THORAX (2076×1876) | 7.43 | 1.82× | 1.82× | 1.81× | 1.84× | 1.83× | 1.81× | **1.90×** |
| PET1 (256×256) | 0.13 | 2.74× | 2.74× | 2.81× | 2.64× | 2.52× | 3.02× | **3.21×** |
| PET2 (256×256) | 0.13 | 3.38× | 3.38× | 3.48× | 3.16× | 2.58× | 4.37× | **4.74×** |
| CT_LUNG (512×512) | 0.50 | 2.73× | 2.73× | 2.45× | 2.50× | 2.25× | 2.67× | **3.15×** |
| CT_PANCREAS (512×512) | 0.50 | 2.36× | 2.36× | 1.76× | 2.18× | 1.98× | 1.85× | **2.70×** |
| MR_BRAIN (320×256) | 0.16 | 7.26× | 7.26× | 6.75× | 7.31× | 7.24× | 7.62× | **8.46×** |
| MR_BREAST (256×256) | 0.13 | 4.01× | 4.01× | 3.94× | 4.00× | 3.91× | 4.10× | **4.48×** |
| MR_PROSTATE (320×320) | 0.20 | 2.30× | 2.30× | 2.34× | 2.28× | 2.26× | 2.49× | **2.65×** |
| PET_PSMA (200×200) | 0.08 | 10.11× | 10.11× | 7.67× | 1.20× | 1.18× | 13.92× | **15.78×** |
| PET_LUNG (200×200) | 0.08 | 2.97× | 2.97× | 2.97× | 1.00× | 1.03× | 5.21× | **5.62×** |

MIC and MIC-4state encode identically — the 4-state variant only unlocks a faster decoder. PICS strips compress independently, which slightly reduces ratio on small images (MR, CT) and can fall back to near-raw on the smallest 200×200 PET frames, but improves ratio on large CR/MG images where strip-local FSE table adaptation helps. JPEG-LS achieves the highest ratio on most images (37 of 39; MIC wins CT_ANKLE and the wavelet pipeline wins DX_CHEST) but at 3–6× lower decompression throughput (see Performance table below).

`CompressSingleFrameGapRemoval` adds +0.45% on CT (2.237× → **2.247×**) by collapsing the 65536-symbol RLE alphabet to the 1782 symbols that actually occur, via a delta-encoded expand map (1798 bytes overhead). Other modalities are unaffected. See [docs/compression-results.md](./docs/compression-results.md).

**Predictor ablation (39 images, full pipeline):** Four predictors were evaluated — left-only, avg (MIC default), Paeth, and MED (JPEG-LS):

| Predictor | Geo. Mean Ratio | Wins (39 imgs) | vs. Avg |
|-----------|:--------------:|:--------------:|:-------:|
| Left-only | 3.22× | 2/39 | -4.4% |
| **Avg (MIC default)** | **3.36×** | **11/39** | baseline |
| Paeth | 3.39× | 0/39 | +0.8% |
| MED (JPEG-LS) | 3.44× | 26/39 | +2.4% |

No single predictor dominates across all modalities. The avg predictor is best for mammography (MG1/MG2) and several MR studies; Paeth and MED improve on CT1/CT2, MR3, MR4, SC1, RG3, and the new CT studies (CT_ANKLE up to +32%) but regress on NM1 (−8%), MG-N, and the PET_PSMA/MR_BRAIN studies. MED provides the best overall geomean at the cost of 1.5–2× slower decompression due to its three-way conditional branch.

**TableLog ablation (39 images, full pipeline):** The adaptive tableLog selection bumps from 11→12 or 11→13 for 15 of 39 images (those with many distinct residual symbols and sufficient data per symbol — notably CR, MG1/MG2, RG2, RG3, XA1, MR2, MR3, NM1, and several added MR studies), gaining 0.2–9.9% compression ratio. Decompression speed is unaffected by tableLog choice (variation <5%, within measurement noise). See `TestTableLogAblation` in [ablation_test.go](ablation_test.go) for full data.

For predictor comparisons (MED, Zstandard) and WSI results, see [docs/compression-results.md](./docs/compression-results.md).

---

## Performance

> **Note:** The per-image throughput tables below are reference values measured on the original 21-image corpus on the machines named in each header (Apple M2 Max, Intel Core Ultra 9 285K). They have not been regenerated for the 39-image corpus. The current paper-quality single-threaded throughput numbers (Apple M4 Pro / AWS c8i, all 39 images on both ARM64 and AMD64) live in the paper tables and in `results/<timestamp>/paper-tables.txt` from `./run-paper-benchmarks.sh`.

**Decompression throughput** (MB/s) — Apple M2 Max (ARM64), `BenchmarkAllCodecs` (`-tags cgo_ojph`, `-benchtime=10x`). C variants compiled with `-O3` (no `-march=native` on ARM64; `MIC-4state-SIMD` = `MIC-4state-C` scalar fallback — no AVX2). PICS-C-N uses C pthreads with N concurrent threads. ⊕ Pure-Go `DecompressParallelStrips` (no CGO) achieves ~60–70% of PICS-C throughput.

| Image | Raw (MB) | MIC-Go | MIC-4state | MIC-4state-C | MIC-4state-SIMD | Wavelet+SIMD | PICS-C-2 | PICS-C-4 | PICS-C-8 | HTJ2K | JPEG-LS |
|-------|:--------:|:------:|:----------:|:------------:|:---------------:|:------------:|:--------:|:--------:|:--------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 144 | 207 | 348 | _377_ | 248 | 530 | **710** | 482 ⚠ | 265 | 102 |
| CT (512×512) | 0.50 | 191 | 245 | 356 | _372_ | 316 | 524 | 955 | **1092** | 307 | 137 |
| CR (2140×1760) | 7.18 | 296 | 338 | 524 | 529 | _567_ | 867 | 1635 | **2661** | 367 | 153 |
| XR (2048×2577) | 10.1 | 308 | 341 | 533 | 532 | _627_ | 874 | 1666 | **3025** | 334 | 108 |
| MG1 (2457×1996) | 9.35 | 482 | 514 | 683 | 682 | 678 | 1205 | 2112 | **3656** | _810_ | 409 |
| MG2 (2457×1996) | 9.35 | 479 | 514 | 686 | 684 | 697 | 1225 | 2120 | **3773** | _790_ | 416 |
| MG3 (4774×3064) | 27.3 | 308 | 347 | _531_ | _531_ | 422 | 864 | 1673 | **3117** | 338 | 153 |
| MG4 (4096×3328) | 26.0 | 417 | 444 | 625 | _624_ | 516 | 1093 | 2004 | **3689** | 548 | 184 |
| CT1 (512×512) | 0.50 | 239 | 289 | 436 | _436_ | 425 | 686 | 1013 | **1183** | 362 | 182 |
| CT2 (512×512) | 0.50 | 238 | 281 | 439 | 442 | _481_ | 676 | 1041 | **1189** | 375 | 175 |
| MG-N (3064×4664) | 27.3 | 316 | 352 | 536 | _537_ | 468 | 883 | 1711 | **3175** | 340 | 153 |
| MR1 (512×512) | 0.50 | 278 | 325 | 521 | _527_ | 435 | 751 | 1207 | **1402** | 325 | 116 |
| MR2 (1024×1024) | 2.00 | 333 | 373 | 563 | _568_ | 498 | 913 | 1552 | **2466** | 388 | 172 |
| MR3 (512×512) | 0.50 | 375 | 417 | 639 | _643_ | 507 | 908 | 1430 | **1614** | 441 | 236 |
| MR4 (512×512) | 0.50 | 316 | 362 | 571 | _574_ | 479 | 818 | 1341 | **1558** | 406 | 197 |
| NM1 (256×1024) | 0.50 | 327 | 375 | _632_ | 624 | 575 | 888 | 1400 | **1679** | 410 | 210 |
| RG1 (1841×1955) | 6.86 | 235 | 298 | 406 | 408 | _584_ | 602 | 1128 | **2017** | 332 | 104 |
| RG2 (1760×2140) | 7.18 | 367 | 402 | 590 | 594 | _644_ | 986 | 1803 | **3194** | 443 | 193 |
| RG3 (1760×1760) | 5.91 | 374 | 410 | 604 | 610 | _656_ | 1035 | 1944 | **3302** | 562 | 246 |
| SC1 (2048×2487) | 9.71 | 375 | 405 | 587 | _588_ | 388 | 1017 | 1861 | **3279** | 401 | 229 |
| XA1 (1024×1024) | 2.00 | 331 | 371 | _576_ | 575 | 459 | 928 | 1583 | **2493** | 419 | 204 |

MIC-4state-C/SIMD and PICS-C require CGO (`-tags cgo_ojph`); MIC-Go, MIC-4state, and Wavelet+SIMD are pure Go. _Italic_ = best single-threaded throughput per row. **Bold** = best PICS-C throughput per row. ⚠ MR (256×256) too small for PICS-C-8 — thread overhead dominates; PICS-C-4 is best. ⚠ On ARM64, `MIC-4state-SIMD` falls back to scalar C (`NO_SIMD_AVAILABLE`); numbers ≈ `MIC-4state-C`. PICS-C-8 beats HTJ2K on all 21 images; MIC-4state-SIMD beats HTJ2K on 17/21 images single-threaded.

**Isolated FSE multi-state speedup** (`BenchmarkFSEDecompress4State`, all 21 images): On ARM64 (Apple M2 Max, pure Go), the four-state FSE decoder achieves a geometric mean of **+68% throughput** over the single-state decoder (2-state: +37%). On AMD64 (Intel Core Ultra 9 285K), where the 4-state uses the BMI2 assembly kernel (`fse4StateDecompKernel`), the geomean is **+43%** (2-state: +34%). The CR image shows a large gain on both platforms driven by the `zeroBits` safe-path: +590% on ARM64 (pure-Go 1-state stalls to 140 MB/s) vs +127% on AMD64 (BMI2 assembly 4-state bypasses the slow path efficiently).

| Platform | 1-state geomean | 2-state geomean | 4-state geomean | 4 vs. 1 speedup |
|:--------:|:---------------:|:---------------:|:---------------:|:---------------:|
| ARM64 — Apple M2 Max (pure Go) | 1,418 MB/s | 1,945 MB/s | 2,375 MB/s | **+68%** |
| AMD64 — Intel Core Ultra 9 285K (1/2-state pure Go; 4-state BMI2 asm) | 2,309 MB/s | 3,091 MB/s | 3,306 MB/s | **+43%** |

Run with: `go test -benchmem -run=^$ -benchtime=30x -bench ^BenchmarkFSEDecompress4State$ mic`

---

**Decompression throughput** (MB/s) — Intel Core Ultra 9 285K (AMD64, 24 P-cores), `BenchmarkAllCodecs` (`-tags cgo_ojph`, C variants compiled with `-O3 -march=native`). Wavelet+SIMD from a separate `BenchmarkWaveletV2SIMDRLEFSECompress` run (pure Go). PICS-C-N uses C pthreads + SIMD auto-detect inner decoder with N concurrent threads. ⊕ Pure-Go `DecompressParallelStrips` (no CGO) achieves ~60–70% of PICS-C throughput.

| Image | Raw (MB) | MIC-Go | MIC-4state | MIC-4state-C | MIC-4state-SIMD | Wavelet+SIMD | PICS-C-2 | PICS-C-4 | PICS-C-8 | HTJ2K | JPEG-LS |
|-------|:--------:|:------:|:----------:|:------------:|:---------------:|:------------:|:--------:|:--------:|:--------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 251 | 260 | 501 | _708_ | 194 | **340** | 301 | 124 ⚠ | 570 | 129 |
| CT (512×512) | 0.50 | 231 | 243 | 403 | 487 | 364 | 534 | **676** | 339 | _544_ | 146 |
| CR (2140×1760) | 7.18 | 324 | 381 | 599 | _744_ | 723 | 908 | 1738 | **2435** | 708 | 177 |
| XR (2048×2577) | 10.1 | 363 | 375 | 601 | _803_ | 681 | 982 | 1714 | **1994** | 570 | 112 |
| MG1 (2457×1996) | 9.35 | 617 | 630 | 789 | 1119 | 746 | 1511 | 1872 | **2514** | _1235_ | 471 |
| MG2 (2457×1996) | 9.35 | 562 | 598 | 800 | 1166 | 848 | 1397 | **2244** | 2085 | _1297_ | 471 |
| MG3 (4774×3064) | 27.3 | 357 | 400 | 633 | 669 | _678_ | 1078 | 1823 | **2538** | 644 | 159 |
| MG4 (4096×3328) | 26.0 | 523 | 559 | 743 | 773 | 808 | 1612 | 2124 | **2707** | _916_ | 204 |
| CT1 (512×512) | 0.50 | 322 | 293 | 520 | _676_ | 525 | 636 | **857** | 423 | 657 | 184 |
| CT2 (512×512) | 0.50 | 293 | 295 | 514 | 636 | _705_ | 645 | **847** | 687 | 627 | 187 |
| MG-N (3064×4664) | 27.3 | 368 | 416 | 635 | 669 | _705_ | 1128 | 1872 | **2191** | 643 | 159 |
| MR1 (512×512) | 0.50 | 356 | 351 | 609 | _766_ | 532 | 645 | **945** | 366 | 654 | 128 |
| MR2 (1024×1024) | 2.00 | 352 | 409 | 658 | _975_ | 601 | 963 | **1121** | 793 | 749 | 190 |
| MR3 (512×512) | 0.50 | 450 | 443 | 728 | _809_ | 676 | 786 | **920** | 705 | 802 | 258 |
| MR4 (512×512) | 0.50 | 390 | 339 | 596 | _861_ | 738 | 528 | 493 | **701** | 660 | 219 |
| NM1 (256×1024) | 0.50 | 367 | 339 | _717_ | 668 | 715 | 663 | **783** | 405 | 627 | 244 |
| RG1 (1841×1955) | 6.86 | 302 | 344 | 497 | 593 | _705_ | 787 | 1248 | **1494** | 557 | 103 |
| RG2 (1760×2140) | 7.18 | 433 | 472 | 676 | _861_ | 811 | 1160 | 1841 | **1912** | 823 | 220 |
| RG3 (1760×1760) | 5.91 | 460 | 464 | 706 | 858 | 881 | 1309 | **1969** | 1613 | _894_ | 287 |
| SC1 (2048×2487) | 9.71 | 464 | 478 | 695 | _888_ | 710 | 1169 | 1819 | **1889** | 728 | 248 |
| XA1 (1024×1024) | 2.00 | 413 | 415 | 693 | _836_ | 538 | 1035 | 1173 | **1513** | 797 | 260 |

MIC-4state-C/SIMD and PICS-C require CGO (`-tags cgo_ojph`); MIC-Go, MIC-4state, and Wavelet+SIMD are pure Go. _Italic_ = best single-threaded throughput per row. **Bold** = best PICS-C throughput per row. ⚠ MR (256×256) is too small for multi-threading. PICS-C-8 shows diminishing returns for highly compressed (MG2, RG3) or small (0.5 MB) images — use PICS-C-4 instead. PICS-C uses C pthreads + SIMD auto-detecting inner decoder with only **1 output-buffer allocation** vs Go PICS which allocates per-strip intermediate buffers. Notable: with `-O3 -march=native`, MIC-4state-SIMD beats HTJ2K on 18/21 images single-threaded; PICS-C-8 beats HTJ2K on all 21 images.

**Encoding (compression) throughput** (MB/s) — Apple M2 Max (ARM64), `BenchmarkAllCodecsEncode` (`-tags cgo_ojph`, `-benchtime=10x`). All codecs run in-process; no subprocess or file-I/O overhead. MIC-4state-C and MIC-C are C encoders via CGO; all others are pure Go.

```
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkAllCodecsEncode$ ./ojph/
```

| Image | Raw (MB) | MIC-Go | MIC-4state | MIC-4state-C | MIC-C | Wavelet+SIMD | HTJ2K | JPEG-LS | PICS-2 | PICS-4 | PICS-8 |
|-------|:--------:|:------:|:----------:|:------------:|:-----:|:------------:|:-----:|:-------:|:------:|:------:|:------:|
| MR (256×256) | 0.13 | 121 | 132 | **290** | 273 | 85 | 177 | 71 | 217 | 186 | 128 |
| CT (512×512) | 0.50 | 180 | 191 | **359** | 311 | 84 | 178 | 104 | 248 | 301 | 312 |
| CR (2140×1760) | 7.18 | 233 | 235 | **461** | 423 | 120 | 193 | 89 | 412 | 732 | 1102 |
| XR (2048×2577) | 10.1 | 254 | 254 | **550** | 519 | 127 | 214 | 95 | 447 | 775 | **1212** |
| MG1 (2457×1996) | 9.35 | 380 | 381 | **861** | 820 | 155 | 508 | 239 | 698 | 1112 | **1651** |
| MG2 (2457×1996) | 9.35 | 380 | 378 | **857** | 830 | 153 | 507 | 235 | 686 | 1119 | **1676** |
| MG3 (4774×3064) | 27.3 | 256 | 257 | **556** | 514 | 97 | 202 | 109 | 465 | 832 | **1317** |
| MG4 (4096×3328) | 26.0 | 336 | 340 | **738** | 710 | 107 | 354 | 162 | 619 | 1098 | **1901** |
| CT1 (512×512) | 0.50 | 206 | 211 | **413** | 384 | 94 | 216 | 132 | 286 | 310 | 329 |
| CT2 (512×512) | 0.50 | 192 | 197 | **371** | 340 | 89 | 194 | 132 | 294 | 320 | 294 |
| MG-N (3064×4664) | 27.3 | 254 | 261 | **562** | 529 | 99 | 202 | 107 | 471 | 842 | **1353** |
| MR1 (512×512) | 0.50 | 221 | 222 | **460** | 429 | 101 | 195 | 92 | 302 | 364 | 347 |
| MR2 (1024×1024) | 2.00 | 275 | 263 | **566** | 532 | 107 | 230 | 102 | 455 | 643 | 857 |
| MR3 (512×512) | 0.50 | 300 | 298 | **609** | 591 | 119 | 292 | 142 | 428 | 498 | 448 |
| MR4 (512×512) | 0.50 | 249 | 251 | **494** | 451 | 108 | 226 | 142 | 333 | 370 | 368 |
| NM1 (256×1024) | 0.50 | 237 | 240 | **467** | 444 | 117 | 242 | 132 | 353 | 360 | 302 |
| RG1 (1841×1955) | 6.86 | 229 | 243 | **485** | 397 | 123 | 198 | 70 | 377 | 651 | 942 |
| RG2 (1760×2140) | 7.18 | 280 | 277 | **582** | 488 | 125 | 254 | 112 | 499 | 842 | **1220** |
| RG3 (1760×1760) | 5.91 | 275 | 271 | **550** | 512 | 128 | 273 | 143 | 503 | 863 | **1222** |
| SC1 (2048×2487) | 9.71 | 286 | 293 | **577** | 551 | 83 | 235 | 169 | 528 | 924 | **1425** |
| XA1 (1024×1024) | 2.00 | 242 | 246 | **496** | 471 | 101 | 219 | 114 | 419 | 616 | 832 |

MIC-4state-C and MIC-C require CGO (`-tags cgo_ojph`); MIC-Go and MIC-4state are pure Go. **Bold** = fastest per row. PICS-N uses Go goroutines encoding independent strips in parallel. Wavelet+SIMD encode is 2–4× slower than MIC-Go due to the multi-level forward transform; its compression advantage (see ratio column in decompression table) is the trade-off.

**Encoding (compression) throughput** (MB/s) — Intel Core Ultra 9 285K (AMD64, 24 P-cores), `BenchmarkAllCodecsEncode` (`-tags cgo_ojph`, `-benchtime=10x`). C variants compiled with `-O3 -march=native`. PICS-N uses Go goroutines encoding independent strips in parallel.

```
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkAllCodecsEncode$ ./ojph/
```

| Image | Raw (MB) | MIC-Go | MIC-4state | MIC-4state-C | MIC-C | Wavelet+SIMD | HTJ2K | JPEG-LS | PICS-2 | PICS-4 | PICS-8 |
|-------|:--------:|:------:|:----------:|:------------:|:-----:|:------------:|:-----:|:-------:|:------:|:------:|:------:|
| MR (256×256) | 0.13 | 180 | 219 | 243 | **305** | 102 | 217 | 104 | 111 | 104 | 103 |
| CT (512×512) | 0.50 | 208 | 222 | 314 | 332 | 89 | 258 | 166 | 195 | **358** | 313 |
| CR (2140×1760) | 7.18 | 307 | 312 | 441 | 416 | 138 | 311 | 119 | 488 | 856 | **1195** |
| XR (2048×2577) | 10.1 | 336 | 322 | 498 | 500 | 126 | 269 | 123 | 411 | 738 | **1136** |
| MG1 (2457×1996) | 9.35 | 512 | 508 | 621 | 651 | 162 | 676 | 320 | 858 | 1437 | **2001** |
| MG2 (2457×1996) | 9.35 | 517 | 505 | 644 | 702 | 160 | 700 | 315 | 829 | 1372 | **2095** |
| MG3 (4774×3064) | 27.3 | 355 | 352 | 469 | 425 | 95 | 293 | 153 | 530 | 862 | **1491** |
| MG4 (4096×3328) | 26.0 | 458 | 463 | 554 | 503 | 106 | 451 | 234 | 654 | 1186 | **1984** |
| CT1 (512×512) | 0.50 | 292 | 267 | 383 | 388 | 89 | 314 | 204 | 299 | **397** | 347 |
| CT2 (512×512) | 0.50 | 228 | 236 | 356 | 335 | 95 | 294 | 231 | 286 | **373** | 343 |
| MG-N (3064×4664) | 27.3 | 341 | 357 | 424 | 427 | 94 | 309 | 156 | 538 | 929 | **1421** |
| MR1 (512×512) | 0.50 | 309 | 307 | 400 | **441** | 104 | 271 | 151 | 176 | 301 | 301 |
| MR2 (1024×1024) | 2.00 | 350 | 350 | 498 | 514 | 106 | 339 | 136 | 463 | 672 | **821** |
| MR3 (512×512) | 0.50 | 385 | 379 | 538 | **568** | 121 | 389 | 183 | 287 | 434 | 516 |
| MR4 (512×512) | 0.50 | 305 | 310 | 466 | **471** | 118 | 354 | 252 | 262 | 363 | 325 |
| NM1 (256×1024) | 0.50 | 302 | 302 | 438 | 444 | 114 | 346 | 178 | 273 | 323 | **474** |
| RG1 (1841×1955) | 6.86 | 284 | 315 | 471 | 367 | 127 | 256 | 107 | 349 | 648 | **985** |
| RG2 (1760×2140) | 7.18 | 390 | 398 | 493 | 520 | 140 | 402 | 155 | 598 | 1034 | **1583** |
| RG3 (1760×1760) | 5.91 | 374 | 388 | 488 | 485 | 153 | 455 | 198 | 566 | 1029 | **1389** |
| SC1 (2048×2487) | 9.71 | 414 | 413 | 508 | 466 | 97 | 349 | 297 | 655 | 1170 | **1712** |
| XA1 (1024×1024) | 2.00 | 335 | 324 | 449 | 463 | 111 | 357 | 154 | 437 | 708 | **944** |

MIC-4state-C and MIC-C require CGO (`-tags cgo_ojph`); MIC-Go and MIC-4state are pure Go. **Bold** = fastest per row. PICS-N uses Go goroutines encoding independent strips in parallel. Wavelet+SIMD encode is 2–4× slower than MIC-Go due to the multi-level forward transform. On Intel AMD64, MIC-C single-core reaches 300–700 MB/s; PICS-8 reaches 1.1–2.1 GB/s on large images (CR, MG, SC1).

Key observations for ingestion pipelines:
- **MIC-4state-C** is the fastest single-core encoder: 2–2.5× faster than pure-Go MIC-Go, reaching 500–860 MB/s on large images.
- **PICS-8** reaches **1.2–1.9 GB/s** on large images (CR, XR, MG, SC1) using 8 goroutines — sufficient for real-time PACS ingestion of high-resolution modalities.
- **HTJ2K** encode is comparable to MIC-Go on most images; JPEG-LS is consistently 2–4× slower to encode than MIC-Go.

---

**FSE table working-set sizes** — `BenchmarkFSETableMemory` (`-benchtime=3x`) reports `alloc-KB/op` (total bytes allocated per encode) and `peakHeap-KB` (max `HeapInuse` snapshot mid-loop). Dynamic table sizing keeps the FSE working set proportional to the actual symbol range (derived from `bits.Len16(maxValue)`), not a fixed 65 536-entry table.

```
go test -benchmem -run=^$ -benchtime=3x -bench ^BenchmarkFSETableMemory$ mic
```

| Image | Bit depth (eff.) | alloc-KB/op | peakHeap-KB |
|-------|:----------------:|:-----------:|:-----------:|
| MR (256×256, 12-bit) | 12 | ~1 827 | ~3 352 |
| CT (512×512, 16-bit) | 16 | ~4 995 | ~4 952 |
| CR (2140×1760, 12-bit) | 12 | ~30 040 | ~24 832 |
| MG1 (2457×1996, 14-bit) | 14 | ~30 407 | ~29 312 |
| MG3 (4774×3064, 14-bit) | 14 | ~133 701 | ~24 832 |
| MR3 (512×512, 12-bit) | 12 | ~2 418 | ~5 984 |

Allocation cost scales with image area (pixel count × bytes/symbol), not bit-depth alone, because the largest allocations are the delta and RLE intermediate buffers. The FSE symbol table itself (`symbolTT`, `stateTable`) is sized to `1 << actualTableLog` entries — typically 2 048–4 096 `uint16` values (~4–8 KB) rather than the 128 KB a fixed 65 536-entry table would consume. This keeps the FSE decode table hot in L1/L2 cache regardless of modality.

---

**When to use which:**
- **Pure Go, zero dependencies** → MIC-Go or `DecompressParallelStrips` (no CGO): parallel strips reach ~60–70% of PICS-C speed.
- **Best single-core throughput** → MIC-4state-SIMD (`-O3 -march=native`): beats HTJ2K on 18/21 images; 2–3× faster than MIC-Go.
- **Best multi-core throughput (CGO)** → PICS-C-4/8 (C pthreads + SIMD): 1.5–2.6× faster than Go goroutines; reaches **2.7 GB/s** on MG4 (8 strips). Use PICS-C-4 for ≤ 0.5 MB images, PICS-C-8 for ≥ 7 MB images.
- **High spatial-frequency images (XR, CR)** → Wavelet+SIMD: better compression and competitive single-threaded throughput.
- **Maximum compression ratio, speed secondary** → JPEG-LS: best ratios but 3–6× slower to decompress than MIC-4state-C.
- **Interoperability with existing DICOM viewers** → HTJ2K: competitive ratios and speed on MG1/MG2, but 3–4× slower than PICS-C-8 on most modalities.

For multi-core scaling detail and wavelet SIMD analysis, see [docs/benchmarks.md](./docs/benchmarks.md). For comparison methodology, see [docs/htj2k-comparison.md](./docs/htj2k-comparison.md) and [docs/jpegls-comparison.md](./docs/jpegls-comparison.md).

---

## Browser Decoder

A browser-based decoder lives in [`web/`](./web/):

- **Pure JavaScript** ES module (~20 KB, zero dependencies)
- **Go WASM** build for maximum throughput
- Drag-and-drop `.mic` file loading (MIC1, MIC2, MIC3, MICR, PICS)
- **16-bit greyscale** — Window/Level controls for diagnostic viewing
- **RGB WSI** (MIC3) — full-color tile rendering with pyramid level selector
- **Single-frame RGB** (MICR) — US/VL images rendered directly without WSI controls
- **Multi-frame movie player** — play/pause, frame slider, configurable FPS, keyboard shortcuts

See the **[Web Decoder README](./web/README.md)** for the full API reference and integration guide.

---

## CLI Reference

```bash
go build -o mic-compress ./cmd/mic-compress/

# Compress a raw binary image
./mic-compress -input image.bin -width 512 -height 512 -output image.mic

# Compress a single-frame DICOM → MIC1
./mic-compress -dicom scan.dcm -output scan.mic

# Compress a multi-frame DICOM (independent mode, default)
./mic-compress -dicom tomo.dcm -output tomo.mic

# Compress a multi-frame DICOM (temporal prediction mode)
./mic-compress -dicom tomo.dcm -output tomo.mic -temporal

# Generate all test .mic files (single-frame + multi-frame)
./mic-compress -testdata
```

---

## Figures

Publication-quality figures are in [paper/figures/](paper/figures/). Regenerate with:

```bash
go test -run TestDumpHistogramCSV -v        # export residual histogram data
.venv/bin/python3 paper/generate_figures.py # render all 6 figures
```

| Figure | Description |
|--------|-------------|
| [fig1_pipeline.png](paper/figures/fig1_pipeline.png) | MIC compression pipeline block diagram |
| [fig2_histogram.png](paper/figures/fig2_histogram.png) | Delta-residual distributions for 5 modalities (MR, CT, CR, MG1, XA1) |
| [fig3_multistate_speedup.png](paper/figures/fig3_multistate_speedup.png) | 1/2/4-state FSE isolated decompression speedup, all 21 images (ARM64, pure Go) |
| [fig4_pareto.png](paper/figures/fig4_pareto.png) | Pareto scatter: compression ratio vs. throughput for all codec variants (ARM64) |
| [fig5_predictor_ablation.png](paper/figures/fig5_predictor_ablation.png) | Per-image predictor ratio change vs. Avg (left-only, Paeth, MED) |
| [fig6_tablelog_ablation.png](paper/figures/fig6_tablelog_ablation.png) | TableLog ablation: ratio and gain (TL=11/12/13/adaptive) across all 21 images |

---

## Test Dataset

The codec is benchmarked on a corpus of **39 real, de-identified DICOM images
across 11 modalities** (CT, MR, CR, X-ray, mammography, tomosynthesis, DX,
PET, nuclear medicine, fluoroscopy, secondary capture). All are single-frame,
grayscale (MONOCHROME2), 8–16-bit — predominantly 12–16-bit, where the codec is
best tuned (the core single-frame MIC format; no MIC2/MIC3). The images are drawn
from three public sources: the NEMA WG-04 conformance sample sets (plus a public
breast-tomosynthesis study), the [GDCM](https://gdcm.sourceforge.net/) sample
data set, and [The Cancer Imaging Archive (TCIA)](https://www.cancerimagingarchive.net/).
All ratios below are Δ+RLE+FSE on Apple M2 Max.

The NEMA-derived images (MR, CT, CR, XR, MG, tomo, NM, RG, SC, XA) appear in the
[Compression Results](#compression-results) and [Performance](#performance)
tables above. The grayscale images sourced from GDCM and TCIA are detailed below.

**[GDCM](https://gdcm.sourceforge.net/) sample data set**
([`gdcmData.tar.gz`](https://sourceforge.net/projects/gdcm/files/gdcmData/gdcmData/)) —
classic public-domain DICOM samples:

| Image | Modality | Dim. | Bits | Raw (MB) | Ratio (Δ+RLE+FSE) | Source file |
|-------|----------|------|------|----------|-------------------|-------------|
| CT_BRAIN  | CT (brain)  | 512×512   | 16 | 0.52 | 3.06× | `CT-MONO2-16-brain` |
| CT_ANKLE  | CT (ankle)  | 512×512   | 16 | 0.52 | **9.49×** | `CT-MONO2-16-ankle` |
| CT_ORT    | CT (ortho)  | 512×512   | 16 | 0.52 | 2.68× | `CT-MONO2-16-ort` |
| CT_CHEST  | CT (chest)  | 512×400   | 16 | 0.41 | 2.82× | `CT-MONO2-16-chest` |
| MR_HEAD   | MR (head)   | 256×256   | 16 | 0.13 | 2.61× | `MR-MONO2-16-head` |
| MR_INTERA | MR (body)   | 1024×1024 | 12 | 2.10 | 3.68× | `PHILIPS_Intera-16-MONO2-Uncompress` |
| DX_HAND   | DX (X-ray)  | 1410×1480 | 14 | 4.17 | 2.24× | `DX_GE_FALCON_SNOWY-VOI` |
| DX_CHEST  | DX (chest)  | 1420×1416 | 16 | 3.83 | 1.66× | `DX_J2K_0Padding` |
| CR_THORAX | CR (thorax) | 1876×2076 | 15 | 7.79 | 1.82× | `gdcm-JPEG-LossLessThoravision` |

**[TCIA](https://www.cancerimagingarchive.net/) `ACRIN-NSCLC-FDG-PET`**
(public collection, de-identified) — **PET** modality:

| Image | Modality | Dim. | Bits | Raw (MB) | Ratio (Δ+RLE+FSE) | Source |
|-------|----------|------|------|----------|-------------------|--------|
| PET1 | PT (FDG-PET) | 256×256 | 16 | 0.13 | 2.74× | TCIA ACRIN-NSCLC-FDG-PET |
| PET2 | PT (FDG-PET) | 256×256 | 16 | 0.13 | 3.39× | TCIA ACRIN-NSCLC-FDG-PET |

**[TCIA](https://www.cancerimagingarchive.net/) diagnostic slices**
(public collections, de-identified) — the middle slice of a representative series,
pulled per image via the NBIA REST single-image endpoint:

| Image | Modality | Dim. | Bits | Raw (MB) | Ratio (Δ+RLE+FSE) | Collection |
|-------|----------|------|------|----------|-------------------|------------|
| CT_LUNG     | CT (lung)     | 512×512 | 16 | 0.52 | 2.73× | LIDC-IDRI |
| CT_PANCREAS | CT (abdomen)  | 512×512 | 16 | 0.52 | 2.36× | Pancreas-CT |
| MR_BRAIN    | MR (brain tumor) | 256×320 | 16 | 0.16 | **7.27×** | UPENN-GBM |
| MR_BREAST   | MR (breast)   | 256×256 | 12 | 0.13 | 4.01× | ACRIN-Contralateral-Breast-MR |
| MR_PROSTATE | MR (prostate) | 320×320 | 12 | 0.20 | 2.30× | PROSTATE-DIAGNOSIS |
| PET_PSMA    | PT (PSMA-PET) | 200×200 | 16 | 0.08 | **10.13×** | PSMA-PET-CT-Lesions |
| PET_LUNG    | PT (FDG-PET)  | 200×200 | 16 | 0.08 | 2.97× | Lung-PET-CT-Dx |

PET_PSMA (10.13×) has the highest ratio in the corpus — PSMA-PET is mostly empty
background around focal uptake, giving very long near-zero delta runs; CT_ANKLE
(9.49×) and MR_BRAIN (7.27×) follow, all ahead of mammography (8.79×).

#### Multi-codec decode throughput (GDCM + TCIA images)

Decompression throughput (MB/s) on **Apple M2 Max**, in-process via
`BenchmarkAllCodecs` (`-tags "cgo_ojph cgo_zstd"`, `-benchtime=10x`, C variants
`-O3`). **MIC-4state-C is the fastest decoder on every one of these
images** — 1.0–1.6× HTJ2K and 1.3–4.0× JPEG-LS. PICS-C-8 (parallel, C pthreads)
reaches 1–2.5 GB/s on the larger images.

| Image | MIC-Go | MIC-4state-C | HTJ2K | JPEG-LS | PICS-C-8 |
|-------|-------:|-------------:|------:|--------:|---------:|
| CT_BRAIN    | 184 | **369** | 338 | 151 | 1068 |
| CT_ANKLE    | 396 | **660** | 461 | 309 | 1839 |
| CT_ORT      | 240 | **434** | 369 | 158 | 1261 |
| CT_CHEST    | 209 | **382** | 334 | 148 | 1073 |
| CT_LUNG     | 241 | **428** | 362 | 156 | 1277 |
| CT_PANCREAS | 197 | **370** | 323 | 143 | 1240 |
| MR_HEAD     | 246 | **506** | 340 | 130 | 750 |
| MR_INTERA   | 293 | **534** | 348 | 157 | 2450 |
| MR_BRAIN    | 331 | **633** | 528 | 284 | 955 |
| MR_BREAST   | 279 | **558** | 390 | 194 | 753 |
| MR_PROSTATE | 260 | **499** | 332 | 129 | 1036 |
| DX_HAND     | 247 | **403** | 335 | 127 | 1996 |
| DX_CHEST    | 238 | **409** | 330 | 106 | 1960 |
| CR_THORAX   | 251 | **415** | 321 | 109 | 1848 |
| PET1        | 249 | **525** | 324 | 152 | 749 |
| PET2        | 266 | **530** | 386 | 173 | 749 |
| PET_PSMA    | 317 | **656** | 547 | 443 | 590 |
| PET_LUNG    | 171 | **376** | 361 | 208 | 447 |

Encoding (M2 Max, `BenchmarkAllCodecsEncode`) shows the same ordering: MIC-4state-C
encodes faster than HTJ2K and JPEG-LS on every one of them (e.g. CR_THORAX 497 vs
205 vs 74 MB/s). The two sparse PET images compress through PICS via a raw-strip
fallback (`picsRawFlag`): an incompressible strip is stored as little-endian
`uint16` pixels rather than failing, decoded by both the Go and C PICS decoders.

The `.bin` files are raw little-endian `uint16` pixel dumps in
[`testdata/expanded/`](./testdata/expanded/), wired into the benchmark table in
[fseu16_test.go](fseu16_test.go). To regenerate: for GDCM, download
`gdcmData.tar.gz`; for TCIA, fetch from the NBIA REST API
(`/v1/getImage?SeriesInstanceUID=…` for a whole series, or
`/v1/getSingleImage?SeriesInstanceUID=…&SOPInstanceUID=…` for one mid-slice).
Then extract each `PixelData` to a raw
16-bit LE buffer (preserving the stored bit pattern for signed CT/PET) with
`pydicom` + `numpy` + `python-gdcm` (the latter decodes JPEG-LS/J2K/RLE source
streams).

> **Note on bit depth:** TCIA mammography collections (CMMD, CBIS-DDSM full
> images) are largely 8-bit and were excluded to keep the corpus focused on
> 12–16-bit data; 16-bit mammography is already covered by MG1–MG4 and MG-N.

## Documentation

| Document | Contents |
|----------|----------|
| [docs/architecture.md](./docs/architecture.md) | Source files, bit-depth handling, FSE/ANS internals, RLE protocol, container format layouts |
| [docs/developer-guide.md](./docs/developer-guide.md) | Optimization details, performance-sensitive code paths, common pitfalls, test/benchmark commands |
| [docs/benchmarks.md](./docs/benchmarks.md) | Detailed per-machine benchmark tables (AMD64, ARM64, Apple M2), two-state FSE speedup, PICS scaling |
| [docs/compression-results.md](./docs/compression-results.md) | Compression ratios, predictor comparisons (MED, Zstandard), wavelet vs Delta+RLE+FSE |
| [docs/htj2k-comparison.md](./docs/htj2k-comparison.md) | In-process comparison against HTJ2K (OpenJPH v0.15) — all MIC variants |
| [docs/jpegls-comparison.md](./docs/jpegls-comparison.md) | In-process comparison against JPEG-LS (CharLS v2.4.2) |
| [docs/native-optimizations.md](./docs/native-optimizations.md) | Two-state FSE, interleaved histogram assembly, CPUID dispatch (AMD64/ARM64) |
| [docs/wavelet-simd.md](./docs/wavelet-simd.md) | SIMD-accelerated wavelet transform: blocked column pass + AVX2 kernels |
| [docs/wavelet-fse-analysis.md](./docs/wavelet-fse-analysis.md) | 5/3 integer wavelet pipeline analysis: multi-level decomposition, 4-state FSE |
| [docs/parallel-strips.md](./docs/parallel-strips.md) | PICS format specification, C/pthreads API, throughput scaling, when to use |
| [docs/adaptive-compression.md](./docs/adaptive-compression.md) | Adaptive compression: CALIC gradient predictor, PICA per-strip selection, tableLog tuning, content-adaptive partitioning |
| [docs/16bit-alphabet-entropy-coding.md](./docs/16bit-alphabet-entropy-coding.md) | Design background: why a 16-bit-native entropy coder; why delta over wavelet |
| [web/README.md](./web/README.md) | Browser decoder API reference and integration guide |

---

## Performance Experiments — Recorded Findings

Small log of ideas we measured and the outcomes — useful for anyone considering the same path.

### AVX-512 `VPGATHERDQ`-based 8-state FSE decoder (AMD64) — *negative result*

**Hypothesis.** The 8-state FSE inner loop does eight `dt[state]` lookups per
iteration, one per independent state lane. Replacing those eight scalar loads
with a single `_mm512_i32gather_epi64` (`VPGATHERDQ`, scale 8 for the 8-byte
`decSymbolU16` entries) should let the hardware issue all eight cache-line
accesses in parallel and shrink the gather-bound part of the critical path.

**Measurement.** Implemented in [`ojph/mic_decompress_c.c`](./ojph/mic_decompress_c.c)
as `fse_decompress_eight_state_avx512`, compile-time gated on
`__AVX512F__ && __AVX512DQ__ && __AVX512BW__`, and routed from
`mic_decompress_eight_state_simd` on AMD64. Benchmarked on AWS EC2
`c8i.4xlarge` (Intel Xeon 6 / Granite Rapids).

**Result.** Materially *slower* than the scalar 8-state path on every image:
the `MIC-8state-SIMD` column dropped ≈20–36 % across the 21-image dataset
(geomean ≈ −27 %) versus the previous scalar-8-state-FSE + AVX-512-RLE/delta
configuration. Two large mammography images (MG1, MG2) showed a small ≈+20 %
gain; everything else regressed.

**Why.** Granite Rapids has three load ports. Eight independent scalar loads
with eight distinct, predictable addresses (exactly our access pattern) issue
in ≈3–4 cycles in steady state. `VPGATHERDQ` is ~12 µops with ≈8-cycle
throughput, so the hardware gather is *fundamentally slower* than the parallel
scalar loads the out-of-order engine was already overlapping. Secondary
costs: a stack-array round-trip for `nb_bits[8]` (vector store → 8 scalar
loads), and `VPMOVQD` / `VPMOVQW` extraction latency added to the dependency
chain. Crucially, the bit reader stays serial — the gather can't shorten the
critical path past the eight back-to-back `bit_reader_get_bits_nz` calls.

**Action.** Reverted. The canonical AMD64 SIMD path is now (again) scalar
8-state FSE + AVX-512 RLE-expand + AVX-512 delta-inverse. The scalar-8-state
FSE inner loop saturates the load ports without help from `VPGATHERDQ`.

**Takeaway for similar work.** x86 gather instructions help when the compiler
*cannot* see addresses as independent or when scatter / load resources are
already exhausted. For a loop with $k$ statically-independent loads on a CPU
with $\geq \lceil k/2 \rceil$ load ports, parallel scalar loads usually win.

---

## Roadmap

- [x] Native amd64 + arm64 optimizations — two-state FSE (pure Go, all platforms), interleaved histogram assembly, CPUID/NEON dispatch — see [docs/native-optimizations.md](./docs/native-optimizations.md)
- [x] Browser-based decoding in JS and WASM — see [web decoder](./web/README.md)
- [x] Multi-frame image support (Breast Tomosynthesis) — MIC2 container with independent and temporal modes, browser movie player
- [x] Wavelet (5/3 integer) decorrelation stage — benchmarked; Delta+RLE+FSE wins for lossless; wavelet is a candidate for future lossy/progressive mode
- [x] SIMD-accelerated wavelet transform — blocked column pass + AVX2 predict/update kernels; +10–38% decompression throughput — see [docs/wavelet-simd.md](./docs/wavelet-simd.md)
- [x] 4-state FSE in wavelet pipeline — BMI2 assembly decoder; wavelet V2 SIMD now beats HTJ2K ratio on 7/8 modalities
- [x] Whole Slide Imaging (WSI) — MIC3 tiled container with YCoCg-R color transform, pyramid levels, parallel tile compression, browser RGB viewer
- [x] Parallel single-image compression (PICS) — horizontal strip partitioning; Go + C (pthreads, AMD64 AVX2, ARM64 scalar) — see [docs/parallel-strips.md](./docs/parallel-strips.md)
- [x] Left+Up average predictor — avg(left, top) replaces pure-left prediction
- [x] Gap removal for sparse symbol distributions — three map representations (bitmap, delta-list, raw-list) collapse unused RLE symbols before FSE; +0.45% on CT (16-bit, 2.7% fill rate); auto-disables on dense alphabets — see [docs/adaptive-compression.md](./docs/adaptive-compression.md)
- [x] CALIC-style gradient-adaptive predictor — `avg(W,N) + slope(NE,NW)/8` correction; improves 7/8 modalities (+0.2–1.1%); CT regresses −2.5% due to sharp boundaries — see [docs/adaptive-compression.md](./docs/adaptive-compression.md)
- [x] Per-strip pipeline selection — PICA format tries avg and grad predictor per strip, keeps smaller; improves 6/8 modalities (+0.3–1.1%); CT correctly auto-selects avg — see [docs/adaptive-compression.md](./docs/adaptive-compression.md)
- [x] Adaptive tableLog refinement — tableLog=13 branch for large symbol sets (symbolLen > 512); reduces probability quantization error on 12-16 bit images — see [docs/adaptive-compression.md](./docs/adaptive-compression.md)
- [x] Content-adaptive strip partitioning — PICA places strip boundaries at entropy transitions (equal-cost on inter-row variance) for more uniform per-strip FSE tables — see [docs/adaptive-compression.md](./docs/adaptive-compression.md)
- [x] Full C encoder/decoder pipeline — `mic_compress_c.c` implements Delta→RLE→FSE 4-state in C; correctness verified on 21 DICOM images; geometric mean **1.04×** decompression speedup vs HTJ2K; CGO bindings `MICCompressFourStateC`/`MICCompressTwoStateC` — see [docs/htj2k-comparison.md](./docs/htj2k-comparison.md)
- [ ] **Re-measure the README reference tables on the full 39-image corpus.** The per-image decompression/encoding tables and the predictor/tableLog/FSE-multi-state result sets in this README (and in [docs/htj2k-comparison.md](./docs/htj2k-comparison.md)) are still the original 21-image runs on Apple M2 Max / Intel Core Ultra 9 285K (HTJ2K win-counts such as "17/21" reflect that subset). Re-run them on Apple M2 Max so the README reflects the single 39-image list. The `BenchmarkAllCodecs` decode/ratio numbers in the Test Dataset section already cover all 39 on M2 Max.
- [x] **Update the paper dataset to 39 images.** `paper/mic-paper-v9-ieee-tmi.tex` now describes the 39-image / 11-modality corpus — dataset table plus the compression-ratio, encoding, decompression, FSE-microbench, strip-parallel (PICS), and JavaScript decoder tables — with the ARM64 columns measured on the M4 Pro reference and the JS tables re-run on the same machine (Node.js v22.16). The 18 added GDCM + TCIA images include the DX and PET modalities (DX_HAND, PET1 are the representative rows in the FSE microbench and JS tables).
- [ ] **Measure the 18 added images on the AMD64 reference (AWS c8i).** The paper's AMD64 columns in the encoding, decompression, FSE-microbench, and strip-parallel tables currently show `---` for the GDCM + TCIA images (only the original 21 are measured on AMD64). Close this gap per [`.claude/benchmark-rules.md`](./.claude/benchmark-rules.md) §3–§4 so both reference platforms cover all 39 images, then drop the "pending the AMD64 reference run" caption notes.
- [ ] WSI streaming API (io.ReaderAt/WriteSeeker for very large files)
- [ ] **NEON wavelet kernel for ARM64.** Port the AVX2 `wt53Predict`/`wt53Update` lifting kernels to NEON in a new `wavelet_simd_arm64.s` (Plan-9 assembler syntax). The scalar wavelet path on Apple Silicon already benefits from MIC's blocked column layout, but a 4-lane `int32x4_t` predict/update kernel issued at 4 NEON ops/cycle on Apple M3/M4 should land within roughly 20% of the AVX2 gain on AMD64 — expected +15–35% wavelet decode throughput. The compressed stream must remain bit-identical to the scalar V2 stream and wire into the existing `BenchmarkWaveletV2SIMDRLEFSECompress` dispatch.
- [ ] **Verify Clang's variable-shift codegen on AArch64.** The four-state FSE C decoder relies on `LSRV`/`LSLV` for the bit-reader inner loop; `objdump -d` on the M4 Pro build should confirm that Clang emits `lsr w_, w_, w_` without spilling the shift count to memory. This is a one-time codegen audit, not a code change — file the result alongside [docs/native-optimizations.md](./docs/native-optimizations.md) so future Clang upgrades have a baseline to diff against.
- [x] Ultrasound (US) and Visible Light (VL) RGB support — `CompressRGB`/`DecompressRGB` provide single-frame YCoCg-R + Delta+RLE+FSE compression without tiled container overhead; 1.56×–6.24× on NEMA compsamples US1/VL1–VL6 — see [rgbcompress.go](rgbcompress.go)
