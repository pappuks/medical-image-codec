# MIC — Medical Image Codec

A **lossless compression codec for 16-bit DICOM images**, implemented in Go. MIC achieves JPEG 2000–comparable compression ratios with significantly higher decompression throughput — up to **16 GB/s** on mammography (best case; geometric mean ~7.5 GB/s across modalities on 64 cores).

| Branch | Status |
|--------|--------|
| main | ![CI](https://github.com/pappuks/medical-image-codec/actions/workflows/go.yml/badge.svg) |

## Why MIC?

| Property | Value |
|----------|-------|
| Compression ratio | 1.7× – 8.9× greyscale; 3–5× RGB tissue tiles (lossless) |
| Peak decompression speed | up to 16 GB/s (ARM64, 64 cores); ~7.5 GB/s geometric mean |
| Supported formats | 8–16 bit greyscale; 8-bit RGB (WSI/pathology) |
| Multi-frame support | MIC2 container (random access or temporal prediction) |
| WSI support | MIC3 tiled container with pyramid levels and parallel encode/decode |
| Browser support | JavaScript + WASM decoder (greyscale + RGB WSI) |

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

All images are 16-bit greyscale DICOM. Ratios measured in-process on Apple M2 Max (`-tags cgo_ojph`).

| Image | Raw (MB) | MIC | MIC-4state | Wavelet | PICS-4 | PICS-8 | HTJ2K | JPEG-LS |
|-------|:--------:|:---:|:----------:|:-------:|:------:|:------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 2.35× | 2.35× | 2.38× | 2.28× | 2.21× | 2.38× | **2.52×** |
| CT (512×512) | 0.50 | 2.24× | 2.24× | 1.67× | 2.15× | 1.96× | 1.77× | **2.68×** |
| CR (2140×1760) | 7.18 | 3.69× | 3.69× | 3.81× | 3.70× | 3.71× | 3.77× | **3.96×** |
| XR (2048×2577) | 10.1 | 1.74× | 1.74× | **1.76×** | 1.75× | **1.76×** | 1.67× | **1.76×** |
| MG1 (2457×1996) | 9.35 | 8.79× | 8.79× | 8.67× | 8.84× | 8.87× | 8.25× | **8.91×** |
| MG2 (2457×1996) | 9.35 | 8.77× | 8.77× | 8.65× | 8.83× | 8.85× | 8.24× | **8.90×** |
| MG3 (4774×3064) | 27.3 | 2.24× | 2.24× | 2.32× | 2.31× | 2.34× | 2.22× | **2.38×** |
| MG4 (4096×3328) | 26.0 | 3.47× | 3.47× | 3.59× | 3.59× | 3.62× | 3.51× | **3.71×** |
| CT1 (512×512) | 0.50 | 2.79× | 2.79× | 2.49× | 2.54× | 2.29× | 2.70× | **3.19×** |
| CT2 (512×512) | 0.50 | 3.49× | 3.49× | 2.87× | 3.11× | 2.72× | 3.29× | **4.54×** |
| MG-N (3064×4664) | 27.3 | 2.24× | 2.24× | 2.32× | 2.31× | 2.34× | 2.23× | **2.38×** |
| MR1 (512×512) | 0.50 | 2.09× | 2.09× | 2.14× | 2.10× | 2.08× | 2.13× | **2.30×** |
| MR2 (1024×1024) | 2.00 | 3.28× | 3.28× | 3.34× | 3.31× | 3.31× | 3.35× | **3.52×** |
| MR3 (512×512) | 0.50 | 3.93× | 3.93× | 4.09× | 3.89× | 3.84× | 4.33× | **4.51×** |
| MR4 (512×512) | 0.50 | 4.12× | 4.12× | 4.18× | 4.09× | 4.03× | 4.21× | **4.49×** |
| NM1 (256×1024) | 0.50 | 5.15× | 5.15× | 5.02× | 5.26× | 5.28× | 5.76× | **6.28×** |
| RG1 (1841×1955) | 6.86 | 1.70× | 1.70× | 1.70× | 1.70× | 1.69× | 1.63× | **1.72×** |
| RG2 (1760×2140) | 7.18 | 4.23× | 4.23× | 4.32× | 4.28× | 4.30× | 4.32× | **4.51×** |
| RG3 (1760×1760) | 5.91 | 6.08× | 6.08× | 6.82× | 6.11× | 6.12× | 6.99× | **7.31×** |
| SC1 (2048×2487) | 9.71 | 3.71× | 3.71× | 3.70× | 3.73× | 3.74× | 3.85× | **4.73×** |
| XA1 (1024×1024) | 2.00 | 5.01× | 5.01× | 4.94× | 5.04× | 5.03× | 4.88× | **5.39×** |

MIC and MIC-4state encode identically — the 4-state variant only unlocks a faster decoder. PICS strips compress independently, which slightly reduces ratio on small images (MR, CT) but improves it on large CR/MG images where strip-local FSE table adaptation helps. JPEG-LS consistently achieves the highest ratios but at 3–6× lower decompression throughput (see Performance table below).

`CompressSingleFrameGapRemoval` adds +0.45% on CT (2.237× → **2.247×**) by collapsing the 65536-symbol RLE alphabet to the 1782 symbols that actually occur, via a delta-encoded expand map (1798 bytes overhead). Other modalities are unaffected. See [docs/compression-results.md](./docs/compression-results.md).

For predictor comparisons (MED, Zstandard) and WSI results, see [docs/compression-results.md](./docs/compression-results.md).

---

## Performance

**Decompression throughput** (MB/s) — Apple M2 Max (ARM64), `BenchmarkAllCodecs` (`-tags cgo_ojph`, `-benchtime=5x`). PICS-N decompresses a single image using N goroutines in parallel.

| Image | Raw (MB) | MIC-Go | MIC-4state | MIC-4state-C | MIC-4state-SIMD | Wavelet+SIMD | PICS-2 | PICS-4 | PICS-8 | HTJ2K | JPEG-LS |
|-------|:--------:|:------:|:----------:|:------------:|:---------------:|:------------:|:------:|:------:|:------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 136 | 201 | 322 | _356_ | 248 | **299** | 262 | 245 ⚠ | 250 | 95 |
| CT (512×512) | 0.50 | 188 | 234 | 368 | _384_ | 316 | 342 | **478** | 467 | 321 | 140 |
| CR (2140×1760) | 7.18 | 299 | 341 | 541 | 540 | _567_ | 549 | 1002 | **1625** | 368 | 153 |
| XR (2048×2577) | 10.1 | 305 | 345 | 545 | 542 | _627_ | 588 | 1066 | **1730** | 338 | 109 |
| MG1 (2457×1996) | 9.35 | 487 | 518 | 692 | 692 | 678 | 888 | 1456 | **2411** | _809_ | 409 |
| MG2 (2457×1996) | 9.35 | 476 | 502 | 685 | 685 | 697 | 877 | 1464 | **2376** | _797_ | 407 |
| MG3 (4774×3064) | 27.3 | 311 | 346 | 529 | _534_ | 422 | 577 | 1110 | **1993** | 340 | 154 |
| MG4 (4096×3328) | 26.0 | 421 | 454 | 639 | _640_ | 516 | 781 | 1369 | **2040** | 554 | 185 |
| CT1 (512×512) | 0.50 | 245 | 293 | 433 | _440_ | 425 | 391 | **542** | 484 | 361 | 182 |
| CT2 (512×512) | 0.50 | 238 | 278 | 416 | 444 | _481_ | 394 | **486** | 428 | 376 | 173 |
| MG-N (3064×4664) | 27.3 | 323 | 359 | _556_ | 551 | 468 | 582 | 1092 | **1894** | 344 | 154 |
| MR1 (512×512) | 0.50 | 274 | 319 | _525_ | 523 | 435 | 443 | 609 | **613** | 326 | 115 |
| MR2 (1024×1024) | 2.00 | 339 | 378 | 585 | _586_ | 498 | 579 | 894 | **1163** | 368 | 167 |
| MR3 (512×512) | 0.50 | 360 | 413 | 597 | _608_ | 507 | 530 | **774** | 753 | 426 | 230 |
| MR4 (512×512) | 0.50 | 323 | 358 | 557 | _586_ | 479 | 479 | 664 | **688** | 402 | 198 |
| NM1 (256×1024) | 0.50 | 330 | 384 | _618_ | 593 | 575 | 502 | 611 | **710** | 416 | 213 |
| RG1 (1841×1955) | 6.86 | 241 | 304 | 419 | 417 | _584_ | 448 | 796 | **1269** | 334 | 104 |
| RG2 (1760×2140) | 7.18 | 365 | 401 | 608 | 607 | _644_ | 635 | 1108 | **1715** | 442 | 178 |
| RG3 (1760×1760) | 5.91 | 380 | 414 | 614 | 616 | _656_ | 657 | 1176 | **1635** | 554 | 245 |
| SC1 (2048×2487) | 9.71 | 383 | 410 | 601 | _602_ | 388 | 699 | 1233 | **1996** | 399 | 221 |
| XA1 (1024×1024) | 2.00 | 337 | 382 | 580 | _592_ | 459 | 589 | 912 | **1232** | 433 | 208 |

MIC-4state-C/SIMD and PICS require CGO (`-tags cgo_ojph`); all other MIC variants are pure Go. _Italic_ = best single-threaded throughput per row. **Bold** = best multi-threaded (PICS) throughput per row. ⚠ MR (256×256) is too small for PICS — goroutine overhead eliminates the parallelism benefit.

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
- Drag-and-drop `.mic` file loading (MIC1, MIC2, MIC3)
- **16-bit greyscale** — Window/Level controls for diagnostic viewing
- **RGB WSI** — full-color tile rendering with pyramid level selector
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
- [ ] WSI streaming API (io.ReaderAt/WriteSeeker for very large files)
- [ ] Ultrasound (US) image support — US DICOM frames are typically RGB (3 samples/pixel, 8-bit); requires extending the single-frame pipeline to handle multi-channel grayscale-equivalent encoding (similar to WSI YCoCg-R path) without the tiled container overhead
