# MIC Benchmarks

This document is the reference for every benchmark in the repository: what each
one measures, how it's structured, how to run it, and which paper table (if
any) it feeds. The benchmarks fall into two structural groups (serial and
parallel) — be sure to read the methodology section before comparing numbers
across groups, because the units are different.

---

## 1. Methodology — serial vs parallel benchmarks

The MIC benchmarks come in two structurally different shapes.

### Serial benchmarks — single-thread throughput

```go
for i := 0; i < b.N; i++ {
    decode(blob)
}
```

The reported `MB/s` is the throughput of a single decompression on a single
core. This is what should be reported in the paper for all "single-threaded
variant" columns (MIC-Go, MIC-4state, MIC-4state-C, MIC-4state-SIMD, HTJ2K,
JPEG-LS, Wav+SIMD, etc.).

### Parallel benchmarks — aggregate multi-core throughput

```go
var wg sync.WaitGroup
for i := 0; i < b.N; i++ {
    wg.Add(1)
    go func() { defer wg.Done(); decode(blob) }()
}
wg.Wait()
```

This launches `b.N` goroutines concurrently and waits for all of them. The
reported `MB/s` is the **aggregate throughput** across all available P-cores.
With `-benchtime=10x` on a 14-core machine, all 10 goroutines run in parallel,
so the number scales with `min(b.N, GOMAXPROCS)` and is several × the serial
number.

This is what the FSE entropy-coder microbenchmarks (`tab:fse-combined`) and the legacy
pipeline benchmarks in [fseu16_test.go](../fseu16_test.go) report. **Do not
compare a parallel benchmark's MB/s against a serial benchmark's MB/s** — they
are not on the same scale.

If you need single-thread throughput from a parallel benchmark, run it with
`-benchtime=1x` (one iteration, one goroutine).

### Quick check: is a given benchmark serial or parallel?

```bash
grep -A2 "func BenchmarkXxx" some_test.go | grep -E "wg.Add|go func|sync.WaitGroup"
```

The inventory in §6 below tags each benchmark accordingly.

---

## 2. Running the paper benchmarks

All numbers in [paper/mic-paper-v9-ieee-tmi.tex](../paper/mic-paper-v9-ieee-tmi.tex)
are produced by [run-paper-benchmarks.sh](../run-paper-benchmarks.sh), which
runs the seven benchmarks below and post-processes them with
[paper-tables.py](../paper-tables.py).

```bash
# Full paper suite (10x iterations, takes ~10 min on M2 Max).
./run-paper-benchmarks.sh

# Faster smoke test.
BENCHTIME=3x ./run-paper-benchmarks.sh

# Custom output directory.
OUTDIR=/tmp/mic-bench ./run-paper-benchmarks.sh
```

The script writes per-benchmark output into `results/<timestamp>/` and then
emits `paper-tables.txt` in that same directory with the ASCII versions of all
four paper tables.

Prerequisite for the cgo benchmarks: `libopenjph` and `libcharls` must be
installed (`brew install openjph charls` on macOS). The script preflights this
and fails fast.

### Mapping benchmarks → paper tables

v9 combined the former per-platform encode/decode tables into single
ARM64+AMD64 tables (`tab:enc`, `tab:decomp`) and added the multi-threaded
`tab:pics`.

| Paper table | Source benchmark | File produced | Parallelism |
|---|---|---|---|
| `tab:ratios` — compression ratios (all variants, 39 images) | `BenchmarkAllCodecs` (ratios) + `BenchmarkMICCDeltaZstdDecomp` (Δ+Zstd-19) + `BenchmarkWaveletV2SIMDRLEFSECompress` (wavelet) | `01-…txt`, `05a-…txt`, `06-…txt` | serial |
| `tab:enc` — encoding throughput (ARM64 + AMD64) | `BenchmarkAllCodecsEncode` + `BenchmarkMICCDeltaZstdEnc` | `02-…txt`, `05b-…txt` | serial |
| `tab:decomp` — decompression throughput (ARM64 + AMD64) | `BenchmarkAllCodecs` + `BenchmarkMICCDeltaZstdDecomp` | `01-…txt`, `05a-…txt` | serial |
| `tab:pics` — multi-threaded (strip-parallel) decompression | `BenchmarkAllCodecs` (`MIC-8state-SIMD` + `PICS-C-2/4/8`) | `01-…txt` | serial loop, internally-parallel C |
| `tab:fse-combined` — FSE 1/4/8-state microbench | `BenchmarkFSEDecompress` + `BenchmarkFSEDecompress4State` + `BenchmarkFSEDecompress8State` | `03-…txt`, `04-…txt`, `10-…txt` | **parallel** |

`tab:fse-combined` reports **aggregate parallel FSE-only throughput** by
design — that table is a microbenchmark of the entropy coder running flat-out
across all cores, isolated from the surrounding pipeline. `tab:decomp` reports
**single-thread end-to-end** throughput for the full Delta+RLE+FSE pipeline.
The numbers are not directly comparable.

The PICS columns in `tab:pics` (`PICS-C-2/4/8`) are *also* parallel, but the
parallelism is internal to the codec (pthread-based strip decoder), driven by
a serial benchmark loop. That correctly measures wall-clock decode time of one
image using N threads — apples to apples with the other single-image columns.

---

## 3. Codec comparison benchmarks

### `BenchmarkAllCodecs` ([ojph/mic_c_test.go:371](../ojph/mic_c_test.go#L371))

Decompression throughput (serial) across every codec variant on the full
**39-image test corpus** (`testImages` in
[ojph/htj2k_fair_comparison_test.go](../ojph/htj2k_fair_comparison_test.go);
NEMA WG-04 + GDCM samples + TCIA diagnostic slices — see the README "Test
Dataset" section). Reports MB/s and compression ratio for each (image, codec)
combination. Requires `-tags cgo_ojph`. Paper-table numbers come from
`run-paper-benchmarks.sh` on the M4 Pro / c8i reference hardware per
[`.claude/benchmark-rules.md`](../.claude/benchmark-rules.md) §4 — the M2 Max
numbers in the README are in-process reference values, not paper numbers.

Variants exercised:

- `MIC-Go` — pure-Go 2-state FSE pipeline (Delta+RLE+FSE)
- `MIC-4state` — pure-Go 4-state FSE pipeline
- `MIC-4state-C` — C 4-state decoder via CGO
- `MIC-4state-SIMD` — C 4-state decoder with platform SIMD (BMI2 on AMD64, scalar on ARM64)
- `MIC-C` — C 2-state decoder
- `MIC-SIMD` — C 2-state decoder with SIMD
- `HTJ2K` — OpenJPH in-process
- `JPEGLS` — CharLS in-process
- `JXL` — libjxl in-process (JPEG XL modular lossless, effort 7)
- `PICS-N` for N ∈ {2, 4, 8} — Go strip decoder with N goroutines
- `PICS-C-N` for N ∈ {2, 4, 8} — C strip decoder with N pthreads + per-strip SIMD

```bash
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x \
  -bench '^BenchmarkAllCodecs$' ./ojph/
```

### `BenchmarkAllCodecsEncode` ([ojph/mic_c_test.go:528](../ojph/mic_c_test.go#L528))

Encoding-side counterpart of the above. Same variant list, plus `Wavelet+SIMD`
(serial wavelet compress). Powers `tab:enc`.

```bash
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x \
  -bench '^BenchmarkAllCodecsEncode$' ./ojph/
```

### `BenchmarkHTJ2KFairDecomp` ([ojph/htj2k_fair_comparison_test.go:285](../ojph/htj2k_fair_comparison_test.go#L285))

Sanity cross-check for the HTJ2K column in `BenchmarkAllCodecs`. Same
methodology (in-process CGO), narrower scope. Serial.

### `BenchmarkJPEGLSDecomp` ([ojph/jpegls_comparison_test.go:177](../ojph/jpegls_comparison_test.go#L177))

Sanity cross-check for the JPEG-LS column. Also reports MIC, MIC-4state,
MIC-4state-C, MIC-4state-SIMD side-by-side. Serial.

### `BenchmarkJXLDecomp` / `BenchmarkJXLEncode` ([ojph/jxl_comparison_test.go](../ojph/jxl_comparison_test.go))

In-process JPEG XL (libjxl, modular lossless, effort 7) compared against MIC and
MIC-4state, mirroring the JPEG-LS framework. `BenchmarkJXLDecomp` measures
decompression MB/s; `BenchmarkJXLEncode` measures libjxl encode MB/s (much
slower than decode, so kept as a separate bench). `TestJXLComparison` prints the
full ratio + decode-speed table with a mean-ratio row; `TestJXLRoundtrip`
verifies lossless. Serial. Requires `brew install jpeg-xl`. Not a paper source
yet — treat as an external-codec cross-check like HTJ2K/JPEG-LS.

The wrapper pins libjxl to `JXL_BIT_DEPTH_FROM_CODESTREAM` on both encode
(`JxlEncoderSetFrameBitDepth`) and decode (`JxlDecoderSetImageOutBitDepth`, which
must be called *after* `JxlDecoderSetImageOutBuffer`). Without this, libjxl
rescales sub-16-bit samples by `65535/((1<<depth)-1)` and the lossless roundtrip
fails for <16-bit medical images (MR, MG, PET, etc.).

### `BenchmarkMICvsHTJ2K` ([htj2k_comparison_test.go:329](../htj2k_comparison_test.go#L329))

Older standalone MIC-vs-HTJ2K bench, predates `BenchmarkAllCodecs`. Kept for
historical comparison. Serial.

### `BenchmarkMICFullCPipelineVsHTJ2K` ([ojph/mic_c_test.go:695](../ojph/mic_c_test.go#L695))

End-to-end C pipeline (delta + RLE + FSE all in C) vs HTJ2K. Serial.

### `BenchmarkDeltaZstdDecompress` ([comparison_test.go:86](../comparison_test.go#L86))

Legacy Δ+Zstd-19 baseline via the `zstd` CLI (subprocess), so timings include
process-launch overhead. **Not** a paper source in v9 — the `tab:ratios`
Δ+Zstd-19 ratio column and the `tab:enc`/`tab:decomp` Δ+Zstd-19 throughput come
from the in-process `BenchmarkMICCDeltaZstd{Decomp,Enc}` (see
`.claude/benchmark-rules.md` §5). Treat this as a sanity-check fallback. Serial.

### `BenchmarkMEDPredictor` ([comparison_test.go:185](../comparison_test.go#L185))

MED predictor (median-edge-detection) vs the default avg-of-neighbors
predictor. Compares ratio + decompression throughput. Serial.

---

## 4. Entropy-coder microbenchmarks

These isolate the entropy step from the surrounding Delta+RLE pipeline. Input
is the Delta+RLE residual stream; output is the same stream after
entropy-decode. Bytes are counted over the *uncompressed RLE symbol stream*,
not the original pixels. **All benchmarks in this section are parallel
(aggregate multi-core throughput).**

### `BenchmarkFSEDecompress` ([fse2state_test.go:269](../fse2state_test.go#L269))

1-state vs 2-state FSE decompression isolated. Feeds `tab:fse-combined` (1-state column).

```bash
go test -benchmem -run=^$ -benchtime=10x -bench '^BenchmarkFSEDecompress$' .
```

### `BenchmarkFSEDecompress4State` ([fse4state_test.go:150](../fse4state_test.go#L150))

1-state vs 2-state vs 4-state FSE decompression. Feeds `tab:fse-combined` (4-state
column).

```bash
go test -benchmem -run=^$ -benchtime=10x -bench '^BenchmarkFSEDecompress4State$' .
```

### `BenchmarkRANSDecompress8State` ([rans8state_test.go:151](../rans8state_test.go#L151))

1/2/4-state FSE alongside an 8-state rANS variant. Exploratory — not used in
the paper.

### `BenchmarkFSE2StateSummary` ([fse2state_test.go:436](../fse2state_test.go#L436))

Human-readable speedup table printed when run with `-v`. Aggregates the
2-state-vs-1-state ratio per image. Parallel.

### `BenchmarkFSECompressCompare` ([fse2state_test.go:334](../fse2state_test.go#L334))

Compression-side 1-state vs 2-state. Serial.

### `BenchmarkDeltaRLEFSEDecompress` ([fse2state_test.go:371](../fse2state_test.go#L371))

Full Delta+RLE+FSE pipeline for both FSE variants. Parallel. Older —
superseded by `BenchmarkAllCodecs` for paper purposes.

---

## 5. Pipeline component microbenchmarks

Lower-level benches for individual stages. Most are in
[fseu16_test.go](../fseu16_test.go) and predate the paper-table refactor;
several use parallel goroutines and are flagged accordingly.

| Benchmark | File | Parallelism | What it measures |
|---|---|---|---|
| `BenchmarkDeltaRLEHuffCompress` | [fseu16_test.go:99](../fseu16_test.go#L99) | parallel | Full Delta+RLE+Huffman pipeline decompression |
| `BenchmarkDeltaRLEHuffCompress2` | [fseu16_test.go:140](../fseu16_test.go#L140) | serial | Same pipeline, fused decode path |
| `BenchmarkDeltaRLEFSECompress` | [fseu16_test.go:161](../fseu16_test.go#L161) | parallel | Full Delta+RLE+FSE pipeline decompression (legacy; use `BenchmarkAllCodecs` for paper) |
| `BenchmarkDeltaZZRLEHuffCompress` | [fseu16_test.go:193](../fseu16_test.go#L193) | serial | Delta+ZigZag+RLE+Huffman pipeline |
| `BenchmarkRLEHuffCompress` | [fseu16_test.go:217](../fseu16_test.go#L217) | serial | RLE+Huffman without delta |
| `BenchmarkDelta` | [fseu16_test.go:243](../fseu16_test.go#L243) | serial | Delta alone |
| `BenchmarkRLECompress` | [fseu16_test.go:259](../fseu16_test.go#L259) | serial | RLE alone |
| `BenchmarkDeltaZZRLEFSECompress` | [fseu16_test.go:278](../fseu16_test.go#L278) | serial | Delta+ZigZag+RLE+FSE pipeline |
| `BenchmarkRLEFSECompress` | [fseu16_test.go:299](../fseu16_test.go#L299) | serial | RLE+FSE without delta |
| `BenchmarkDeltaZZFSECompress` | [fseu16_test.go:322](../fseu16_test.go#L322) | serial | Delta+ZigZag+FSE (no RLE) |
| `BenchmarkFSECompress` | [fseu16_test.go:344](../fseu16_test.go#L344) | serial | FSE-only on raw pixels |
| `BenchmarkHuffCompress` | [fseu16_test.go:361](../fseu16_test.go#L361) | serial | Huffman-only on raw pixels |
| `BenchmarkDeltaRLEFSEEncodeSpeed` | [fseu16_test.go:1219](../fseu16_test.go#L1219) | serial | Encode-side pipeline throughput |
| `BenchmarkFSETableMemory` | [fseu16_test.go:1245](../fseu16_test.go#L1245) | serial | symbolTT/decTable memory footprint sweep |
| `BenchmarkGradDeltaRLEFSECompress` | [deltagradcompressu16_test.go:97](../deltagradcompressu16_test.go#L97) | serial | Gradient predictor variant of Delta+RLE+FSE |

---

## 6. Wavelet (5/3 integer wavelet pipelines)

All wavelet decompression benchmarks are now serial (single-thread) — see
[the recent fix](../waveletu16_test.go) that replaced parallel goroutines with
a serial loop. In v9 the wavelet variant contributes only its compression
*ratio* to a paper table — the Wavelet column in `tab:ratios` — so this keeps
the throughput measurement honest even though it is no longer a paper column.

| Benchmark | File | Parallelism | Pipeline |
|---|---|---|---|
| `BenchmarkWaveletFSECompress` | [waveletu16_test.go:165](../waveletu16_test.go#L165) | serial | Wavelet (1-level) + FSE, no RLE |
| `BenchmarkWaveletRLEFSECompress` | [waveletu16_test.go:305](../waveletu16_test.go#L305) | serial | Wavelet (1-level) + RLE + FSE |
| `BenchmarkWaveletV2RLEFSECompress` | [waveletu16_test.go:421](../waveletu16_test.go#L421) | serial | Wavelet V2 (5-level, Mallat layout) + RLE + FSE — scalar |
| `BenchmarkWaveletV2SIMDRLEFSECompress` | [waveletu16_test.go:446](../waveletu16_test.go#L446) | serial | Same as V2 but with blocked-column + AVX2 (AMD64) / scalar-blocked (ARM64) kernels — **this is the bench used for the paper's Wav+SIMD column** |

Compressed streams of the SIMD and scalar V2 pipelines are bit-identical;
only the transform kernel differs.

```bash
# Scalar vs SIMD V2 side-by-side
go test -benchmem -run=^$ -benchtime=10x \
  -bench '^(BenchmarkWaveletV2RLEFSECompress|BenchmarkWaveletV2SIMDRLEFSECompress)$' .
```

---

## 7. PICS (parallel single-image, strip-based)

PICS splits a single image into horizontal strips and decompresses them in
parallel. The PICS strip benchmarks run a serial benchmark loop wrapping
internally-parallel code, so `MB/s` is the wall-clock throughput of one
image decoded with N threads.

| Benchmark | File | What it measures |
|---|---|---|
| `BenchmarkParallelStripsCompress` | [parallelstrips_test.go:149](../parallelstrips_test.go#L149) | Compress at strips ∈ {1,2,4,8} on CR image |
| `BenchmarkParallelStripsDecompress` | [parallelstrips_test.go:169](../parallelstrips_test.go#L169) | Decompress at strips ∈ {1,2,4,8} on CR image |
| `BenchmarkPICSVsAllCodecs` | [parallelstrips_test.go:195](../parallelstrips_test.go#L195) | PICS-1/2/4/8 vs MIC-Go and MIC-4state across the full corpus (no CGO) |
| `BenchmarkParallelStripsAdaptive` | [parallelstripsadaptive_test.go:79](../parallelstripsadaptive_test.go#L79) | PICA (adaptive: avg vs grad predictor per strip) at strips ∈ {1,2,4,8} on MR image |

The CGO `PICS-C-N` numbers in `BenchmarkAllCodecs` are the variants used for
the paper.

---

## 8. RGB / YCoCg-R benchmarks

For single-frame RGB images (US, VL). Pipeline is YCoCg-R color transform →
Delta+RLE+FSE per plane. All in [rgbbench_test.go](../rgbbench_test.go).

| Benchmark | Parallelism | Pipeline |
|---|---|---|
| `BenchmarkRGBDeltaRLEHuffCompress` | parallel | YCoCg-R + Delta+RLE+Huffman per plane |
| `BenchmarkRGBDeltaRLEFSECompress` | parallel | YCoCg-R + Delta+RLE+FSE per plane (production path) |
| `BenchmarkRGBDeltaZZRLEHuffCompress` | serial | YCoCg-R + Delta+ZZ+RLE+Huffman per plane |
| `BenchmarkRGBRLEHuffCompress` | serial | YCoCg-R + RLE+Huffman per plane |
| `BenchmarkRGBDeltaZZRLEFSECompress` | serial | YCoCg-R + Delta+ZZ+RLE+FSE per plane |
| `BenchmarkRGBRLEFSECompress` | serial | YCoCg-R + RLE+FSE per plane |
| `BenchmarkRGBDeltaZZFSECompress` | serial | YCoCg-R + Delta+ZZ+FSE per plane |
| `BenchmarkRGBFSECompress` | serial | YCoCg-R + FSE per plane |

---

## 9. WSI (whole slide imaging, MIC3 format)

For tiled RGB pathology images. All in [wsi_test.go](../wsi_test.go), all
serial.

| Benchmark | What it measures |
|---|---|
| `BenchmarkWSITileCompressTissue` | Compress one 256×256 H&E-stained tile |
| `BenchmarkWSITileDecompressTissue` | Decompress one 256×256 H&E-stained tile |
| `BenchmarkWSITileCompressWhite` | Compress one all-white 256×256 tile (constant-plane fast path) |
| `BenchmarkWSICompress1024` | Compress 1024×1024 image (16 tiles), single worker |
| `BenchmarkWSICompressParallel1024` | Same image, all cores (`Workers: 0`) — measures intra-image parallelism |

---

## 10. Complete inventory

Sorted by file. P = parallel goroutines per iteration; S = serial loop.

| File | Benchmark | P/S |
|---|---|---|
| comparison_test.go | `BenchmarkDeltaZstdDecompress` | S |
| comparison_test.go | `BenchmarkMEDPredictor` | S |
| deltagradcompressu16_test.go | `BenchmarkGradDeltaRLEFSECompress` | S |
| fse2state_test.go | `BenchmarkFSEDecompress` | **P** |
| fse2state_test.go | `BenchmarkFSECompressCompare` | S |
| fse2state_test.go | `BenchmarkDeltaRLEFSEDecompress` | **P** |
| fse2state_test.go | `BenchmarkFSE2StateSummary` | **P** |
| fse4state_test.go | `BenchmarkFSEDecompress4State` | **P** |
| fseu16_test.go | `BenchmarkDeltaRLEHuffCompress` | **P** |
| fseu16_test.go | `BenchmarkDeltaRLEHuffCompress2` | S |
| fseu16_test.go | `BenchmarkDeltaRLEFSECompress` | **P** |
| fseu16_test.go | `BenchmarkDeltaZZRLEHuffCompress` | S |
| fseu16_test.go | `BenchmarkRLEHuffCompress` | S |
| fseu16_test.go | `BenchmarkDelta` | S |
| fseu16_test.go | `BenchmarkRLECompress` | S |
| fseu16_test.go | `BenchmarkDeltaZZRLEFSECompress` | S |
| fseu16_test.go | `BenchmarkRLEFSECompress` | S |
| fseu16_test.go | `BenchmarkDeltaZZFSECompress` | S |
| fseu16_test.go | `BenchmarkFSECompress` | S |
| fseu16_test.go | `BenchmarkHuffCompress` | S |
| fseu16_test.go | `BenchmarkDeltaRLEFSEEncodeSpeed` | S |
| fseu16_test.go | `BenchmarkFSETableMemory` | S |
| htj2k_comparison_test.go | `BenchmarkMICvsHTJ2K` | S |
| ojph/htj2k_fair_comparison_test.go | `BenchmarkHTJ2KFairDecomp` | S |
| ojph/jpegls_comparison_test.go | `BenchmarkJPEGLSDecomp` | S |
| ojph/jxl_comparison_test.go | `BenchmarkJXLDecomp` | S |
| ojph/jxl_comparison_test.go | `BenchmarkJXLEncode` | S |
| ojph/mic_c_test.go | `BenchmarkAllCodecs` | S |
| ojph/mic_c_test.go | `BenchmarkAllCodecsEncode` | S |
| ojph/mic_c_test.go | `BenchmarkMICFullCPipelineVsHTJ2K` | S |
| parallelstrips_test.go | `BenchmarkParallelStripsCompress` | S |
| parallelstrips_test.go | `BenchmarkParallelStripsDecompress` | S |
| parallelstrips_test.go | `BenchmarkPICSVsAllCodecs` | S |
| parallelstripsadaptive_test.go | `BenchmarkParallelStripsAdaptive` | S |
| rans8state_test.go | `BenchmarkRANSDecompress8State` | **P** |
| rgbbench_test.go | `BenchmarkRGBDeltaRLEHuffCompress` | **P** |
| rgbbench_test.go | `BenchmarkRGBDeltaRLEFSECompress` | **P** |
| rgbbench_test.go | `BenchmarkRGBDeltaZZRLEHuffCompress` | S |
| rgbbench_test.go | `BenchmarkRGBRLEHuffCompress` | S |
| rgbbench_test.go | `BenchmarkRGBDeltaZZRLEFSECompress` | S |
| rgbbench_test.go | `BenchmarkRGBRLEFSECompress` | S |
| rgbbench_test.go | `BenchmarkRGBDeltaZZFSECompress` | S |
| rgbbench_test.go | `BenchmarkRGBFSECompress` | S |
| waveletu16_test.go | `BenchmarkWaveletFSECompress` | S |
| waveletu16_test.go | `BenchmarkWaveletRLEFSECompress` | S |
| waveletu16_test.go | `BenchmarkWaveletV2RLEFSECompress` | S |
| waveletu16_test.go | `BenchmarkWaveletV2SIMDRLEFSECompress` | S |
| wsi_test.go | `BenchmarkWSITileCompressTissue` | S |
| wsi_test.go | `BenchmarkWSITileDecompressTissue` | S |
| wsi_test.go | `BenchmarkWSITileCompressWhite` | S |
| wsi_test.go | `BenchmarkWSICompress1024` | S |
| wsi_test.go | `BenchmarkWSICompressParallel1024` | S |

---

## 11. Historical hardware results

The numbers in this section come from the parallel `BenchmarkDeltaRLEFSECompress`,
`BenchmarkFSE2StateSummary`, and (the now-fixed) parallel wavelet benches.
They report **aggregate multi-core throughput**, not single-thread MB/s — so
they are higher than the equivalent rows in the paper's `tab:decomp`.

> These are kept for historical reference. To reproduce current numbers,
> run `./run-paper-benchmarks.sh` and look at `paper-tables.txt` in the
> results directory; those are the apples-to-apples numbers that go into
> the paper.

### `BenchmarkDeltaRLEFSECompress` — parallel pipeline aggregate

**AWS c7g.metal — ARM64 | 64 cores**

| Modality | FPS | Aggregate Decomp |
|----------|-----|------------------|
| MR (256×256) | 17 411 | 2 282 MB/s |
| CT (512×512) | 8 455 | 4 433 MB/s |
| CR (2140×1760) | 1 132 | 8 527 MB/s |
| XR (2048×2577) | 892 | 9 411 MB/s |
| MG1 (2457×1996) | 1 671 | **16 387 MB/s** |
| MG2 (2457×1996) | 1 634 | 16 023 MB/s |
| MG3 (4774×3064) | 281 | 8 044 MB/s |
| MG4 (4096×3328) | 558 | 15 213 MB/s |

**AWS c7i.8xlarge — AMD64 | 32 cores (Intel Xeon Platinum 8488C)**

| Modality | FPS | Aggregate Decomp |
|----------|-----|------------------|
| MR | 8 714 | 1 142 MB/s |
| CT | 2 303 | 1 208 MB/s |
| CR | 421 | 3 172 MB/s |
| XR | 310 | 3 269 MB/s |
| MG1 | 532 | 5 220 MB/s |
| MG2 | 522 | 5 124 MB/s |
| MG3 | 121 | 3 468 MB/s |
| MG4 | 182 | 4 964 MB/s |

---

## 12. Browser PACS web-viewer benchmark

Distinct from the Go benchmarks above: this one runs **in a real browser** and
measures the end-to-end experience of a browser-based PACS viewer — network
transfer of the compressed bytes **plus live in-browser decode** — instead of
decode alone. It lives in [`web/`](../web/), shares one model
([`web/pacs-model.mjs`](../web/pacs-model.mjs)) between a Node console report and
an interactive dashboard, and verifies every live decode bit-exact against a
raw-pixel checksum manifest.

### Runners

| Runner | File | What it does |
|---|---|---|
| Node console report | [web/bench-pacs-viewer.mjs](../web/bench-pacs-viewer.mjs) | MIC + PICS live (pure-JS, `worker_threads`); HTJ2K/JPEG-LS/JPEG-XL informational. 8 network profiles + study-level sim. |
| Interactive dashboard | [web/pacs-dashboard.html](../web/pacs-dashboard.html) | **12 codecs decoded live in-browser**, per-image tables, study sim, charts, optional pixel-correctness pass. Must be served with COOP/COEP (`web/serve.py`). |
| Headless CI runner | [web/tests/pacs-bench.spec.mjs](../web/tests/pacs-bench.spec.mjs) | Playwright drives the dashboard in headless Chromium, asserts every live codec pixel-verifies, writes JSON to `web/results/`. |

### Codec variants (dashboard, live in-browser)

Every MIC/PICS row below decodes **identical `.mic` bytes**, so the differences
are pure decoder-implementation cost. Reference codecs decode their own
`.jph`/`.jls` files (generated + native-roundtrip-verified by
[`cmd/mic-refgen`](../cmd/mic-refgen)).

| Variant | Implementation | Loaded from |
|---|---|---|
| MIC-1/4/8-state | pure JS (`mic-decoder.js`) | — |
| MIC-WASM (Go) | Go codec → WASM (`cmd/mic-wasm`) | `web/mic-decoder.wasm` (~2.9 MB) |
| MIC-C-WASM (4/8-state) | pure C (`ojph/mic_decompress_c.c`) → WASM | `web/vendor/mic-c/` (~20 KB) |
| MIC-PICS (4/8 strips) | pure JS, real Web Worker pool + SAB | `mic-decoder-parallel.js` |
| MIC-C-WASM-PICS (8 strips) | pure C pthreads (`ojph/mic_parallel.c`) → WASM | `web/vendor/mic-pics/` (~30 KB) |
| HTJ2K | OpenJPH WASM (vendored) | `web/vendor/openjph/` |
| JPEG-LS | CharLS WASM (vendored) | `web/vendor/charls/` |
| JPEG-XL | informational (no lossless-16-bit browser decoder) | `refcodecs-manifest.json` |

### Representative decode times (CR, 1760×2140, 7.18 MB raw; Apple M2 Max, headless Chromium)

Same 4-state stream unless noted; all pixel-verified bit-exact.

| Variant | Decode | Notes |
|---|---|---|
| MIC-4state (JS) | ~140 ms | pure-JS baseline |
| MIC-WASM (Go) | ~330 ms | *slower* than JS — Go runtime + GC overhead |
| MIC-C-WASM (4-state) | ~17 ms | ~8× faster than JS, from a 20 KB binary |
| MIC-PICS-8 (JS workers) | ~24 ms | 8 Web Workers, SharedArrayBuffer |
| **MIC-C-WASM-PICS-8** | **~4 ms** | C pthreads → WASM; fastest path, ~6× the JS pool |
| HTJ2K (OpenJPH WASM) | ~125 ms | reference codec, live |
| JPEG-LS (CharLS WASM) | ~70 ms | reference codec, live |

Headline: on fast networks decode dominates time-to-display (codec/threading
choice matters most); on slow networks (broadband, cellular, 3G, satellite)
transfer dominates by many multiples, so compression ratio matters more than
decode speed. Full how-to (build steps for the Go/C WASM variants, Emscripten
prereqs) is in the **[Web Decoder README](../web/README.md#pacs-web-viewer-benchmark)**.

### Cine / multi-frame datasets

Both runners include a cine section over seven multi-frame studies (five
fetched via `testdata/multiframe/fetch-cine-sources.sh` and two reused from
the whole-image demo corpus). Each is a real, uncompressed (native) multi-frame
DICOM whose **every frame is emitted as an independent single-frame image**
(`<id>_f<NNN>`) by `mic-compress -testdata` and `mic-refgen`. Because each frame
is a normal single-frame file, the *entire* codec matrix (MIC 1/4/8-state, PICS,
HTJ2K, JPEG-LS, JPEG-XL) runs per frame exactly as in the single-frame tables;
the benchmark fetches and decodes each frame independently and aggregates to a
full cine-loop decode time and frames/s — the way a PACS viewer streams a cine
loop.

| Dataset | Modality | Frames | Dims | Depth | Source |
|---|---|---|---|---|---|
| `CINE_MRCARD` | Cardiac cine MR | 16 | 256×256 | 8-bit | `MR-MONO2-8-16x-heart` |
| `CINE_XA` | XA coronary angiography | 12 | 512×512 | 8-bit | `XA-MONO2-8-12x-catheter` (transcoded to native) |
| `CINE_NM` | Nuclear-medicine gated heart | 13 | 64×64 | 16-bit | `NM-MONO2-16-13x-heart` |
| `CINE_EMR` | Enhanced / volumetric MR | 10 | 64×64 | 16-bit | `emri_small` |
| `CINE_ECT` | Enhanced CT | 2 | 512×512 | 16-bit | `eCT_Supplemental` |
| `CINE_TOMO` | Breast tomosynthesis DBT | 16 (capped) | 1890×2457 | 10-bit | MG_TOMO whole-image source (native: 69f) |
| `CINE_CTMULTI` | CT axial series | 16 (capped) | 512×512 | 12-bit | CT_MULTI whole-image source (native: 203f) |

The first five sources are fetched and prepared by
[`testdata/multiframe/fetch-cine-sources.sh`](../testdata/multiframe/fetch-cine-sources.sh)
(public-domain Barre / pydicom-data samples; the JPEG-Lossless XA is transcoded
to uncompressed once via the project `.venv`). The last two reuse existing
testdata sources already present for the whole-image demo tables, capped to 16
frames to keep the benchmark wall-clock time bounded. Because the 8-bit cardiac/XA/NM
frames trip CharLS's 1-byte sample packing and libjxl's UINT16-at-8-bit path,
`mic-refgen` floors the *reference-codec* declared bit depth at 9 (still bit-exact
lossless). Every dataset now ships **both** 4- and 8-strip PICS variants (see
`cineDatasetPICSStrips` in `cmd/mic-compress/main.go`), so the table below
shows whichever strip count is faster per dataset. Representative cine
throughput (Apple M2 Max, MIC decode live):

| Dataset | MIC-4state | MIC-PICS (best of 4/8 strips) |
|---|---|---|
| Cardiac cine MR (16f) | ~444 fps | ~1070 fps (8 strips) |
| XA angiography (12f) | ~141 fps | ~396 fps (8 strips) |
| NM gated heart (13f, 64²) | ~9214 fps | ~3974 fps (4 strips)* |
| Enhanced MR (10f, 64²) | ~5355 fps | ~3762 fps (4 strips)* |
| Enhanced CT (2f, 512²) | ~220 fps | ~571 fps (8 strips) |
| Breast tomosynthesis (16f, 1890×2457) | ~17 fps | ~51 fps (8 strips) |
| CT axial series (16f, 512²) | ~88 fps | ~229 fps (8 strips) |

\* At these tiny per-frame sizes (64×64, ~8 KB raw) worker dispatch/collection
overhead in the JS PICS pool exceeds the decode work itself, so single-threaded
MIC beats PICS — parallel strips only pay off once per-frame decode time clears
the worker overhead floor (visible starting around the Enhanced CT / cardiac
cine sizes).

`CINE_TOMO`'s per-frame raw size (~9.3 MB, comparable to a full CR/MG image) makes
it the heaviest cine dataset by far — single-threaded MIC decode throughput drops
to ~17 fps accordingly, while 8-strip PICS recovers ~3× to ~51 fps. `CINE_CTMULTI`
frames are 512×512 like `CINE_XA` but 12-bit instead of 8-bit and unpadded, giving
a lower ~2.4x compression ratio than the smaller cine datasets.

Full run: `cd web && node bench-pacs-viewer.mjs` (prints per-image tables, then
a `########## Cine / multi-frame ##########` section with one table per dataset
above — compressed size, decode loop time, frames/s, and full-loop time to
display across all eight simulated network profiles). That Node run only
live-measures MIC/PICS; HTJ2K/JPEG-LS/JPEG-XL are informational native-C
numbers there since Node has no WASM DOM environment — but real per-frame
reference-codec files (and therefore real compressed sizes, not corpus-average
estimates) now exist for all seven datasets, including `CINE_TOMO`/`CINE_CTMULTI`
(see "What closed the three gaps" below).

### Full codec-matrix cine benchmark (browser, all WASM variants live)

The Node run above can't exercise the browser-only decoders (MIC-WASM Go,
MIC-C-WASM, MIC-C-WASM-PICS) and only has informational numbers for the
reference codecs. Driving the actual dashboard in headless Chromium
(`pacs-dashboard.html?headless=1&images=cine&verify=1`, the same harness
[`tests/pacs-bench.spec.mjs`](../web/tests/pacs-bench.spec.mjs) uses for CI,
just pointed at every cine dataset instead of the quick subset) live-decodes
every codec that has a browser implementation — HTJ2K and JPEG-LS included —
and pixel-verifies every frame against the manifest checksums. Below is one
table per dataset (Apple M2 Max, headless Chromium, 8 iterations + 2 warm-up
per codec/frame; every row is pixel-verified bit-exact except JPEG-XL, which
has no lossless-16-bit browser decoder and stays informational). All twelve
codec rows have real, live-measured data for all seven datasets — closing
three gaps that an earlier version of this table left open (see below).

**Cardiac cine MR (16f, 256×256, 8-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 387 KB | 300 |
| MIC-4state | 387 KB | 300 |
| MIC-8state | 387 KB | 286 |
| MIC-WASM (Go) | 387 KB | 153 |
| MIC-C-WASM (4-state) | 387 KB | 1077 |
| MIC-C-WASM (8-state) | 387 KB | 954 |
| MIC-PICS (JS, 4 strips) | 393 KB | 445 |
| MIC-PICS (JS, 8 strips) | 400 KB | 605 |
| MIC-C-WASM-PICS (8 strips) | 400 KB | 1123 |
| HTJ2K (OpenJPH WASM) | 435 KB | 287 |
| JPEG-LS (CharLS WASM) | 367 KB | 421 |
| JPEG-XL (informational) | 165 KB | 391 |

**XA coronary angiography (12f, 512×512, 8-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 749 KB | 136 |
| MIC-4state | 749 KB | 140 |
| MIC-8state | 749 KB | 140 |
| MIC-WASM (Go) | 749 KB | 46 |
| MIC-C-WASM (4-state) | 749 KB | 430 |
| MIC-C-WASM (8-state) | 749 KB | 396 |
| MIC-PICS (JS, 4 strips) | 750 KB | 252 |
| MIC-PICS (JS, 8 strips) | 754 KB | 347 |
| MIC-C-WASM-PICS (8 strips) | 754 KB | 584 |
| HTJ2K (OpenJPH WASM) | 701 KB | 129 |
| JPEG-LS (CharLS WASM) | 619 KB | 218 |
| JPEG-XL (informational) | 571 KB | 98 |

**NM gated heart (13f, 64×64, 16-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 17 KB | 1045 |
| MIC-4state | 17 KB | 937 |
| MIC-8state | 17 KB | 925 |
| MIC-WASM (Go) | 17 KB | 502 |
| MIC-C-WASM (4-state) | 17 KB | 8497 |
| MIC-C-WASM (8-state) | 17 KB | 8442 |
| MIC-PICS (JS, 4 strips) | 19 KB | 1055 |
| MIC-PICS (JS, 8 strips) | 22 KB | 785 |
| MIC-C-WASM-PICS (8 strips) | 22 KB | 1791 |
| HTJ2K (OpenJPH WASM) | 16 KB | 779 |
| JPEG-LS (CharLS WASM) | 12 KB | 2951 |
| JPEG-XL (informational) | 10 KB | 6256 |

**Enhanced/volumetric MR (10f, 64×64, 16-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 37 KB | 672 |
| MIC-4state | 37 KB | 607 |
| MIC-8state | 37 KB | 847 |
| MIC-WASM (Go) | 37 KB | 513 |
| MIC-C-WASM (4-state) | 37 KB | 6849 |
| MIC-C-WASM (8-state) | 37 KB | 7547 |
| MIC-PICS (JS, 4 strips) | 42 KB | 903 |
| MIC-PICS (JS, 8 strips) | 47 KB | 549 |
| MIC-C-WASM-PICS (8 strips) | 47 KB | 1625 |
| HTJ2K (OpenJPH WASM) | 39 KB | 705 |
| JPEG-LS (CharLS WASM) | 36 KB | 1660 |
| JPEG-XL (informational) | 34 KB | 6256 |

**Enhanced CT (2f, 512×512, 16-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 104 KB | 174 |
| MIC-4state | 104 KB | 197 |
| MIC-8state | 104 KB | 181 |
| MIC-WASM (Go) | 104 KB | 46 |
| MIC-C-WASM (4-state) | 104 KB | 658 |
| MIC-C-WASM (8-state) | 104 KB | 548 |
| MIC-PICS (JS, 4 strips) | 107 KB | 393 |
| MIC-PICS (JS, 8 strips) | 237 KB¹ | 442 |
| MIC-C-WASM-PICS (8 strips) | 237 KB¹ | 1173 |
| HTJ2K (OpenJPH WASM) | 126 KB | 127 |
| JPEG-LS (CharLS WASM) | 78 KB | 325 |
| JPEG-XL (informational) | 54 KB | 98 |

**Breast tomosynthesis (16f, 1890×2457, 10-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 10792 KB | 19 |
| MIC-4state | 10794 KB | 18 |
| MIC-8state | 10795 KB | 18 |
| MIC-WASM (Go) | 10794 KB | 3 |
| MIC-C-WASM (4-state) | 10794 KB | 66 |
| MIC-C-WASM (8-state) | 10795 KB | 67 |
| MIC-PICS (JS, 4 strips) | 10740 KB | 47 |
| MIC-PICS (JS, 8 strips) | 10681 KB | 90 |
| MIC-C-WASM-PICS (8 strips) | 10681 KB | 252 |
| HTJ2K (OpenJPH WASM) | 11282 KB | 9 |
| JPEG-LS (CharLS WASM) | 10192 KB | 36 |
| JPEG-XL (informational) | 8813 KB | 6 |

**CT axial series (16f, 512×512, 12-bit)**

| Codec | Comp. (total) | Frames/s |
|---|---|---|
| MIC-1state | 3365 KB | 102 |
| MIC-4state | 3366 KB | 101 |
| MIC-8state | 3367 KB | 97 |
| MIC-WASM (Go) | 3366 KB | 44 |
| MIC-C-WASM (4-state) | 3366 KB | 554 |
| MIC-C-WASM (8-state) | 3367 KB | 483 |
| MIC-PICS (JS, 4 strips) | 3304 KB | 254 |
| MIC-PICS (JS, 8 strips) | 3339 KB | 369 |
| MIC-C-WASM-PICS (8 strips) | 3339 KB | 676 |
| HTJ2K (OpenJPH WASM) | 3053 KB | 111 |
| JPEG-LS (CharLS WASM) | 2929 KB | 176 |
| JPEG-XL (informational) | 2691 KB | 98 |

¹ Larger than the single-threaded MIC variants because one of the 8 strips
(64 rows of high-entropy volumetric CT data) is incompressible and falls back
to raw storage — a real, expected ratio/parallelism tradeoff of the PICS
format, not a display artifact.

What closed the three gaps from the previous version of this table:

- **`mic-c-wasm-pics` was 8-strip-only, so datasets shipped with only a
  4-strip PICS file had nothing for it to decode.** `cmd/mic-compress`'s
  `cineDatasets` used to declare one `picsStrips` count per dataset (4 or 8);
  now every cine dataset always gets **both** 4- and 8-strip PICS variants
  (`cineDatasetPICSStrips = []int{4, 8}` in `cmd/mic-compress/main.go`), so
  MIC-PICS (JS, 4 strips), MIC-PICS (JS, 8 strips), and MIC-C-WASM-PICS
  (8 strips only) all have a file to decode for every dataset.
- **`mic-refgen` hadn't been re-run since `CINE_TOMO`/`CINE_CTMULTI` were
  added**, even though its cine loop already covered them — the reference
  manifest was just stale. Re-running `go run -tags cgo_ojph ./cmd/mic-refgen`
  generated real `.jph`/`.jls`/`.jxl` files (native-roundtrip-verified) for
  both, giving HTJ2K/JPEG-LS live browser numbers and JPEG-XL a real
  per-frame compressed size instead of the corpus-average fallback.
- **Generating the missing 8-strip PICS files for the four small (64–512px)
  cine datasets surfaced a real, previously-latent decoder bug**: an
  incompressible strip is stored raw with the high bit of its length field
  set (`picsRawFlag` in `parallelstrips.go`), but the JS decoders
  (`mic-decoder.js`'s `parsePICSHeader`/`decodePICS`, the Web Worker in
  `mic-worker.js`, and the parallel orchestrator in `mic-decoder-parallel.js`)
  never masked the flag or checked it — they always ran a raw-fallback strip
  through the FSE decompressor, corrupting the read length and throwing
  `Invalid typed array length`. This only manifested when a fine-grained
  strip split (8 strips of 64 rows on `CINE_ECT`) hit a high-entropy region;
  large single-frame images never had. Fixed by masking `picsRawFlag` out of
  the parsed length, exposing an `isRaw` flag per strip, and having both the
  main-thread and worker decode paths read raw strips as plain little-endian
  uint16 pixels instead of FSE-decoding them (mirroring the already-correct
  Go and C decoders). The Node harness's `bench-worker.mjs` /
  `NodeWorkerPool` had the identical bug and got the identical fix.

Reproduce: `cd web && npx playwright test` runs the CI-scoped quick cine subset
(`CINE_MRCARD` only); the tables above cover all seven datasets by pointing
the same dashboard at `?images=cine` directly (see
[`tests/pacs-bench.spec.mjs`](../web/tests/pacs-bench.spec.mjs) for the URL
pattern and assertions to mirror). Regenerating requires `mic-compress
-testdata` and `mic-refgen` to have been (re-)run first — see build
prerequisites below.

### Build prerequisites for the WASM variants

```bash
bash testdata/multiframe/fetch-cine-sources.sh # cine source DICOMs (one-time; needs .venv)
go run ./cmd/mic-compress -testdata            # MIC .mic + manifest.json (no cgo)
go run -tags cgo_ojph ./cmd/mic-refgen         # HTJ2K/JPEG-LS/JPEG-XL reference files
cd web && npm install && bash scripts/vendor-wasm.sh   # vendor OpenJPH + CharLS WASM
GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/  # MIC-WASM (Go)
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js
bash web/wasm-c/build.sh                        # MIC-C-WASM (needs emscripten)
bash web/wasm-c/build-pics.sh                   # MIC-C-WASM-PICS (C pthreads → WASM)
cd web && npx playwright test                   # headless run + pixel-verify
```

**AWS c7g.8xlarge — ARM64 | 32 cores**

| Modality | FPS | Aggregate Decomp |
|----------|-----|------------------|
| MR | 11 627 | 1 524 MB/s |
| CT | 4 170 | 2 186 MB/s |
| CR | 570 | 4 290 MB/s |
| XR | 432 | 4 562 MB/s |
| MG1 | 908 | 8 901 MB/s |
| MG2 | 803 | 7 879 MB/s |
| MG3 | 156 | 4 455 MB/s |
| MG4 | 262 | 7 132 MB/s |

**Mac Studio — Apple M2 Max | ARM64 | 12 cores**

| Modality | FPS | Aggregate Decomp |
|----------|-----|------------------|
| MR | 8 044 | 1 054 MB/s |
| CT | 2 137 | 1 121 MB/s |
| CR | 277 | 2 089 MB/s |
| XR | 199 | 2 101 MB/s |
| MG1 | 374 | 3 666 MB/s |
| MG2 | 373 | 3 659 MB/s |
| MG3 | 78 | 2 239 MB/s |
| MG4 | 117 | 3 188 MB/s |
