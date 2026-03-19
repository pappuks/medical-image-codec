# Compression Results

This document contains detailed compression ratio results and predictor comparisons across all supported modalities.

## Delta+RLE+FSE — Baseline Results

All images are 16-bit greyscale DICOM. CT has the widest dynamic range (max value 65 535).

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

### Adaptive tableLog Improvement

Raising `tableLog` from 11 → 12 when symbol density is high (>128 distinct symbols, >32 samples each) improves ratio on wide-dynamic-range modalities with no meaningful decompression speed impact:

| Modality | Before | After | Gain |
|----------|--------|-------|------|
| CR | 3.47× | 3.63× | +4.4% |
| MG1 | 7.99× | 8.57× | +7.1% |
| MG2 | 7.98× | 8.55× | +7.1% |

---

## Comparison with Delta+Zstandard

MIC's entropy coder (FSE) belongs to the same ANS family as [Zstandard](https://facebook.github.io/zstd/). To verify that MIC's custom RLE+FSE stages add value beyond a general-purpose compressor, we delta-encode the raw uint16 stream and compress the byte representation with `zstd` at three levels. A raw+zstd column (no delta) isolates the delta stage's contribution.

| Modality | MIC | d+zstd-1 | d+zstd-3 | d+zstd-19 | raw zstd-3 |
|----------|:---:|:--------:|:--------:|:---------:|:----------:|
| MR | **2.35×** | 1.75× | 1.82× | 1.95× | 1.65× |
| CT | **2.24×** | 1.71× | 1.89× | 2.03× | 1.79× |
| CR | **3.63×** | 2.70× | 2.95× | 3.27× | 2.05× |
| XR | **1.74×** | 1.43× | 1.43× | 1.43× | 1.32× |
| MG1 | **8.57×** | 6.19× | 6.37× | 7.07× | 5.77× |
| MG2 | **8.55×** | 6.18× | 6.36× | 7.04× | 5.75× |
| MG3 | **2.29×** | 1.71× | 1.89× | 2.09× | 1.50× |
| MG4 | **3.47×** | 2.80× | 2.87× | 2.99× | 2.57× |

**Key findings:**
- MIC outperforms Delta+zstd-19 (ultra compression) on every modality by 10–22%.
- The advantage is largest on mammography (MG1: 8.57× vs 7.07×) where MIC's 16-bit-aware RLE efficiently encodes long runs of identical residuals that zstd's byte-oriented LZ77 matcher cannot exploit.
- Removing delta encoding reduces zstd's ratio by 10–44%, confirming delta encoding is essential.

```bash
go test -run TestDeltaZstdComparison -v
```

---

## MED Predictor Comparison

The JPEG-LS MED (Median Edge Detector) predictor `median(left, top, left+top-diag)` adapts to horizontal, vertical, and diagonal edges. MIC uses a simpler `avg(left, top)` predictor. Both predictors were benchmarked through the same RLE+FSE pipeline.

| Modality | Avg (MIC) | MED | Diff |
|----------|:---------:|:---:|:----:|
| MR | 2.348× | 2.357× | +0.3% |
| CT | 2.238× | 2.306× | +3.1% |
| CR | 3.628× | 3.632× | +0.1% |
| XR | 1.738× | 1.734× | −0.2% |
| MG1 | 8.566× | 8.690× | +1.5% |
| MG2 | 8.553× | 8.678× | +1.5% |
| MG3 | 2.289× | 2.356× | +2.9% |
| MG4 | 3.474× | 3.415× | −1.7% |

**Key findings:**
- MED gives modest improvements on some modalities (CT: +3.1%, MG3: +2.9%) but slight losses on others (XR: −0.2%, MG4: −1.7%). Geometric mean improvement: ~0.9%.
- MED decompression is ~1.5–2× slower due to the diagonal pixel dependency, which prevents the branch-free interior loop optimization.
- The simpler avg predictor is justified: the marginal ratio gain does not offset the decompression speed penalty.

```bash
go test -run TestMEDPredictorComparison -v
```

---

## Wavelet V2 SIMD + 4-State FSE vs Delta+RLE+FSE

The 5/3 integer wavelet pipeline with 5-level decomposition, SIMD acceleration, and 4-state FSE achieves **better compression ratios than Delta+RLE+FSE on 7 of 8 modalities** — matching or exceeding HTJ2K on the same set. Delta+RLE+FSE remains faster to decompress due to its single-pass memory access pattern.

Full analysis: [docs/wavelet-fse-analysis.md](./wavelet-fse-analysis.md).

Benchmarks on **Intel Xeon @ 2.80 GHz, 4 cores, 10 concurrent goroutines**.

### Compression Ratio

| Modality | Delta+RLE+FSE | Wavelet V2 SIMD+4FSE | HTJ2K (OpenJPH) |
|----------|:---:|:---:|:---:|
| MR (256×256) | 2.35× | **2.38×** | 2.38× |
| CT (512×512) | **2.24×** | 1.67× | 1.77× |
| CR (2140×1760) | 3.63× | **3.81×** | 3.77× |
| XR (2048×2577) | 1.74× | **1.76×** | 1.67× |
| MG1 (2457×1996) | 8.57× | **8.67×** | 8.25× |
| MG2 (2457×1996) | 8.55× | **8.65×** | 8.24× |
| MG3 (4774×3064) | 2.24× | **2.32×** | 2.22× |
| MG4 (4096×3328) | 3.47× | **3.59×** | 3.51× |

The wavelet pipeline matches HTJ2K on MR, beats it on CR/XR/MG1/MG2/MG3/MG4 (+1–5%), and falls short only on CT (−6%, due to 16-bit escape encoding in low-pass bands).

### Decompression Speed (Intel Xeon @ 2.80 GHz, 4 cores, 10 goroutines)

| Modality | Delta+RLE+FSE | Wavelet V2 scalar+4FSE | Wavelet V2 SIMD+4FSE | SIMD speedup |
|----------|:---:|:---:|:---:|:---:|
| MR | **186** | 150 | 165 | +10% |
| CT | **281** | 152 | 190 | +25% |
| CR | **302** | 166 | 210 | +27% |
| XR | **513** | 193 | 214 | +11% |
| MG1 | **860** | 182 | 227 | +25% |
| MG2 | **729** | 193 | 241 | +25% |
| MG3 | **466** | 118 | 112 | — |
| MG4 | **826** | 144 | 198 | +38% |

### Decompression Speed (Apple M2 Max, ARM64, single-threaded)

| Modality | Delta+RLE+FSE (Go) | Wavelet V2 SIMD | HTJ2K (in-process) |
|----------|:---:|:---:|:---:|
| MR | 145 | **464** | 261 |
| CT | 181 | **538** | 292 |
| CR | 290 | **784** | 358 |
| XR | 301 | **878** | 317 |
| MG1 | 472 | **1129** | 790 |
| MG2 | 473 | **1069** | 794 |
| MG3 | 304 | **716** | 334 |
| MG4 | 415 | **827** | 551 |

Wavelet V2 SIMD exceeds HTJ2K on all 8 modalities single-threaded on ARM64.

**Guidance:** Use **Delta+RLE+FSE** for pure-Go, zero-dependency production deployment. Use **WaveletV2SIMDRLEFSECompressU16** when compression ratio is the priority or HTJ2K-compatible ratios are required.

---

## Multi-Frame Benchmark

69-frame Breast Tomosynthesis, 2457×1890, 10-bit:

| Mode | Raw Size | Compressed | Ratio |
|------|----------|------------|-------|
| Independent | 614 MB | 46.1 MB | **13.3×** |
| Temporal | 614 MB | 47.5 MB | 12.9× |

For smooth mammographic images, the spatial predictor outperforms inter-frame prediction. Temporal mode is intended for sequences with high inter-frame correlation (e.g., cardiac cine MRI, fluoroscopy, nuclear medicine); it has not yet been benchmarked favorably on available clinical datasets.

---

## WSI Compression Results

| Tile Type | Ratio | Notes |
|-----------|-------|-------|
| White background | **1946×** | Near-zero entropy after color transform |
| Dense tissue (H&E) | **4.4×** | Smooth staining gradients, good delta prediction |
| Gradient | **5.4×** | Excellent spatial correlation |
| Mixed slide (typical) | **4–8×** | Weighted average across background + tissue tiles |
