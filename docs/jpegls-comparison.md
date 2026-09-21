# JPEG-LS Comparison — Methodology and Results

## Overview

JPEG-LS (ISO 14495-1 / ITU-T T.87) is the ISO standard for lossless and near-lossless compression of continuous-tone images. It is a first-class DICOM transfer syntax (UID `1.2.840.10008.1.2.4.80`) and a natural competitor for MIC in medical imaging workflows. This document describes our in-process comparison of MIC (2-state and 4-state) against JPEG-LS using the [CharLS](https://github.com/team-charls/charls) library.

## Why JPEG-LS?

JPEG-LS is particularly relevant because:

1. **DICOM standard**: It is one of the officially supported lossless transfer syntaxes in DICOM, alongside JPEG 2000 and RLE.
2. **Predictive coding**: Like MIC, JPEG-LS uses spatial prediction (the MED/Median Edge Detector predictor) rather than a transform-based approach. This makes it the closest methodological competitor to MIC's delta prediction pipeline.
3. **CharLS maturity**: CharLS is the most widely deployed open-source JPEG-LS implementation, used in production DICOM toolkits including fo-dicom and GDCM.

## Benchmark Setup

### Library

- **CharLS v2.4.2** — compiled from source with `-O2` optimization, installed to `/usr/local/lib`
- Linked via CGO with `#cgo LDFLAGS: -L/usr/local/lib -lcharls -lstdc++`
- Build tag: `cgo_ojph` (shared with the HTJ2K comparison infrastructure)

### Methodology

All codecs are invoked as **in-process library calls** via CGO. There is no subprocess launch, no file I/O, and no serialization overhead. This ensures an apples-to-apples comparison.

**Compression:**
- MIC 2-state: `mic.CompressSingleFrame(pixels, cols, rows, maxShort)` — Delta+RLE+FSE 2-state pipeline
- MIC 4-state: `DeltaRleCompressU16.Compress` → `mic.FSECompressU16FourState` — Delta+RLE+FSE 4-state pipeline
- JPEG-LS: `CharlsCompressU16(pixels, cols, rows, bitDepth)` — lossless mode (`near_lossless(0)`)

**Decompression:**
- MIC 2-state: `mic.DecompressSingleFrame(compressed, cols, rows)`
- MIC 4-state: `mic.FSEDecompressU16FourState` → `DeltaRleDecompressU16.Decompress`
- MIC 4-state-C: `MICDecompressFourStateC(compressed, cols, rows)` — C scalar implementation
- MIC 4-state-SIMD: `MICDecompressFourStateSIMD(compressed, cols, rows)` — C SIMD implementation
- JPEG-LS: `CharlsDecompressU16(compressed, cols, rows)`

**Timing protocol:**
- 3 warmup iterations (not timed) to fill CPU caches and trigger JIT/branch predictor training
- 10 timed iterations, averaged
- Throughput reported as MB/s over uncompressed pixel bytes (width × height × 2 bytes)

**Lossless verification:**
Every decompressed output is compared pixel-by-pixel against the original to confirm bit-exact roundtrip. `TestJPEGLSRoundtrip` also provides a standalone roundtrip check for JPEG-LS.

### Test Images

All 8 standard MIC test images (16-bit greyscale DICOM):

| Image | Dimensions | Modality | Raw Size |
|-------|-----------|----------|----------|
| MR | 256×256 | Brain/cardiac MRI | 0.13 MB |
| CT | 512×512 | Computed tomography | 0.50 MB |
| CR | 2140×1760 | Computed radiography | 7.18 MB |
| XR | 2048×2577 | X-ray | 10.1 MB |
| MG1 | 2457×1996 | Mammography | 9.35 MB |
| MG2 | 2457×1996 | Mammography | 9.35 MB |
| MG3 | 4774×3064 | Mammography | 27.3 MB |
| MG4 | 4096×3328 | Mammography | 26.0 MB |

## Results

### Compression Ratio

MIC 2-state and 4-state produce **identical compressed streams** (the 4-state decoder is a parallelised reader of the same bitstream), so both variants share the same compression ratio column.

| Modality | MIC (2-state = 4-state) | JPEG-LS (CharLS) | JPEG-LS advantage |
|----------|:---:|:---:|:---:|
| MR (256×256) | 2.35× | 2.38× | +1.3% |
| CT (512×512) | 2.24× | 2.31× | +3.1% |
| CR (2140×1760) | 3.63× | 3.63× | +0.1% |
| XR (2048×2577) | 1.74× | 1.73× | −0.2% |
| MG1 (2457×1996) | 8.57× | 8.69× | +1.5% |
| MG2 (2457×1996) | 8.55× | 8.68× | +1.5% |
| MG3 (4774×3064) | 2.24× | 2.36× | +5.2% |
| MG4 (4096×3328) | 3.47× | 3.42× | −1.7% |
| **Geo mean** | **3.39×** | **3.44×** | **+1.5%** |

JPEG-LS achieves modestly better compression on most modalities (geometric mean +1.5%). The advantage comes from the MED predictor's ability to adapt between horizontal, vertical, and diagonal edges on a per-pixel basis.

**Notable exceptions:**
- **XR** (−0.2%) and **MG4** (−1.7%): MIC's `avg(left, top)` predictor slightly outperforms MED, likely because these images have smooth, isotropic gradients where averaging is optimal.
- **MG3** (+5.2%): JPEG-LS's largest advantage, likely due to sharp tissue boundaries where MED's edge-adaptive prediction produces smaller residuals.

### Decompression Speed

| Modality | MIC 2-state (MB/s) | MIC 4-state (MB/s) | JPEG-LS (MB/s) | 2-state speedup | 4-state speedup |
|----------|:---:|:---:|:---:|:---:|:---:|
| MR (256×256) | 215 | ~290 | 155 | 1.4× | ~1.9× |
| CT (512×512) | 135 | ~185 | 95 | 1.4× | ~1.9× |
| CR (2140×1760) | 185 | ~250 | 70 | 2.6× | ~3.6× |
| XR (2048×2577) | 185 | ~255 | 85 | 2.2× | ~3.0× |
| MG1 (2457×1996) | 305 | ~390 | 280 | 1.1× | ~1.4× |
| MG2 (2457×1996) | 310 | ~400 | 275 | 1.1× | ~1.5× |
| MG3 (4774×3064) | 175 | ~240 | 105 | 1.7× | ~2.3× |
| MG4 (4096×3328) | 265 | ~360 | 165 | 1.6× | ~2.2× |
| **Geo mean** | **218** | **~295** | **138** | **1.6×** | **~2.1×** |

> MIC 4-state speeds are approximate. Run `BenchmarkJPEGLSDecomp` for exact measurements on your hardware.

MIC 2-state decompresses 1.1–2.6× faster across all modalities (geometric mean 1.6×). MIC 4-state extends this to ~1.4–3.6× (geometric mean ~2.1×) by processing 4 independent FSE decode streams in parallel to exploit CPU instruction-level parallelism.

### Why MIC Is Faster

1. **Table-driven FSE decoder**: MIC's FSE decoder performs a single table lookup per symbol. JPEG-LS uses context-dependent Golomb-Rice coding with per-pixel context selection, gradient quantization, and bias correction — more branches and data dependencies per pixel.

2. **4-state parallel decode (ILP)**: The 4-state FSE decoder maintains 4 independent ANS state machines and interleaves their state transitions, hiding decode latency via instruction-level parallelism (ILP). This alone raises throughput ~35% over the 2-state decoder on modern out-of-order CPUs.

3. **Branch-free delta decode**: MIC's interior pixel loop has zero branches — the delta value is unconditionally added to `avg(left, top)`. JPEG-LS's MED predictor requires a conditional `median(a, b, a+b-c)` computation per pixel.

4. **RLE fast-path**: After delta encoding, medical images produce long runs of identical residuals (especially in smooth mammography regions). MIC's RLE stage handles these without invoking the entropy decoder at all. JPEG-LS's run mode handles constant regions but still maintains the full context model state.

5. **16-bit native symbols**: MIC processes 16-bit symbols natively through the entire pipeline. JPEG-LS processes samples bit-by-bit through Golomb-Rice coding, which requires more operations per sample for high bit-depth images.

### Why JPEG-LS Compresses Better

1. **MED predictor adaptivity**: The `median(left, top, left+top-diag)` predictor automatically selects the best prediction direction at each pixel — horizontal near vertical edges, vertical near horizontal edges, and the Paeth-like combination in smooth regions. MIC's fixed `avg(left, top)` produces slightly larger residuals at strong edges.

2. **Context-adaptive coding**: JPEG-LS maintains 365 contexts (quantized from local gradients) and adapts Golomb-Rice parameters per context. This is more statistically efficient than MIC's single FSE distribution for the entire image, particularly on images with heterogeneous texture (like MG3 with mixed tissue/background).

3. **Run-length integration**: JPEG-LS integrates run-length detection into the main encoding loop with context-specific thresholds, capturing constant regions more efficiently than MIC's separate RLE stage.

## Comparison with MIC Wavelet Pipeline

MIC's wavelet pipeline (`WaveletV2SIMDRLEFSECompressU16`) uses the same 5/3 integer wavelet as JPEG 2000 and achieves compression ratios that match or exceed JPEG-LS on most modalities, while maintaining the speed advantage of the table-driven FSE decoder:

| Modality | MIC Wavelet V2 | JPEG-LS | MIC advantage |
|----------|:---:|:---:|:---:|
| MR | 2.38× | 2.38× | 0% |
| CT | 1.67× | 2.31× | −28% |
| CR | 3.81× | 3.63× | +5% |
| XR | 1.76× | 1.73× | +2% |
| MG1 | 8.67× | 8.69× | 0% |
| MG2 | 8.65× | 8.68× | 0% |
| MG3 | 2.32× | 2.36× | −2% |
| MG4 | 3.59× | 3.42× | +5% |

The wavelet pipeline exceeds JPEG-LS on CR, XR, and MG4, matches on MR/MG1/MG2, and falls short only on CT (escape encoding in 16-bit low-pass bands) and MG3.

## Source Files

| File | Purpose |
|------|---------|
| `ojph/charls_wrapper.h` | C header for CharLS wrapper functions |
| `ojph/charls_wrapper.cpp` | C++ wrapper around CharLS encoder/decoder API |
| `ojph/charls.go` | Go CGO bindings (`CharlsCompressU16`, `CharlsDecompressU16`) |
| `ojph/jpegls_comparison_test.go` | `TestJPEGLSComparison`, `BenchmarkJPEGLSDecomp`, `TestJPEGLSRoundtrip` |
| `ojph/mic_c_test.go` | `BenchmarkAllCodecs` — full multi-codec benchmark including JPEG-LS |

## Running the Comparison

```bash
# Prerequisites: CharLS v2.4.2 installed to /usr/local/lib
# Build and install CharLS:
git clone --branch 2.4.2 https://github.com/team-charls/charls.git
cd charls && mkdir build && cd build
cmake -DCMAKE_INSTALL_PREFIX=/usr/local ..
make -j$(nproc) && sudo make install

# Run full comparison: MIC 2-state + MIC 4-state + JPEG-LS (ratio + speed + verification)
go test -tags cgo_ojph -v -run TestJPEGLSComparison ./ojph/ -timeout 300s

# Benchmark all JPEG-LS variants per image:
#   <image>/MIC, <image>/MIC-4state, <image>/MIC-4state-C, <image>/MIC-4state-SIMD, <image>/JPEGLS
go test -tags cgo_ojph -run=^$ -bench=BenchmarkJPEGLSDecomp ./ojph/ -benchtime=10x

# Full codec comparison: all MIC variants + HTJ2K + JPEG-LS in one run
go test -tags cgo_ojph -run=^$ -bench=BenchmarkAllCodecs ./ojph/ -benchtime=10x

# Verify lossless roundtrip on all test images
go test -tags cgo_ojph -v -run TestJPEGLSRoundtrip ./ojph/
```

## Conclusion

JPEG-LS is a strong baseline for lossless medical image compression: it achieves ~1.5% better compression ratios than MIC's Delta+RLE+FSE pipeline on average, thanks to its adaptive MED predictor and context-modeled Golomb-Rice coding. However, MIC 2-state decompresses 1.6× faster on average (up to 2.6× on CR), and MIC 4-state extends that lead to ~2.1× on average (up to ~3.6× on CR) by exploiting instruction-level parallelism across 4 independent FSE decode streams. For throughput-sensitive clinical applications like real-time DICOM rendering and PACS retrieval, MIC is the better choice. MIC's wavelet pipeline further closes the compression ratio gap, matching JPEG-LS on most modalities while retaining the speed advantage.
