# Wavelet + FSE Compression: Implementation and Analysis

This document describes the Le Gall 5/3 integer wavelet transform as an alternative
decorrelation stage for the MIC codec, the progressive improvements made to the
pipeline (multi-level decomposition, SIMD acceleration, 4-state FSE), and a detailed
comparison against Delta+RLE+FSE and HTJ2K (High Throughput JPEG 2000).

---

## Background

The primary MIC codec uses a **Delta → RLE → FSE** pipeline. Delta encoding
decorrelates pixels spatially (each pixel stores the difference from the average of
its top and left neighbours), RLE suppresses runs of identical residuals, and FSE
entropy-codes the resulting symbol stream.

The 5/3 integer wavelet is the same transform used by JPEG 2000 for lossless coding.
It is a two-tap predict/update lifting scheme that produces a multi-resolution
decomposition while remaining perfectly reversible over integers. This document
evaluates how effectively it decorrelates medical images compared to per-pixel delta
encoding, and how recent optimisations change that picture.

---

## The Le Gall 5/3 Lifting Scheme

### 1D Forward Transform

For a row of `n` values `x[0], x[1], …, x[n-1]`:

```
Predict (high-pass, odd-indexed):
  d[i] = x[2i+1] − ⌊(x[2i] + x[2i+2]) / 2⌋

Update (low-pass, even-indexed):
  s[i] = x[2i] + ⌊(d[i-1] + d[i] + 2) / 4⌋
```

Boundary extension is symmetric: `d[-1] = d[0]` at the left edge,
`x[n] = x[n-1]` at the right. The result is an interleaved array where even
positions hold low-pass (smooth) coefficients and odd positions hold high-pass
(detail) coefficients.

### 2D Transform (Mallat / Separated Layout)

`wt53Forward2DSeparated` applies the 1D transform to every row (horizontal pass)
then de-interleaves; then to every column (vertical pass) then de-interleaves. This
produces the Mallat subband layout:

```
┌───────┬───────┐
│  LL   │  LH   │
│ (low) │       │
├───────┼───────┤
│  HL   │  HH   │
│       │       │
└───────┴───────┘
```

### Multi-Level Decomposition

For `L` levels the LL quadrant is recursively subdivided. MIC defaults to **5
levels**, which is also the standard used by JPEG 2000 / HTJ2K. The buffer stride
is always the original image width so all subbands remain in a single flat array.

---

## Coefficient Encoding (ZigZag + Escape)

Predict-step detail coefficients are signed, in `[−M, M]` where `M` is the maximum
pixel value. After zigzag encoding values up to ±32767 fit in a single `uint16`.
Larger values (rare; mainly low-pass coefficients in full-range CT) use a 3-word
escape: sentinel `65535` + high 16 bits + low 16 bits of the raw `int32`.

After encoding, the `uint16` stream goes through **RLE** (same same/diff-run
protocol as the primary Delta pipeline) then **FSE**.

---

## Compression Pipelines

### WaveletV2RLEFSECompressU16 (scalar)

```
uint16 pixels
  → int32 (widening cast)
  → 5-level 2D 5/3 forward wavelet  (wt53Forward2DSeparated)
  → subband-order scan (LL → detail bands coarsest→finest)
  → ZigZag + escape encoding → []uint16
  → RleCompressU16
  → FSECompressU16FourState          ← 4 independent ANS states
  → [11-byte header] + compressed bytes
```

### WaveletV2SIMDRLEFSECompressU16 (SIMD + 4-state FSE)

```
uint16 pixels
  → int32 (widening cast)
  → 5-level 2D 5/3 forward wavelet  (wt53Forward2DSeparatedSIMD)
      ↳ blocked column pass (8 columns per cache-line)
      ↳ AVX2 predict/update kernels  (wt53PredictAVX2, wt53UpdateAVX2)
  → subband-order scan
  → ZigZag + escape encoding → []uint16
  → RleCompressU16
  → FSECompressU16FourState          ← 4 independent ANS states
  → [11-byte header] + compressed bytes

Decompression:
  → FSEDecompressU16FourState
      ↳ fse4StateDecompNative
          ↳ fse4StateDecompKernel    (BMI2/AVX2 assembly, AMD64)
  → RleDecompressU16
  → ZigZag + escape decode
  → 5-level 2D 5/3 inverse wavelet  (wt53Inverse2DSeparatedSIMD)
      ↳ AVX2 inverse kernels         (wt53InvUpdateAVX2, wt53InvPredictAVX2)
  → uint16 pixels
```

The two pipelines produce **bit-identical compressed streams** — files are
interchangeable between scalar and SIMD paths.

---

## Source Files

| File | Contents |
|------|----------|
| [`waveletu16.go`](../waveletu16.go) | `wt53Forward2DSeparated`, `wt53Inverse2DSeparated`, blocked SIMD variants, scalar helpers |
| [`waveletfsecompressu16.go`](../waveletfsecompressu16.go) | All four wavelet pipelines, `collectSubbandOrder`, `waveletCoeffsToU16`, zigzag helpers |
| [`wavelet_simd_amd64.go`](../wavelet_simd_amd64.go) | AVX2 dispatch: `wt53PredictBlocks`, `wt53UpdateBlocks`, and inverse counterparts |
| [`wavelet_simd_amd64.s`](../wavelet_simd_amd64.s) | Plan 9 AVX2 assembly kernels |
| [`wavelet_simd_arm64.go`](../wavelet_simd_arm64.go) | ARM64 scalar fallback (blocked layout still improves cache) |
| [`fse4state.go`](../fse4state.go) | `FSECompressU16FourState`, `FSEDecompressU16FourState`, 4-state decode loop |
| [`asm_amd64.go`](../asm_amd64.go) / [`asm_amd64.s`](../asm_amd64.s) | `fse4StateDecompKernel`: BMI2 assembly fast path for 4-state FSE decode |
| [`waveletu16_test.go`](../waveletu16_test.go) | Roundtrip, SIMD-vs-scalar, and benchmark tests |

---

## Benchmark Results

All measurements on **Intel Xeon @ 2.80 GHz, 4 cores, `benchtime=10x`**.
The benchmark spawns one goroutine per iteration — 10 goroutines run concurrently,
reflecting real-world multi-frame decode parallelism. Throughput is reported as
MB/s of **raw pixel data** (before compression).

### Compression Ratio

| Modality | Dimensions | Delta+RLE+FSE | Wavelet V2 (scalar) | Wavelet V2 (SIMD) |
|----------|-----------|:---:|:---:|:---:|
| MR  | 256×256    | 2.35× | 2.38× | **2.38×** |
| CT  | 512×512    | **2.24×** | 1.67× | 1.67× |
| CR  | 2140×1760  | 3.63× | 3.81× | **3.81×** |
| XR  | 2048×2577  | 1.74× | 1.76× | **1.76×** |
| MG1 | 2457×1996  | 8.57× | 8.67× | **8.67×** |
| MG2 | 2457×1996  | 8.55× | 8.65× | **8.65×** |
| MG3 | 4774×3064  | 2.24× | 2.32× | **2.32×** |
| MG4 | 4096×3328  | 3.47× | 3.59× | **3.59×** |

The Wavelet V2 pipeline (5 levels) achieves **equal or better compression ratio
than Delta+RLE+FSE on 7 of 8 modalities**. The sole exception is CT, where the
full 16-bit dynamic range forces the escape-encoding path for low-pass coefficients,
inflating the stream.

### Decompression Speed (MB/s)

| Modality | Delta+RLE+FSE | Wavelet V2 (scalar)+4FSE | Wavelet V2 (SIMD)+4FSE | SIMD speedup |
|----------|:---:|:---:|:---:|:---:|
| MR  | **186** | 150 | 165 | +10% |
| CT  | **281** | 152 | 190 | +25% |
| CR  | **302** | 166 | 210 | +27% |
| XR  | **513** | 193 | 214 | +11% |
| MG1 | **860** | 182 | 227 | +25% |
| MG2 | **729** | 193 | 241 | +25% |
| MG3 | **466** | 118 | 112 | — † |
| MG4 | **826** | 144 | 198 | +38% |

† MG3 SIMD vs scalar difference is within measurement noise on this configuration.

Delta+RLE+FSE maintains a significant decompression speed advantage (1.1×–3.8×)
because it requires only one memory pass versus the wavelet's two full passes
(rows + columns, both forward and inverse), and it operates on `uint16` (2 bytes)
while the wavelet operates on `int32` (4 bytes), doubling cache pressure.

---

## Comparison with HTJ2K

HTJ2K (High Throughput JPEG 2000) uses the same 5/3 integer wavelet as MIC's
wavelet pipeline. The ratio comparison is therefore **directly algorithmic**: any
difference in ratio reflects differences in entropy coding, subband ordering,
context modeling, and coefficient quantisation strategy rather than the transform.

HTJ2K reference implementation: [OpenJPH](https://github.com/aous72/OpenJPH)
v0.15 (`ojph_compress -reversible true`). HTJ2K throughput measured single-threaded
on Apple M2 Max (different hardware to the ratio benchmarks above — ratio numbers
are hardware-independent).

### Compression Ratio Comparison

| Modality | Dimensions | Delta+RLE+FSE | Wavelet V2 SIMD | HTJ2K (OpenJPH) | MIC Wavelet vs HTJ2K |
|----------|-----------|:---:|:---:|:---:|:---:|
| MR  | 256×256   | 2.35× | 2.38× | 2.38× | **tied** |
| CT  | 512×512   | 2.24× | 1.67× | 1.77× | −6% |
| CR  | 2140×1760 | 3.63× | **3.81×** | 3.77× | **+1%** |
| XR  | 2048×2577 | 1.74× | **1.76×** | 1.67× | **+5%** |
| MG1 | 2457×1996 | 8.57× | **8.67×** | 8.25× | **+5%** |
| MG2 | 2457×1996 | 8.55× | **8.65×** | 8.24× | **+5%** |
| MG3 | 4774×3064 | 2.24× | **2.32×** | 2.22× | **+4%** |
| MG4 | 4096×3328 | 3.47× | **3.59×** | 3.51× | **+2%** |

MIC's wavelet V2 pipeline **matches or exceeds HTJ2K ratios on 7 of 8 modalities**.
Only CT is lower (−6%), due to the escape encoding needed for full-range 16-bit
low-pass coefficients. On mammography (MG1/MG2), MIC is +5% better than HTJ2K.

The ratio advantage comes from MIC's 5-level subband-order scan combined with
the 16-bit-native RLE: long runs of near-zero detail coefficients in the HL/LH/HH
subbands compress more effectively through same-run RLE than through HTJ2K's
context-adaptive arithmetic coder on these smooth medical images.

### Decompression Speed Comparison

All measurements below are **Apple M2 Max, ARM64, single-threaded, in-process** (no
subprocess, no file I/O). HTJ2K uses CGO bindings to libopenjph (`BenchmarkHTJ2KFairDecomp`,
`BenchmarkThreeWay` in `ojph/`). All values in **MB/s** over uncompressed pixel bytes.

| Modality | MIC Delta+RLE+FSE (Go) | MIC-4state-C | Wavelet V2 SIMD | HTJ2K (OpenJPH) |
|----------|:---:|:---:|:---:|:---:|
| MR (256×256)      | 145 | 350 | **464** | 261 |
| CT (512×512)      | 181 | **375** | 538 | 292 |
| CR (2140×1760)    | 290 | **532** | 784 | 358 |
| XR (2048×2577)    | 301 | **529** | 878 | 317 |
| MG1 (2457×1996)   | 472 | 684 | **1129** | 790 |
| MG2 (2457×1996)   | 473 | 688 | **1069** | 794 |
| MG3 (4774×3064)   | 304 | **534** | 716 | 334 |
| MG4 (4096×3328)   | 415 | **627** | 827 | 551 |

**Wavelet V2 SIMD** decompression exceeds HTJ2K on **all 8 modalities** — including
mammography where HTJ2K's own SIMD wavelet decoder achieves 790–794 MB/s. The SIMD
blocked-column wavelet transform (8 columns per cache line, AVX2 kernels) combined
with 4-state FSE produces this result despite the wavelet's 4× memory bandwidth overhead
vs delta decode.

For **pure-Go** (no CGO) deployments, `MIC-4state` (Go+NEON) beats HTJ2K on CR, XR,
MG3, MG4, and is within 15% on others. MIC-4state-C (CGO) beats HTJ2K on 6/8.

---

## Analysis: Why V2 (5-Level) Beats V1 (1-Level) on Compression Ratio

The earlier analysis (V1, 1-level wavelet without subband ordering) showed the
wavelet underperforming Delta+RLE+FSE on compression ratio. The V2 pipeline
reverses this for 7 of 8 modalities through two improvements:

**1. Multi-level decomposition (5 levels)**

Each additional wavelet level further decorrelates the LL subband. On smooth medical
images, 5 levels of LL compaction concentrates signal energy in a tiny region,
leaving vast HH/LH/HL subbands filled with near-zero coefficients. RLE then
encodes these as single same-run headers.

**2. Subband-order scan**

The original code scanned coefficients in row-major order (interleaving LL and
detail coefficients). `collectSubbandOrder` reorders them as:
`LL(level 5) → HL5 → LH5 → HH5 → HL4 → … → HH1`. This groups the near-zero
detail coefficients together so RLE's same-run encoding is maximally effective —
a single same-run header can cover thousands of consecutive near-zero values.

**3. 4-state FSE with SIMD decoder**

Switching from 1-state FSE to `FSECompressU16FourState` produces the same
compressed bytes (4-state FSE is a stream format difference, not a different
entropy model). The benefit is on the decoder side: the 4 independent ANS state
machines break the serial dependency chain, and on AMD64 the BMI2 assembly kernel
(`fse4StateDecompKernel`) processes the bulk of the stream without bounds checks.

---

## Analysis: Why Delta+RLE+FSE Still Wins on Decompression Speed

Even with SIMD acceleration, the wavelet decompressor is 1.1×–3.8× slower than
Delta+RLE+FSE on large images (MG1: 860 vs 227 MB/s). The reasons:

1. **Two full image passes, twice each**: Inverse wavelet requires undo-update +
   undo-predict on columns, then undo-update + undo-predict on rows. That is
   4× the memory bandwidth of a single delta-decode pass.

2. **int32 vs uint16 working set**: Wavelet operates on `int32` (4 B/element);
   delta operates on `uint16` (2 B/element). For MG1 at 9.35 MB raw, the wavelet
   working set is ~18 MB of int32 — far exceeding L2 cache on a single core.

3. **Escape decoding branch**: Full-range CT images force the `if in[i] !=
   waveletEscape` branch on every symbol, polluting the branch predictor.

4. **RLE interaction**: The wavelet's subband-order scan is optimised for
   *compression* (groups near-zeros together for RLE). But long same-runs in
   detail subbands still require RLE overhead to decode, unlike Delta's simple
   recurrence.

---

## Where Wavelet+FSE Excels

| Scenario | Reason |
|----------|--------|
| **Compression ratio priority** | Wavelet V2 SIMD matches or beats Delta+RLE+FSE on 7/8 modalities and HTJ2K on 7/8 |
| **HTJ2K compatibility** | Same 5/3 integer wavelet; ratios directly comparable and competitive |
| **Lossy compression (future)** | Quantise HH/LH/HL coefficients by subband; large ratio gains with controlled quality loss |
| **Progressive delivery (future)** | Each LL subband level is a valid downscaled image; Delta coding does not support this |
| **Non-CT 16-bit images** | CT's full 16-bit range triggers the escape path; other modalities (10–14 bit effective depth) are unaffected |

---

## Summary

| Pipeline | Compression ratio | Decompression speed | vs HTJ2K ratio |
|----------|:----------------:|:-------------------:|:--------------:|
| Delta+RLE+FSE (primary) | Good (wins on CT) | **Best** (1–4× faster than wavelet) | N/A (different approach) |
| Wavelet V2 scalar + 4-state FSE | **Better** on 7/8 | 1.1–3.8× slower | Competitive |
| Wavelet V2 SIMD + 4-state FSE | **Better** on 7/8 | 1.1–3.8× slower | **Matches or beats on 7/8** |
| HTJ2K (OpenJPH lossless) | Similar to Wavelet V2 | N/A (subprocess) | — |

**Recommendation:**
- Use **Delta+RLE+FSE** (the default `CompressSingleFrame`) for production lossless
  when decompression throughput matters (real-time rendering, multi-frame playback).
- Use **WaveletV2SIMDRLEFSECompressU16** when compression ratio is the priority and
  compatibility with the JPEG 2000 / HTJ2K transform family is desirable.
- The wavelet pipeline is the natural foundation for a **future lossy or progressive
  mode**.

---

## Reproducing the Benchmarks

```bash
# Full pipeline comparison: Delta+RLE+FSE vs Wavelet V2 scalar vs Wavelet V2 SIMD
go test -benchmem -run=^$ -benchtime=10x \
  -bench "^(BenchmarkDeltaRLEFSECompress|BenchmarkWaveletV2RLEFSECompress|BenchmarkWaveletV2SIMDRLEFSECompress)$" mic

# Lossless roundtrip verification
go test -run "TestWaveletV2SIMDRLEFSECompress|TestWaveletSIMDMatchesScalar" -v

# HTJ2K comparison (requires OpenJPH installed as ojph_compress / ojph_expand)
go test -run TestHTJ2KComparison -v -timeout 300s
```
