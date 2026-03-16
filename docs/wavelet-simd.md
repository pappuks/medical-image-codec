# SIMD-Accelerated Wavelet Transform

## Overview

`WaveletV2SIMDRLEFSECompressU16` / `WaveletV2SIMDRLEFSEDecompressU16` are drop-in
replacements for the scalar V2 wavelet pipeline that improve single-threaded
decompression throughput by **14–47%** on AMD64 (AVX2/Haswell+) with **no change
to the compressed byte stream**. The same `.mic` file decompresses correctly through
either path.

---

## Background: The 5/3 Integer Wavelet

MIC implements the Le Gall 5/3 integer lifting wavelet — the same transform used by
JPEG 2000 for lossless (reversible) coding. The 1D forward transform applies two
lifting steps to a row or column of `n` values:

```
Predict:  d[i] = x[2i+1] − ⌊(x[2i] + x[2i+2]) / 2⌋     i = 0..⌊n/2⌋−1
Update:   s[i] = x[2i]   + ⌊(d[i−1] + d[i] + 2) / 4⌋   i = 0..⌈n/2⌉−1
```

Boundaries are mirrored: `x[−1] = x[1]` (left), `x[n] = x[n−2]` (right).

After forward lifting the `n` values are *de-interleaved* into two half-length
sequences: low-pass (scaling) coefficients `s[0..nLow−1]` and high-pass (detail)
coefficients `d[0..nHigh−1]`. The 2D transform applies the 1D transform
independently to rows (horizontal) then columns (vertical).

The output uses the **Mallat layout**: after `L` levels of decomposition the
coefficients are arranged in quadrants:

```
┌───────┬───────┐
│  LL   │  LH   │
│ (low) │       │
├───────┼───────┤
│  HL   │  HH   │
│       │       │
└───────┴───────┘
```

This layout is produced by `wt53Forward2DSeparated` (scalar) and
`wt53Forward2DSeparatedSIMD` (SIMD). For multi-level transforms the LL quadrant is
recursively subdivided.

---

## Performance Problem: The Column Pass

For a 2140×1760 image the horizontal row pass is cache-friendly — it scans each
row sequentially. The vertical column pass is not.

With the scalar per-column loop:

```
for x := 0; x < cols; x++ {
    wt53Forward1D(data, x, rows, fullCols)   // stride = fullCols
    deinterleave(column x)
}
```

Each call to `wt53Forward1D` accesses elements at byte offsets `x·4`,
`(x + fullCols)·4`, `(x + 2·fullCols)·4`, … — a stride of
`fullCols × 4 = 2140 × 4 = 8560 bytes` between consecutive accesses in the same
column. That is **133 cache lines apart**. Every element access is a cold miss for
large images whose working set exceeds L2 cache.

Total cache misses for the column pass of a 2140×1760 image:
```
cols × rows = 2140 × 1760 ≈ 3.8 M accesses, each likely a cache miss
```

The column pass dominates runtime for images wider than ~256 pixels.

---

## Optimisation 1: Blocked Column Pass

The blocked approach processes **8 consecutive columns at a time**:

```
for x0 := 0; x0 < cols; x0 += 8 {
    // Predict for rows 0..rows−1, columns x0..x0+7
    for i := 0; i < nRowHigh; i++ {
        data[(2i+1)·stride + x0..x0+7] -=
            (data[2i·stride + x0..x0+7] + data[(2i+2)·stride + x0..x0+7]) >> 1
    }
    // Update …
}
```

When the inner loop loads `data[row·stride + x0]` to `data[row·stride + x0+7]`,
those 8 `int32` values occupy exactly one 32-byte cache line. The **same load**
serves all 8 columns simultaneously. Cache miss count is reduced ~8×:

```
Before (per-column):  cols × rows cache misses   ≈ 3.8 M
After  (blocked 8):   (cols/8) × rows cache misses ≈ 475 K
```

This alone accounts for the majority of the speedup on wide images like CR
(2140×1760, +47%) and XR (2048×2577, +32%).

Crucially, blocking by column is **semantically correct**: the predict and update
steps for column `x` depend only on values in that column (no cross-column data
dependency), so all columns in a block can be processed together at each row.

---

## Optimisation 2: AVX2 Predict/Update Kernels

The blocked layout produces per-row sub-slices of 8 contiguous `int32` values. Two
AVX2 kernels operate on these sub-slices:

### `wt53PredictAVX2` — predict step

```c
// odd[i] -= (left[i] + right[i]) >> 1   for i = 0..n−1
```

Plan 9 assembly (`wavelet_simd_amd64.s`):

```asm
wt53pred_loop:
    VMOVDQU (SI), Y0          // left[i..i+7]   — 8 × int32
    VMOVDQU (DI), Y1          // right[i..i+7]
    VMOVDQU (BX), Y2          // odd[i..i+7]
    VPADDD  Y1, Y0, Y3        // left + right
    VPSRAD  $1, Y3, Y3        // arithmetic >> 1
    VPSUBD  Y3, Y2, Y2        // odd − shift
    VMOVDQU Y2, (BX)
    ADDQ    $32, SI
    ADDQ    $32, DI
    ADDQ    $32, BX
    DECQ    CX
    JNZ     wt53pred_loop
    VZEROUPPER
```

`VPSRAD` performs a signed (arithmetic) right shift, correctly rounding towards
`−∞` for negative residuals — essential for lossless reconstruction.

### `wt53UpdateAVX2` — update step

```c
// even[i] += (dLeft[i] + dRight[i] + 2) >> 2   for i = 0..n−1
```

The constant `2` (the rounding bias) is broadcast into a YMM register once before
the loop and reused every iteration, avoiding a load each cycle.

```asm
    VPBROADCASTD X4, Y4       // Y4 = {2, 2, 2, 2, 2, 2, 2, 2}

wt53upd_loop:
    VMOVDQU (SI), Y0           // dLeft[i..i+7]
    VMOVDQU (DI), Y1           // dRight[i..i+7]
    VMOVDQU (BX), Y2           // even[i..i+7]
    VPADDD  Y1, Y0, Y3         // dLeft + dRight
    VPADDD  Y4, Y3, Y3         // + 2
    VPSRAD  $2, Y3, Y3         // arithmetic >> 2
    VPADDD  Y3, Y2, Y2         // even + shift
    VMOVDQU Y2, (BX)
    …
```

### Inverse kernels

Two additional kernels undo the forward steps for decompression:

| Kernel | Operation |
|--------|-----------|
| `wt53InvUpdateAVX2` | `even[i] -= (dLeft[i] + dRight[i] + 2) >> 2` |
| `wt53InvPredictAVX2` | `odd[i]  += (left[i]  + right[i])  >> 1` |

These are structurally identical to the forward kernels with `VPSUBD` / `VPADDD`
swapped.

### Dispatch

On AMD64, a one-time `init()` sets `cpuHasAVX2` via `CPUID` leaf 7 EBX bit 5
(standard Haswell detection). `wt53PredictBlocks` and `wt53UpdateBlocks` dispatch to
AVX2 for the n⌊/8⌋·8 aligned interior and fall through to scalar for the tail (at
most 7 elements):

```go
func wt53PredictBlocks(left, right, odd unsafe.Pointer, n int) {
    n8 := n &^ 7                     // round down to multiple of 8
    if n8 > 0 && cpuHasAVX2 {
        wt53PredictAVX2(left, right, odd, n8)
    } else {
        n8 = 0
    }
    if n8 < n {
        wt53PredictScalar(           // handle tail (0–7 elements)
            unsafe.Add(left,  n8*4),
            unsafe.Add(right, n8*4),
            unsafe.Add(odd,   n8*4),
            n-n8,
        )
    }
}
```

On ARM64 and other platforms the dispatch stubs call the scalar implementations
directly; the blocked layout still improves cache behaviour on those platforms.

---

## Transform Layout: Why No De-interleaving Inside the Kernel

A common SIMD approach to the wavelet row transform loads interleaved even/odd
elements (`x0, x1, x2, x3, …`) and de-interleaves them inside the kernel before
computing the predict step. This requires 6 shuffle instructions per 16 elements
(`VPUNPCKLPS`, `VPUNPCKHPS`, `VPERMD`) and complicates the kernel.

The blocked column pass avoids this entirely. For the vertical transform, elements
at positions `(row, x0)` to `(row, x0+7)` are *already consecutive* in memory —
there is no interleaving. The AVX2 kernels load 8 elements from three rows and
compute the lifting step directly with 4 instructions per 8 elements.

The horizontal row pass uses the existing scalar `wt53Forward1D` (which is
cache-friendly and short), followed by a scalar de-interleave. The column pass —
which is the bottleneck — benefits from SIMD without any de-interleave complexity.

---

## Benchmark Results

**Platform**: Intel Xeon @ 2.10 GHz, single-threaded (`GOMAXPROCS=1` per
goroutine), 5-level forward + inverse transform, measured with
`BenchmarkWaveletV2RLEFSECompress` /
`BenchmarkWaveletV2SIMDRLEFSECompress` (`-benchtime=5x`).

### Decompression throughput (MB/s of raw pixel data)

| Modality | Dimensions | Scalar | SIMD | Speedup | Compression ratio |
|----------|-----------|:------:|:----:|:-------:|:-----------------:|
| MR   | 256×256     |  93 | 118 | **+27%** | 2.38× |
| CT   | 512×512     | 136 | 159 | **+17%** | 1.67× |
| CR   | 2140×1760   | 158 | 232 | **+47%** | 3.81× |
| XR   | 2048×2577   | 183 | 241 | **+32%** | 1.76× |
| MG1  | 2457×1996   | 212 | 257 | **+21%** | 8.67× |
| MG2  | 2457×1996   | 221 | 255 | **+15%** | 8.65× |
| MG3  | 4774×3064   | 141 | 171 | **+21%** | 2.37× |
| MG4  | 4096×3328   | 175 | 201 | **+14%** | 3.59× |

**Why CR shows the largest gain (+47%)**:
CR (2140×1760) has the widest row width in the test set relative to L2 cache. Its
column pass accesses rows 8560 bytes apart. The blocked layout converts that pattern
from 3.77 M individual cache misses to ~472 K block loads, and the AVX2 kernels
eliminate the per-element branch overhead in the tight inner loop.

**Why MG4 shows the smallest gain (+14%)**:
MG4 at 4096×3328 is large enough that even the blocked pass is memory-bandwidth
bound. The AVX2 kernels help but the bottleneck shifts to DRAM bandwidth rather than
cache-miss count.

---

## Correctness Guarantee

The SIMD and scalar transforms produce **bit-identical output** on every input
tested. This is verified by `TestWaveletSIMDMatchesScalar` which compares the full
forward + inverse transform coefficient arrays at every image size in the test suite:

```bash
go test -run TestWaveletSIMDMatchesScalar -v
```

The compressed stream format is identical to `WaveletV2RLEFSECompressU16` — the
11-byte header and FSE-compressed RLE payload are interchangeable:

```
Header (11 bytes):
  [0..3]  rows    uint32 LE
  [4..7]  cols    uint32 LE
  [8..9]  maxValue uint16 LE
  [10]    levels  uint8
Body:
  FSE-compressed RLE stream of ZigZag-encoded wavelet coefficients
```

`WaveletV2SIMDRLEFSEDecompressU16` accepts streams produced by either compressor.

---

## Implementation Files

| File | Contents |
|------|----------|
| `wavelet_simd_amd64.go` | AMD64 `//go:noescape` declarations; `wt53PredictBlocks` / `wt53UpdateBlocks` / inverse dispatch functions; `cpuHasAVX2` gate |
| `wavelet_simd_amd64.s` | Plan 9 AVX2 assembly: `wt53PredictAVX2`, `wt53UpdateAVX2`, `wt53InvPredictAVX2`, `wt53InvUpdateAVX2` |
| `wavelet_simd_arm64.go` | ARM64 build tag: scalar fallback; blocked layout still benefits cache |
| `wavelet_simd_generic.go` | `!amd64 && !arm64` build tag: scalar fallback for WASM, RISC-V, etc. |
| `waveletu16.go` | `wt53Forward2DSeparatedSIMD` / `wt53Inverse2DSeparatedSIMD` (blocked 2D transform); scalar helper functions (`wt53PredictScalar`, `wt53UpdateScalar`, and inverse variants) |
| `waveletfsecompressu16.go` | `WaveletV2SIMDRLEFSECompressU16` / `WaveletV2SIMDRLEFSEDecompressU16` public API |
| `waveletu16_test.go` | `TestWaveletSIMD2DRoundTrip`, `TestWaveletSIMDMatchesScalar`, `TestWaveletV2SIMDRLEFSECompress`, `BenchmarkWaveletV2SIMDRLEFSECompress` |

---

## Running Tests and Benchmarks

```bash
# Lossless roundtrip at various dimensions (4×4 … 256×256)
go test -run TestWaveletSIMD2DRoundTrip -v

# Bit-exact comparison vs scalar at larger sizes (8×8 … 256×512)
go test -run TestWaveletSIMDMatchesScalar -v

# End-to-end compress + decompress on all DICOM test images
go test -run TestWaveletV2SIMDRLEFSECompress -v

# Side-by-side scalar vs SIMD benchmark
go test -benchmem -run=^$ -benchtime=5x \
  -bench "^(BenchmarkWaveletV2RLEFSECompress|BenchmarkWaveletV2SIMDRLEFSECompress)$" mic
```

Sample output (Intel Xeon @ 2.10 GHz, `GOMAXPROCS=4`):

```
BenchmarkWaveletV2RLEFSECompress/CR-4      158 MB/s   3.811 ratio
BenchmarkWaveletV2SIMDRLEFSECompress/CR-4  232 MB/s   3.811 ratio   ← +47%

BenchmarkWaveletV2RLEFSECompress/MG1-4     212 MB/s   8.666 ratio
BenchmarkWaveletV2SIMDRLEFSECompress/MG1-4 257 MB/s   8.666 ratio   ← +21%
```

---

## Extension Points

### Vectorise the horizontal row pass

The current horizontal pass uses `wt53Forward1D` (scalar lifting + de-interleave).
For images wider than ~512 pixels the row pass is a secondary bottleneck. The
bottleneck is the de-interleave of interleaved even/odd elements, which requires a
6-instruction AVX2 shuffle sequence:

```
// De-interleave 16 consecutive int32 into 8 evens + 8 odds:
//   VPUNPCKLPS Y0, Y1, T0
//   VPUNPCKHPS Y0, Y1, T1
//   VPUNPCKLPS T0, T1, T2
//   VPUNPCKHPS T0, T1, T3
//   VPERMD     T2, perm, Ye   // perm = [0,1,4,5,2,3,6,7]
//   VPERMD     T3, perm, Yo
```

After de-interleave the predict and update steps match the existing AVX2 kernels
exactly. Estimated additional gain: ~5–15% on wide images.

### NEON vectorisation (ARM64)

ARM64 NEON `LD1 {V0.4S}` loads 4 × int32 per register. The predict/update
operations map directly to `ADD`, `SSHR`, `SUB` vector instructions. The blocked
layout eliminates the de-interleave requirement in the column pass, making the ARM64
NEON implementation structurally identical to the AMD64 version. Estimated gain on
AWS Graviton 3 (NEON): +10–30% column pass, similar to the AMD64 scalar→AVX2 ratio
scaled by NEON width (4 vs 8 per register).

### Wavelet for lossy/progressive compression

The existing SIMD infrastructure compresses losslessly. For a future lossy mode,
quantising the high-frequency subband coefficients (`d[]` values) before RLE+FSE
encoding would reduce bitrate with controlled quality loss — the same approach used
by JPEG 2000 lossy mode. The blocked layout and AVX2 kernels carry over unchanged;
only the encoding step after `collectSubbandOrder` would need a quantisation pass.
