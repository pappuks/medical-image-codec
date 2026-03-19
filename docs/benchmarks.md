# MIC Benchmark Results

This document contains detailed decompression benchmark results across hardware platforms. All benchmarks measure **decompression throughput** — the primary use case is real-time rendering of compressed DICOM images.

## Benchmark Methodology

All decompression benchmarks (`BenchmarkDeltaRLEFSECompress`, `BenchmarkFSEDecompress`, `BenchmarkDeltaRLEFSEDecompress`, `BenchmarkFSE2StateSummary`) spawn one goroutine per iteration and run all `b.N` goroutines concurrently. The reported MB/s reflects **aggregate multi-core throughput** across all available CPUs, not single-core speed. With `-benchtime=200x` on a 64-core machine, all 200 frames decompress in parallel — matching the real-world use case of concurrent multi-frame rendering. Use `-benchtime=1x` for single-iteration (single-goroutine) measurements.

> **Note:** RAM speed has a larger impact than CPU clock speed. Machines with DDR5 RAM outperform older machines even at lower core counts.

## Running the Benchmarks

```bash
# Full Delta+RLE+FSE pipeline (parallel, 200 concurrent goroutines)
go test -benchmem -run=^$ -benchtime=200x -bench ^BenchmarkDeltaRLEFSECompress$ mic

# Compare single-state vs two-state FSE decompression (isolated)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSEDecompress mic

# Compare single-state vs two-state: full Delta+RLE+FSE pipeline
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkDeltaRLEFSEDecompress mic

# Human-readable speedup table (parallel)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSE2StateSummary -v mic

# Parallel single-image (PICS) benchmarks — 1/2/4/8 strips
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkParallelStrips mic

# Wavelet SIMD vs scalar
go test -benchmem -run=^$ -benchtime=5x \
  -bench "^(BenchmarkWaveletV2RLEFSECompress|BenchmarkWaveletV2SIMDRLEFSECompressU16)$" mic

# All benchmarks
go test -bench=. -benchtime=10x
```

---

## Delta+RLE+FSE Decompression — Multi-Core Throughput

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

---

## Two-State FSE Speedup

`BenchmarkFSE2StateSummary` — full Delta+RLE+FSE pipeline, 200 iterations, Mac Studio (Apple M2 Max, 12 cores):

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

Isolated FSE decompression speedup (Intel Xeon @ 2.80 GHz):

| Image | 1-State | 2-State | Δ |
|-------|---------|---------|---|
| MR 256×256 | 164 MB/s | 207 MB/s | **+26%** |
| MG3 4774×3064 | 243 MB/s | 312 MB/s | **+28%** |
| MG4 4096×3328 | 256 MB/s | 321 MB/s | **+25%** |

---

## Wavelet SIMD vs Scalar

Intel Xeon @ 2.80 GHz, 4 cores, 10 concurrent goroutines (5-level transform, 4-state FSE):

| Modality | Scalar+4FSE (MB/s) | SIMD+4FSE (MB/s) | Speedup | Ratio |
|----------|--------------------|-------------------|:-------:|:-----:|
| MR   256×256   | 150 | 165 | **+10%** | 2.38× |
| CT   512×512   | 152 | 190 | **+25%** | 1.67× |
| CR   2140×1760 | 166 | **210** | **+27%** | 3.81× |
| XR   2048×2577 | 193 | 214 | **+11%** | 1.76× |
| MG1  2457×1996 | 182 | 227 | **+25%** | 8.67× |
| MG2  2457×1996 | 193 | 241 | **+25%** | 8.65× |
| MG3  4774×3064 | 118 | 112 | — | 2.32× |
| MG4  4096×3328 | 144 | **198** | **+38%** | 3.59× |

The CR and MG4 images show the largest gains — their column passes are most cache-bound (wide rows, high L2 miss count). MG3 is memory-bandwidth bound on this configuration.

---

## PICS Parallel Strip Throughput

### Compression Ratio Impact (all modalities)

For CR/XR/MG modalities, strip-local FSE table adaptation at higher strip counts actually **improves** ratio. Only small images (MR, CT) show boundary overhead:

| Image | 1-strip | 4-strip | 8-strip | Δ(1→4) |
|-------|:-------:|:-------:|:-------:|:------:|
| MR (256×256) | 2.35× | 2.28× | 2.21× | −3.0% |
| CT (512×512) | 2.24× | 2.15× | 1.96× | −4.0% |
| CR (1760×2140) | 3.63× | **3.66×** | 3.68× | +0.8% |
| XR (2048×2577) | 1.74× | **1.75×** | 1.76× | +0.6% |
| MG1 (2457×1996) | 8.57× | **8.69×** | 8.77× | +1.4% |
| MG3 (4774×3064) | 2.29× | **2.36×** | 2.39× | +3.1% |
| MG4 (4096×3328) | 3.47× | **3.59×** | 3.62× | +3.5% |

### Throughput Scaling (CR 1760×2140, Intel Xeon @ 2.10 GHz, 4 cores)

| Strips | Compress (MB/s) | Speedup | Decompress (MB/s) | Speedup |
|--------|:--------------:|:-------:|:----------------:|:-------:|
| 1 | 133 | 1.0× | 186 | 1.0× |
| 2 | 219 | **1.7×** | 346 | **1.9×** |
| 4 | 401 | **3.0×** | 583 | **3.1×** |
| 8 | 479 | **3.6×** | 540 | 2.9× |

### PICS-2, PICS-4 and PICS-8 vs Single-Thread — All Modalities (MB/s)

Apple M2 Max (ARM64), `BenchmarkAllCodecs` (`-tags cgo_ojph`, `-benchtime=10x`):

| Image | MIC-4state-C | PICS-2 | PICS-4 | PICS-8 | Speedup (PICS-4) | Speedup (PICS-8) |
|-------|:------------:|:------:|:------:|:------:|:----------------:|:----------------:|
| MR (256×256) | 353 | 320 | 313 | 283 | 0.9× ⚠ | 0.8× ⚠ |
| CT (512×512) | 389 | 341 | **495** | 477 | **1.3×** | 1.2× |
| CR (2140×1760) | 534 | 561 | 1010 | **1718** | **1.9×** | **3.2×** |
| XR (2048×2577) | 540 | 574 | 1039 | **1367** | **1.9×** | **2.5×** |
| MG1 (2457×1996) | 666 | 902 | 1477 | **2449** | **2.2×** | **3.7×** |
| MG2 (2457×1996) | 692 | 901 | 1480 | **2414** | **2.1×** | **3.5×** |
| MG3 (4774×3064) | 543 | 573 | 1097 | **1850** | **2.0×** | **3.4×** |
| MG4 (4096×3328) | 626 | 790 | 1358 | **2437** | **2.2×** | **3.9×** |

> ⚠ MR (256×256) is too small for PICS — goroutine overhead exceeds the workload. For images ≥ 0.5 MB, PICS-4 delivers 1.9–2.2× and PICS-8 delivers 2.5–3.9× speedup over single-threaded MIC.
