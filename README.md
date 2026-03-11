# MIC — Medical Image Codec

A **lossless compression codec for 16-bit DICOM images**, implemented in Go. MIC achieves JPEG 2000–comparable compression ratios with significantly higher decompression throughput — up to **16 GB/s** on large images.

| Branch | Status |
|--------|--------|
| main | ![CI](https://github.com/pappuks/medical-image-codec/actions/workflows/go.yml/badge.svg) |

## Why MIC?

| Property | Value |
|----------|-------|
| Compression ratio | 1.7× – 8.9× greyscale, 3–5× RGB tissue (lossless) |
| Peak decompression speed | up to 16 GB/s (ARM64, 64 cores) |
| Supported formats | 8–16 bit greyscale, 8-bit RGB (WSI/pathology) |
| Multi-frame support | MIC2 container (random access or temporal prediction) |
| WSI support | MIC3 tiled container with pyramid levels, parallel encode/decode |
| Browser support | JavaScript + WASM decoder (greyscale + RGB WSI) |

## Table of Contents

1. [Quick Start](#quick-start)
2. [Compression Pipeline](#compression-pipeline)
3. [Multi-Frame Support (MIC2)](#multi-frame-support-mic2)
4. [Whole Slide Imaging (MIC3)](#whole-slide-imaging-mic3)
5. [Algorithm Details](#algorithm-details)
6. [Native Optimizations](#native-optimizations)
7. [Compression Results](#compression-results)
8. [Benchmark Results](#benchmark-results)
9. [Browser Decoder](#browser-decoder)
10. [CLI Reference](#cli-reference)
11. [Comparison with HTJ2K](#comparison-with-htj2k)
12. [Roadmap](#roadmap)

---

## Quick Start

```bash
# Build the CLI tool
go build -o mic-compress ./cmd/mic-compress/

# Compress a single-frame DICOM
./mic-compress -dicom scan.dcm -output scan.mic

# Compress a multi-frame DICOM (e.g., Breast Tomosynthesis)
./mic-compress -dicom tomo.dcm -output tomo.mic

# Compress an RGB WSI image (Go API)
mic.CompressWSI(rgbPixels, width, height, 3, 8, mic.WSIOptions{})

# Run all tests
go test -v ./...

# Run benchmarks
go test -bench=. -benchtime=10x
```

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

---

## Multi-Frame Support (MIC2)

MIC2 is a container format for multi-frame DICOM images (e.g., Breast Tomosynthesis / DBT).

### MIC2 File Layout

```
Byte offset   Field
────────────  ─────────────────────────────────────────
0  – 3        Magic: "MIC2"
4  – 7        Image width        (uint32 LE)
8  – 11       Image height       (uint32 LE)
12 – 15       Frame count        (uint32 LE)
16            Pipeline flags     bit0=spatial  bit1=temporal
17 – 19       Reserved
────────────  ─────────────────────────────────────────
20 – …        Frame offset table (N × 8 bytes each)
              └─ per entry: offset (uint32) + length (uint32)
────────────  ─────────────────────────────────────────
…             Frame 0 compressed data
              Frame 1 compressed data
              ⋮
              Frame N-1 compressed data
```

### Two Compression Modes

```
Independent Mode                   Temporal Mode
─────────────────────────────────  ─────────────────────────────────────────
Frame 0  →  Delta+RLE+FSE          Frame 0  →  Delta+RLE+FSE
Frame 1  →  Delta+RLE+FSE          Frame 1  →  ZigZag(residual)+RLE+FSE
Frame 2  →  Delta+RLE+FSE          Frame 2  →  ZigZag(residual)+RLE+FSE
  ⋮                                  ⋮
Frame N  →  Delta+RLE+FSE          Frame N  →  ZigZag(residual)+RLE+FSE

✓ Random access to any frame       residual = current frame − previous frame
✓ Best for spatially smooth data   ZigZag maps signed diff → unsigned
                                   ✓ Candidate for low-spatial-correlation data
```

### Multi-Frame Benchmark (69-frame Breast Tomosynthesis, 2457×1890, 10-bit)

| Mode | Raw Size | Compressed | Ratio |
|------|----------|------------|-------|
| Independent | 614 MB | 46.1 MB | **13.3×** |
| Temporal | 614 MB | 47.5 MB | 12.9× |

For smooth mammographic images the spatial predictor outperforms inter-frame prediction. Temporal mode may win on datasets with less spatial redundancy.

---

## Whole Slide Imaging (MIC3)

MIC3 is a tiled container format for whole slide images (WSI) used in digital pathology. It extends the existing compression pipeline to handle RGB images via a reversible color transform, with tiled random access and multi-resolution pyramid levels.

### WSI Pipeline

```
RGB Pixels (8-bit per channel)
       │
       ▼
┌──────────────────────────────────────────┐
│         YCoCg-R Color Transform          │
│  Reversible, bit-exact decorrelation     │
│  Y: luminance [0,255]                   │
│  Co/Cg: chrominance (ZigZag → [0,510])  │
└─────────────────┬────────────────────────┘
                  │
    ┌─────────────┼─────────────┐
    ▼             ▼             ▼
  Y plane      Co plane      Cg plane
    │             │             │
    ▼             ▼             ▼
  Delta+RLE+FSE  Delta+RLE+FSE  Delta+RLE+FSE
    │             │             │
    └─────────────┼─────────────┘
                  │
                  ▼
          Compressed tile blob
```

### Key Features

- **Tiled architecture**: Images divided into tiles (default 256×256) for O(1) random access to any tile
- **Pyramid levels**: Multi-resolution levels (each ½ the previous) generated via 2×2 box filter downsampling
- **Parallel compression**: Tiles are independent — goroutine worker pool for parallel encode/decode
- **Constant-plane optimization**: Background tiles (all white/black) compress to 15–17 bytes total
- **Full RGB losslessness**: YCoCg-R transform is perfectly reversible for integer inputs

### MIC3 File Layout

```
Byte offset   Field
────────────  ─────────────────────────────────────────
0  – 3        Magic: "MIC3"
4  – 7        Version (1)
8  – 15       Width × Height (uint32 LE each)
16 – 23       Tile width × height (uint32 LE each)
24 – 25       Channels: 1=grey, 3=RGB
26            Bits per sample: 8 or 16
27            Flags: bit0=spatial, bit1=color_transform
28 – 29       Pyramid level count
32 – 39       Total tile count (uint64 LE)
────────────  ─────────────────────────────────────────
48 – …        Level descriptors (N × 20 bytes each)
              └─ width, height, tilesX, tilesY, firstTileIdx
────────────  ─────────────────────────────────────────
…             Tile offset table (M × 16 bytes each)
              └─ per tile: offset (uint64) + length (uint64)
────────────  ─────────────────────────────────────────
…             Concatenated compressed tile blobs
```

### WSI Compression Results

| Tile Type | Ratio | Notes |
|-----------|-------|-------|
| White background | **1946×** | Near-zero entropy after color transform |
| Dense tissue (H&E) | **4.4×** | Smooth staining gradients, good delta prediction |
| Gradient | **5.4×** | Excellent spatial correlation |
| Mixed slide (typical) | **4–8×** | Weighted average across background + tissue tiles |

### WSI API

```go
// Compress a full-resolution RGB image into MIC3
compressed, err := mic.CompressWSI(pixels, width, height, 3, 8, mic.WSIOptions{
    TileWidth:  256,
    TileHeight: 256,
    Workers:    8,  // parallel goroutines
})

// Read header without decompressing
hdr, err := mic.ReadWSIHeader(compressed)

// Decompress a single tile (O(1) random access)
tile, err := mic.DecompressWSITile(compressed, level, tileX, tileY)

// Decompress an arbitrary region across tiles
region, err := mic.DecompressWSIRegion(compressed, level, x, y, w, h)
```

### DICOM WSI Integration

MIC3 is designed to work with DICOM Supplement 145 (VL Whole Slide Microscopy Image):
- Each MIC3 tile maps to a DICOM frame
- Tile grid matches DICOM's Dimension Organization
- Pyramid levels can map to DICOM concatenation or separate instances
- Sample WSI test images for validation: [jcupitt/dicom-wsi-sample](https://github.com/jcupitt/dicom-wsi-sample)

---

## Algorithm Details

### Delta Encoding

Encodes each pixel as its difference from the average of its top and left neighbors, transforming spatially correlated pixels into small, zero-clustered residuals.

```
          top
           │
  left ──► pixel  →  delta = pixel − avg(left, top)
```

Differences that exceed the threshold are stored verbatim, preceded by an escape delimiter whose value is derived from the image bit depth.

### RLE

Encodes the delta-coded stream as runs:

- **Same runs** — a count followed by a single repeated value (most common after delta coding)
- **Diff runs** — a count followed by that many distinct values

The minimum encoded run length is 3, guaranteeing the RLE output is never larger than the input.

### FSE (Finite State Entropy / ANS)

An [asymmetric numeral systems](https://en.wikipedia.org/wiki/Asymmetric_numeral_systems) entropy coder. MIC extends the reference implementation to support up to 65 535 distinct symbols (vs 4 095 in the original). The encoder writes **backwards**; the decoder reads **forwards**.

Key adaptive behavior: `tableLog` is automatically raised from 11 → 12 when symbol density is high (>128 distinct symbols, >32 samples each), yielding **4–7% better ratios** on CR and MG images.

### Canonical Huffman

An alternative entropy stage using [canonical Huffman codes](https://en.wikipedia.org/wiki/Canonical_Huffman_code). Symbol selection is capped iteratively so the tree depth stays ≤ 14 bits, keeping the codebook compact. Produces the smallest files but at lower decompression speed compared to FSE.

---

## Native Optimizations

MIC includes platform-specific optimizations that are automatically active on **amd64** (Intel/AMD) and **arm64** (Apple Silicon, AWS Graviton), and fall back to equivalent pure-Go implementations on all other architectures.

### Two-State FSE (`fse2state.go`)

The standard FSE decode loop has a serial dependency chain: each state transition depends on the output of the previous one, limiting throughput to ~1 symbol per table-lookup latency (~4 cycles). Two-state FSE breaks this by running two independent state machines on alternating symbol positions:

```
symbol[0] ← stateA    symbol[2] ← stateA    ...
symbol[1] ← stateB    symbol[3] ← stateB    ...
```

The two chains are independent, so the CPU's out-of-order engine executes them in parallel. This is **pure Go** — it delivers ILP benefits on every platform (amd64, arm64, WASM) without any assembly. Streams are prefixed with `[0xFF, 0x02]` magic bytes; `FSEDecompressU16Auto` dispatches transparently — existing single-state compressed files continue to work unchanged.

**Measured gains** (Intel Xeon @ 2.80 GHz, isolated FSE decompression):

| Image | 1-State | 2-State | Δ |
|-------|---------|---------|---|
| MR 256×256 | 164 MB/s | 207 MB/s | **+26%** |
| MG3 4774×3064 | 243 MB/s | 312 MB/s | **+28%** |
| MG4 4096×3328 | 256 MB/s | 321 MB/s | **+25%** |

### Interleaved Histogram (`asm_amd64.s`, `asm_arm64.s`)

The symbol frequency histogram distributes even-indexed pixels into `count[]` and odd-indexed pixels into `count2[]`, avoiding store-to-load forwarding stalls when consecutive pixels have identical values (common after delta coding). The two arrays are merged in a single backward scan that finds the max symbol and symbol range simultaneously.

The same 4-way unrolled algorithm is implemented in assembly for both amd64 (using `ADDL`/`MOVWQZX`) and arm64 (using `MOVWU`/`MOVW`/`LSL`).

### Platform Dispatch (`asm_amd64.go`, `asm_arm64.go`)

- **amd64**: A one-time `init()` probes SSSE3 and AVX2 support via `CPUID`. `ycocgRForwardNative` and `ycocgRInverseNative` dispatch to the best available implementation at runtime.
- **arm64**: NEON is always available on ARM64 — no CPUID probe needed. The dispatch wrappers call scalar-in-assembly stubs whose register layout is ready for a future 8-pixel-wide NEON path.
- **other platforms**: `asm_generic.go` (`//go:build !amd64 && !arm64`) provides identical pure-Go fallbacks.

For full details, design decisions, and benchmark methodology see [`docs/native-optimizations.md`](./docs/native-optimizations.md).

---

## Compression Results

`Delta + RLE + FSE` — all images are 16-bit greyscale DICOM. CT has the widest dynamic range (max value 65 535).

| Modality | Dimensions | Raw Size | Compressed | Ratio |
|----------|-----------|----------|------------|-------|
| MR | 256×256 | 0.13 MB | 0.053 MB | **2.35×** |
| CT | 512×512 | 0.50 MB | 0.22 MB | **2.24×** |
| CR | 2140×1760 | 7.18 MB | 1.98 MB | **3.63×** |
| XR | 2048×2577 | 10.1 MB | 5.79 MB | **1.74×** |
| MG1 | 2457×1996 | 9.35 MB | 1.09 MB | **8.57×** |
| MG2 | 2457×1996 | 9.35 MB | 1.09 MB | **8.55×** |
| MG3 | 4774×3064 | 27.3 MB | 12.2 MB | **2.24×** |
| MG4 | 4096×3328 | 26.0 MB | 7.49 MB | **3.47×** |

### Adaptive tableLog improvement

| Modality | Before | After | Gain |
|----------|--------|-------|------|
| CR | 3.47× | 3.63× | +4.4% |
| MG1 | 7.99× | 8.57× | +7.1% |
| MG2 | 7.98× | 8.55× | +7.1% |

### Wavelet+FSE vs Delta+RLE+FSE

A 5/3 integer wavelet transform was evaluated as an alternative decorrelation stage. **Delta+RLE+FSE wins on all DICOM modalities**, in both compression ratio and decompression speed. Full analysis: [docs/wavelet-fse-analysis.md](./docs/wavelet-fse-analysis.md).

#### Compression Ratio

| Modality | Delta+RLE+FSE | Wavelet+FSE | Wavelet+RLE+FSE |
|----------|:---:|:---:|:---:|
| MR (256×256) | **2.35×** | 2.09× | 2.09× |
| CT (512×512) | **2.24×** | 1.48× | 1.48× |
| CR (2140×1760) | **3.63×** | 2.59× | 2.59× |
| XR (2048×2577) | **1.74×** | 1.53× | 1.53× |
| MG1 (2457×1996) | **8.57×** | 4.91× | 7.28× |
| MG2 (2457×1996) | **8.55×** | 4.90× | 7.27× |
| MG3 (4774×3064) | **2.29×** | 1.90× | 1.93× |
| MG4 (4096×3328) | **3.47×** | 2.63× | 3.11× |

#### Decompression Speed (MB/s)

| Modality | Delta+RLE+FSE | Wavelet+FSE | Wavelet+RLE+FSE |
|----------|:---:|:---:|:---:|
| MR | 116 | **146** | 122 |
| CT | 165 | **168** | 142 |
| CR | **543** | 418 | 371 |
| XR | **605** | 576 | 486 |
| MG1 | **1 530** | 592 | 680 |
| MG2 | **1 493** | 618 | 644 |
| MG3 | **606** | 387 | 352 |
| MG4 | **1 054** | 480 | 579 |

---

## Benchmark Results

Benchmarks measure **decompression speed** — the primary use case is real-time rendering of compressed DICOM.

> **Note:** RAM speed has a larger impact than CPU clock speed. Machines with DDR5 RAM outperform older machines even at lower core counts.

> **Benchmark methodology:** All decompression benchmarks (`BenchmarkDeltaRLEFSECompress`, `BenchmarkFSEDecompress`, `BenchmarkDeltaRLEFSEDecompress`, `BenchmarkFSE2StateSummary`) spawn one goroutine per iteration and run all `b.N` goroutines concurrently. The reported MB/s therefore reflects **aggregate multi-core throughput** across all available CPUs, not single-core speed. With `-benchtime=200x` on a 64-core machine, all 200 frames decompress in parallel — matching the real-world use case of concurrent multi-frame rendering. Use `-benchtime=1x` for single-iteration (single-goroutine) measurements.

```bash
# Run the full benchmark suite (parallel decompression, 200 concurrent goroutines)
go test -benchmem -run=^$ -benchtime=200x -bench ^BenchmarkDeltaRLEFSECompress$ mic

# Compare single-state vs two-state FSE decompression (isolated, parallel)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSEDecompress mic

# Compare single-state vs two-state: full Delta+RLE+FSE pipeline (parallel)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkDeltaRLEFSEDecompress mic

# Human-readable speedup table (parallel)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSE2StateSummary -v mic
```

### AWS c7g.metal — ARM64 | 64 cores

| Modality | FPS | Decompression Speed |
|----------|-----|---------------------|
| MR (256×256) | **17 411** | 2 282 MB/s |
| CT (512×512) | **8 455** | 4 433 MB/s |
| CR (2140×1760) | **1 132** | 8 527 MB/s |
| XR (2048×2577) | **892** | 9 411 MB/s |
| MG1 (2457×1996) | **1 671** | **16 387 MB/s** |
| MG2 (2457×1996) | **1 634** | 16 023 MB/s |
| MG3 (4774×3064) | **281** | 8 044 MB/s |
| MG4 (4096×3328) | **558** | 15 213 MB/s |

### AWS c7i.8xlarge — AMD64 | 32 cores (Intel Xeon Platinum 8488C)

| Modality | FPS | Decompression Speed |
|----------|-----|---------------------|
| MR | 8 714 | 1 142 MB/s |
| CT | 2 303 | 1 208 MB/s |
| CR | 421 | 3 172 MB/s |
| XR | 310 | 3 269 MB/s |
| MG1 | 532 | 5 220 MB/s |
| MG2 | 522 | 5 124 MB/s |
| MG3 | 121 | 3 468 MB/s |
| MG4 | 182 | 4 964 MB/s |

### AWS c7g.8xlarge — ARM64 | 32 cores

| Modality | FPS | Decompression Speed |
|----------|-----|---------------------|
| MR | 11 627 | 1 524 MB/s |
| CT | 4 170 | 2 186 MB/s |
| CR | 570 | 4 290 MB/s |
| XR | 432 | 4 562 MB/s |
| MG1 | 908 | 8 901 MB/s |
| MG2 | 803 | 7 879 MB/s |
| MG3 | 156 | 4 455 MB/s |
| MG4 | 262 | 7 132 MB/s |

### Mac Studio — Apple M2 Max | ARM64 | 12 cores

| Modality | FPS | Decompression Speed |
|----------|-----|---------------------|
| MR | 8 044 | 1 054 MB/s |
| CT | 2 137 | 1 121 MB/s |
| CR | 277 | 2 089 MB/s |
| XR | 199 | 2 101 MB/s |
| MG1 | 374 | 3 666 MB/s |
| MG2 | 373 | 3 659 MB/s |
| MG3 | 78 | 2 239 MB/s |
| MG4 | 117 | 3 188 MB/s |

#### Two-State FSE Decompression Speedup — Mac Studio (Apple M2 Max, 12 cores)

`BenchmarkFSE2StateSummary` — full Delta+RLE+FSE pipeline, 200 iterations:

| Image | 1-state (MB/s) | 2-state (MB/s) | Speedup | Ratio |
|-------|:--------------:|:--------------:|:-------:|:-----:|
| MR (256×256)    | 1 403.5 | 2 284.7 | **1.63×** | 2.35× |
| CT (512×512)    | 1 556.4 | 2 028.9 | **1.30×** | 2.28× |
| CR (2140×1760)  | 3 777.6 | 5 323.3 | **1.41×** | 3.62× |
| XR (2048×2577)  | 3 889.8 | 5 787.2 | **1.49×** | 1.74× |
| MG1 (2457×1996) | 3 722.1 | 5 148.3 | **1.38×** | 2.80× |
| MG2 (2457×1996) | 3 636.7 | 4 751.1 | **1.31×** | 2.80× |
| MG3 (4774×3064) | 1 916.8 | 5 705.3 | **2.98×** | 2.19× |
| MG4 (4096×3328) | 4 230.0 | 6 001.2 | **1.42×** | 1.84× |

Two-state FSE delivers **1.3–3.0× faster decompression** across all modalities. MG3 shows the largest gain (2.98×) due to its symbol distribution characteristics.

---

## Browser Decoder

A browser-based decoder lives in [`web/`](./web/):

- **Pure JavaScript** ES module (~20 KB, zero dependencies)
- **Go WASM** build for maximum throughput
- Drag-and-drop `.mic` file loading (MIC1, MIC2, MIC3)
- **16-bit greyscale**: Window/Level controls for diagnostic viewing
- **RGB WSI**: Full-color tile rendering with pyramid level selector
- **Multi-frame movie player** — play/pause, frame slider, configurable FPS, keyboard shortcuts (Space, ←/→)
- ~10–30 M pixels/s in JavaScript (V8), higher with WASM

See the **[Web Decoder README](./web/README.md)** for the full API reference and integration guide.

---

## CLI Reference

```bash
go build -o mic-compress ./cmd/mic-compress/

# Compress a raw binary image
./mic-compress -input image.bin -width 512 -height 512 -output image.mic

# Compress a single-frame DICOM → MIC1
./mic-compress -dicom scan.dcm -output scan.mic

# Compress a multi-frame DICOM (independent mode, default — random frame access)
./mic-compress -dicom tomo.dcm -output tomo.mic

# Compress a multi-frame DICOM (temporal prediction mode)
./mic-compress -dicom tomo.dcm -output tomo.mic -temporal

# Generate all test .mic files (single-frame + multi-frame)
./mic-compress -testdata
```

---

## Comparison with HTJ2K

MIC is compared against lossless HTJ2K using [OpenJPH](https://github.com/aous72/OpenJPH) v0.15 (`ojph_compress -reversible true`), the leading open-source HTJ2K implementation. Measurements are single-threaded on Apple M2 Max. MIC uses the two-state FSE decoder. MIC timings are **in-process** (pure library calls); HTJ2K timings include subprocess launch + file I/O overhead (~6 ms).

| Image | Raw (MB) | MIC ratio | HTJ2K ratio | MIC decomp (MB/s) | HTJ2K decomp (MB/s) | MIC speedup |
|-------|:--------:|:---------:|:-----------:|:-----------------:|:-------------------:|:-----------:|
| MR    | 0.13 | 2.35× | **2.38×** | 133 | 20 † | — |
| CT    | 0.50 | **2.24×** | 1.77× | 164 | 82 † | — |
| CR    | 7.18 | 3.63× | **3.77×** | **287** | 215 | **1.33×** |
| XR    | 10.1 | **1.74×** | 1.67× | **297** | 205 | **1.45×** |
| MG1   | 9.35 | **8.57×** | 8.25× | **471** | 338 | **1.39×** |
| MG2   | 9.35 | **8.55×** | 8.24× | **471** | 338 | **1.39×** |
| MG3   | 27.3 | **2.24×** | 2.22× | **297** | 225 | **1.32×** |
| MG4   | 26.0 | 3.47× | **3.51×** | **399** | 307 | **1.30×** |

† MR and CT HTJ2K throughput is dominated by the ~6 ms process startup cost; the comparison is not meaningful for these small images. For images ≥ 7 MB the startup overhead is < 7% of total time.

**Key takeaways:**
- Compression ratios are within 4% across all modalities; MIC wins on CT (+27%), XR (+4%), MG1/MG2 (+4%).
- MIC decompresses **1.3–1.5× faster** on large images in single-threaded use (up from 1.1–1.2× with single-state FSE).
- At 64 cores (AWS c7g.metal), MIC reaches up to **16 GB/s** while OpenJPH's single-process CLI scales to ~2–4 GB/s.
- MIC is a pure Go library with no subprocess overhead — decompress any frame with a function call.

Reproduce the comparison:

```bash
go test -run TestHTJ2KComparison -v -timeout 300s
```

---

## Roadmap

- [x] Native amd64 + arm64 optimizations — two-state FSE (ILP via dual ANS chains, pure Go, all platforms), interleaved histogram assembly (amd64 + arm64), CPUID dispatch (amd64) / always-NEON dispatch (arm64) for future SIMD paths — see [`docs/native-optimizations.md`](./docs/native-optimizations.md)
- [x] Browser-based decoding in JS and WASM — see [web decoder](./web/README.md)
- [x] Multi-frame image support (Breast Tomosynthesis) — MIC2 container with independent and temporal modes, browser movie player
- [x] Wavelet (5/3 integer) decorrelation stage — benchmarked; Delta+RLE+FSE wins for lossless; wavelet remains a candidate for a future lossy/progressive mode
- [x] Whole Slide Imaging (WSI) — MIC3 tiled container with YCoCg-R color transform, pyramid levels, parallel tile compression, browser RGB viewer with level selector
- [ ] WSI streaming API (io.ReaderAt/WriteSeeker for very large files)
- [x] Left+Up average predictor — implemented from [Klaus Post's feedback](https://github.com/pappuks/medical-image-codec/issues/1); avg(left, top) replaces pure-left prediction in the main Delta+RLE+FSE pipeline
- [ ] Gap removal for sparse value distributions (XR images) — bitmap to collapse unused symbols before FSE; estimated 15–20% size reduction for XR modality
- [ ] Dynamic prediction switching (every 32 pixels) — adaptive selection between left, top, avg predictors; ~5% CT improvement in Klaus's experiments
- [ ] Paeth filtering — PNG-style predictor; marginal gain (~1–3%) over current avg predictor, low priority
