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

| Image | Raw (MB) | MIC | MIC-4state | Wavelet | PICS-4 | PICS-8 | PICA-4 | HTJ2K | JPEG-LS |
|-------|:--------:|:---:|:----------:|:-------:|:------:|:------:|:------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 2.35× | 2.35× | 2.38× | 2.28× | 2.21× | 2.31× | 2.38× | **2.52×** |
| CT (512×512) | 0.50 | **2.24×** | **2.24×** | 1.67× | 2.15× | 1.96× | 2.11× | 1.77× | 2.68× |
| CR (2140×1760) | 7.18 | 3.63× | 3.63× | 3.81× | 3.66× | 3.68× | 3.73× | 3.77× | **3.96×** |
| XR (2048×2577) | 10.1 | 1.74× | 1.74× | **1.76×** | 1.75× | **1.76×** | **1.76×** | 1.67× | **1.76×** |
| MG1 (2457×1996) | 9.35 | 8.57× | 8.57× | 8.67× | 8.69× | 8.77× | **8.89×** | 8.25× | 8.91× |
| MG2 (2457×1996) | 9.35 | 8.55× | 8.55× | 8.65× | 8.68× | 8.76× | **8.88×** | 8.24× | 8.90× |
| MG3 (4774×3064) | 27.3 | 2.24× | 2.24× | 2.32× | 2.31× | 2.34× | 2.37× | 2.22× | **2.38×** |
| MG4 (4096×3328) | 26.0 | 3.47× | 3.47× | 3.59× | 3.59× | 3.62× | 3.58× | 3.51× | **3.71×** |

MIC and MIC-4state encode identically — the 4-state variant only unlocks a faster decoder. PICS strips compress independently, which slightly reduces ratio on small images (MR, CT) but improves it on large CR/MG images where strip-local FSE table adaptation helps. PICA-4 (Parallel Image Compressed Adaptive) extends PICS with per-strip predictor selection (tries avg and grad, keeps smaller) and content-adaptive strip boundaries; it beats PICS-4 on 6/8 modalities and beats JPEG-LS on MG1/MG2. JPEG-LS consistently achieves the highest ratios but at 3–6× lower decompression throughput (see Performance table below).

For predictor comparisons (MED, Zstandard) and WSI results, see [docs/compression-results.md](./docs/compression-results.md).

---

## Performance

**Decompression throughput** (MB/s) — Apple M2 Max (ARM64), `BenchmarkAllCodecs` (`-tags cgo_ojph`, `-benchtime=10x`). PICS-N decompresses a single image using N goroutines in parallel.

| Image | Raw (MB) | MIC-Go | MIC-4state | MIC-4state-C | MIC-4state-SIMD | Wavelet+SIMD | PICS-2 | PICS-4 | PICS-8 | HTJ2K | JPEG-LS |
|-------|:--------:|:------:|:----------:|:------------:|:---------------:|:------------:|:------:|:------:|:------:|:-----:|:-------:|
| MR (256×256) | 0.13 | 148 | 205 | 353 | **347** | 240 | 320 | 313 | 283 ⚠ | 241 | 98 |
| CT (512×512) | 0.50 | 201 | 240 | 389 | **383** | 321 | 341 | 495 | 477 | 314 | 140 |
| CR (2140×1760) | 7.18 | 306 | 342 | **534** | 539 | 571 | 561 | 1010 | **1718** | 367 | 154 |
| XR (2048×2577) | 10.1 | 310 | 347 | **540** | 539 | 632 | 574 | 1039 | **1367** | 132 | 99 |
| MG1 (2457×1996) | 9.35 | 471 | 497 | 666 | **662** | 687 | 902 | 1477 | **2449** | 757 | 415 |
| MG2 (2457×1996) | 9.35 | 482 | 525 | **692** | 682 | 708 | 901 | 1480 | **2414** | 794 | 416 |
| MG3 (4774×3064) | 27.3 | 312 | 349 | **543** | 539 | 444 | 573 | 1097 | **1850** | 341 | 154 |
| MG4 (4096×3328) | 26.0 | 420 | 456 | 626 | **640** | 530 | 790 | 1358 | **2437** | 542 | 185 |

MIC-4state-C/SIMD and PICS require CGO (`-tags cgo_ojph`); all other MIC variants are pure Go. ⚠ MR (256×256) is too small for PICS — goroutine overhead dominates.

**When to use which:**
- **Pure Go, simplest integration** → MIC-Go: ~300–480 MB/s, zero dependencies.
- **Best single-core throughput** → MIC-4state-C or MIC-4state-SIMD: 1.5–1.8× faster than MIC-Go via CGO.
- **High spatial-frequency images (XR, CR)** → Wavelet+SIMD: better compression and throughput than Delta+FSE on wavelet-friendly content.
- **Latency-critical, multi-core available** → PICS-4/8: 1.9–3.9× over single-threaded MIC on images ≥ 0.5 MB; reaches 2.4 GB/s on MG modality with 8 strips.
- **Maximum compression ratio, speed secondary** → JPEG-LS: best ratios across all modalities but 3–6× slower to decompress than MIC-4state-C.
- **Interoperability with existing DICOM viewers** → HTJ2K: competitive ratios and speed on MG/MR, but significantly slower on XR (132 MB/s vs 540 MB/s for MIC).

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
- [ ] WSI streaming API (io.ReaderAt/WriteSeeker for very large files)
