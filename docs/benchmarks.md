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

This is what the FSE entropy-coder microbenchmarks (Table 6) and the legacy
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

All numbers in [paper/mic-paper-v6-ieee-tmi.tex](../paper/mic-paper-v6-ieee-tmi.tex)
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

| Paper table | Source benchmark | File produced | Parallelism |
|---|---|---|---|
| Table 1 (`tab:ratios`) — compression ratios | `BenchmarkAllCodecs` (ratios) + `BenchmarkDeltaZstdDecompress` + `BenchmarkWaveletV2SIMDRLEFSECompress` | `01-…txt`, `05-…txt`, `06-…txt` | serial |
| Table 2 (`tab:enc-amd64`) — AMD64 encoding | `BenchmarkAllCodecsEncode` | `02-…txt` | serial |
| Table 3 (`tab:enc-arm64-full`) — ARM64 encoding | `BenchmarkAllCodecsEncode` | `02-…txt` | serial |
| Table 4 (`tab:decomp-arm`) — ARM64 decoding | `BenchmarkAllCodecs` + `BenchmarkWaveletV2SIMDRLEFSECompress` | `01-…txt`, `06-…txt` | serial |
| Table 5 (`tab:decomp-amd64`) — AMD64 decoding | `BenchmarkAllCodecs` + `BenchmarkWaveletV2SIMDRLEFSECompress` | `01-…txt`, `06-…txt` | serial |
| Table 6 (`tab:fse-combined`) — FSE 1/4-state | `BenchmarkFSEDecompress` + `BenchmarkFSEDecompress4State` | `03-…txt`, `04-…txt` | **parallel** |

Table 6 reports **aggregate parallel FSE-only throughput** by design — that
table is a microbenchmark of the entropy coder running flat-out across all
cores, isolated from the surrounding pipeline. Tables 4/5 report
**single-thread end-to-end** throughput for the full Delta+RLE+FSE pipeline.
The numbers are not directly comparable.

The PICS columns in Tables 4/5 (`PICS-C-2/4/8`) are *also* parallel, but the
parallelism is internal to the codec (pthread-based strip decoder), driven by
a serial benchmark loop. That correctly measures wall-clock decode time of one
image using N threads — apples to apples with the other single-image columns.

---

## 3. Codec comparison benchmarks

### `BenchmarkAllCodecs` ([ojph/mic_c_test.go:371](../ojph/mic_c_test.go#L371))

Decompression throughput (serial) across every codec variant on the 21-image
paper corpus. Reports MB/s and compression ratio for each (image, codec)
combination. Requires `-tags cgo_ojph`.

Variants exercised:

- `MIC-Go` — pure-Go 2-state FSE pipeline (Delta+RLE+FSE)
- `MIC-4state` — pure-Go 4-state FSE pipeline
- `MIC-4state-C` — C 4-state decoder via CGO
- `MIC-4state-SIMD` — C 4-state decoder with platform SIMD (BMI2 on AMD64, scalar on ARM64)
- `MIC-C` — C 2-state decoder
- `MIC-SIMD` — C 2-state decoder with SIMD
- `HTJ2K` — OpenJPH in-process
- `JPEGLS` — CharLS in-process
- `PICS-N` for N ∈ {2, 4, 8} — Go strip decoder with N goroutines
- `PICS-C-N` for N ∈ {2, 4, 8} — C strip decoder with N pthreads + per-strip SIMD

```bash
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x \
  -bench '^BenchmarkAllCodecs$' ./ojph/
```

### `BenchmarkAllCodecsEncode` ([ojph/mic_c_test.go:528](../ojph/mic_c_test.go#L528))

Encoding-side counterpart of the above. Same variant list, plus `Wavelet+SIMD`
(serial wavelet compress). Powers Tables 2/3.

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

### `BenchmarkMICvsHTJ2K` ([htj2k_comparison_test.go:329](../htj2k_comparison_test.go#L329))

Older standalone MIC-vs-HTJ2K bench, predates `BenchmarkAllCodecs`. Kept for
historical comparison. Serial.

### `BenchmarkMICFullCPipelineVsHTJ2K` ([ojph/mic_c_test.go:695](../ojph/mic_c_test.go#L695))

End-to-end C pipeline (delta + RLE + FSE all in C) vs HTJ2K. Serial.

### `BenchmarkDeltaZstdDecompress` ([comparison_test.go:86](../comparison_test.go#L86))

Δ+Zstd-19 baseline column for Table 1. Note: Zstd is invoked via the CLI
(subprocess), so timings include process-launch overhead — this benchmark is
only useful for the *ratio* column (Table 1), not for throughput. Serial.

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

1-state vs 2-state FSE decompression isolated. Feeds Table 6 (1-state column).

```bash
go test -benchmem -run=^$ -benchtime=10x -bench '^BenchmarkFSEDecompress$' .
```

### `BenchmarkFSEDecompress4State` ([fse4state_test.go:150](../fse4state_test.go#L150))

1-state vs 2-state vs 4-state FSE decompression. Feeds Table 6 (4-state
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
a serial loop so the Wav+SIMD column in Tables 4/5 is comparable to the other
single-thread columns.

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
| `BenchmarkPICSVsAllCodecs` | [parallelstrips_test.go:195](../parallelstrips_test.go#L195) | PICS-1/2/4/8 vs MIC-Go and MIC-4state across all 21 images (no CGO) |
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
they are higher than the equivalent rows in the paper's Tables 4/5.

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
