# HTJ2K Performance Comparison Framework

## Overview

The `ojph/` package provides an in-process benchmarking framework for comparing MIC against HTJ2K (High Throughput JPEG 2000) using [OpenJPH](https://github.com/aous72/OpenJPH) via CGO. This eliminates the subprocess launch and file I/O overhead that inflated HTJ2K timings in earlier comparisons, providing a fair apples-to-apples measurement.

The package also includes a C implementation of the MIC decompression pipeline (scalar and SIMD-optimized), enabling a four-way comparison: MIC-Go vs MIC-C vs MIC-SIMD vs HTJ2K — all as in-process library calls with in-memory I/O.

---

## Build Requirements

The `ojph/` package is gated behind the `cgo_ojph` build tag and requires:

- **CGO enabled** (`CGO_ENABLED=1`)
- **OpenJPH** (`libopenjph`) installed in `/usr/local/lib` with headers in `/usr/local/include`
- **C/C++ compiler** with SSE2/AVX2 support (for the SIMD-optimized MIC-C path)

Without the `cgo_ojph` tag, `go build ./...` and `go test ./...` skip the entire package — CI runs cleanly without OpenJPH installed.

```bash
# Build/test WITHOUT ojph (default — works everywhere)
go build ./...
go test ./...

# Build/test WITH ojph (requires OpenJPH + CGO)
go test -tags cgo_ojph -v ./ojph/
```

---

## Components

### 1. OpenJPH CGO Bindings (`ojph.go`, `ojph_wrapper.cpp`)

Provides `CompressU16` and `DecompressU16` functions that call OpenJPH's `ojph::codestream` API directly via CGO:

- **Compression**: Configures SIZ/COD markers for reversible (lossless) mode, pushes rows via `codestream.exchange()`, writes to `ojph::mem_outfile`
- **Decompression**: Reads from `ojph::mem_infile`, pulls rows via `codestream.pull()`, copies to uint16 output buffer

No subprocess, no temp files — pure in-process encode/decode.

### 2. MIC C Implementation (`mic_decompress_c.c`, `mic_decompress_c.h`)

A complete C port of the MIC decompression pipeline for cross-language performance comparison:

- **FSE two-state decoder**: Reverse bit reader, `readNCount` header parser, `buildDtable`, two-state decode loop with `zeroBits` safe path
- **RLE decoder**: Same/diff run protocol matching the Go implementation
- **Delta decoder**: avg(left, top) predictor with overflow delimiter handling

Two entry points:
- `mic_decompress_two_state()` — Scalar C implementation (fused RLE+Delta in one pass)
- `mic_decompress_two_state_simd()` — SIMD-optimized two-pass architecture (x86-64 only, falls back to scalar on other platforms)

### 3. SIMD-Optimized Decompression (`mic_decompress_c.c`)

The SIMD path uses a two-pass architecture to maximize vectorization opportunities:

**Pass 1 — RLE Decode (SIMD fills)**:
- Same-runs: `_mm_set1_epi16` / `_mm256_set1_epi16` for SSE2/AVX2 broadcast fills
- Diff-runs: `_mm_loadu_si128` / `_mm256_loadu_si256` for bulk memory copies
- Falls back to scalar for runs shorter than 8 elements

**Pass 2 — Delta Decode (SIMD-assisted)**:
- SIMD delimiter scanning: `_mm_cmpeq_epi16` + `_mm_movemask_epi8` to find delimiter-free stretches in batches of 8 pixels
- SIMD batch preloading: Loads top-row values and computes diffs with `_mm_sub_epi16` in bulk
- Serial left-neighbor dependency: The avg(left, top) predictor has an inherent left-to-right dependency that cannot be fully parallelized; the SIMD path reduces memory loads but the accumulation remains serial

---

## Tests and Benchmarks

All tests require `-tags cgo_ojph`:

### Correctness Tests

```bash
# Verify SIMD decompression matches original pixels (all 8 test images)
go test -tags cgo_ojph -run TestMICCorrectnessSIMD -v ./ojph/

# Verify C scalar decompression matches Go decompression
go test -tags cgo_ojph -run TestMICCorrectnessC -v ./ojph/

# OpenJPH roundtrip test
go test -tags cgo_ojph -run TestRoundtrip -v ./ojph/
```

### Comparison Tests

```bash
# Fair MIC vs HTJ2K comparison (both in-process, no subprocess overhead)
go test -tags cgo_ojph -run TestHTJ2KFairComparison -v ./ojph/ -timeout 300s

# Four-way comparison: MIC-Go vs MIC-C vs MIC-SIMD vs HTJ2K
go test -tags cgo_ojph -run TestFourWayComparison -v ./ojph/ -timeout 300s
```

### Benchmarks

```bash
# Go benchmarks for all decompressors
go test -tags cgo_ojph -run=^$ -bench BenchmarkHTJ2KFairDecomp ./ojph/ -benchtime=10x

# Three-way benchmark: MIC-Go vs MIC-C vs MIC-SIMD vs HTJ2K
go test -tags cgo_ojph -run=^$ -bench BenchmarkThreeWay ./ojph/ -benchtime=10x
```

---

## Files

| File | Purpose |
|------|---------|
| `ojph.go` | Go API: `CompressU16`, `DecompressU16` via OpenJPH CGO bindings |
| `ojph_wrapper.cpp` | C++ implementation calling OpenJPH `codestream` API |
| `ojph_wrapper.h` | C header for OpenJPH wrapper functions |
| `mic_decompress_c.c` | C implementation of MIC decompression (scalar + SIMD) |
| `mic_decompress_c.h` | C header for MIC decompression functions |
| `mic_c.go` | Go CGO bindings for MIC-C and MIC-SIMD decompression |
| `htj2k_fair_comparison_test.go` | Fair in-process MIC vs HTJ2K comparison test + benchmark |
| `mic_c_test.go` | Correctness tests and four-way comparison benchmark |
| `ojph_test.go` | Basic OpenJPH roundtrip test |

All `.go` files have `//go:build cgo_ojph` — the package is invisible without the tag.

---

## Design Decisions

### Why a build tag?

The `ojph/` package depends on `libopenjph` (a C++ shared library) and uses `-mavx2` CFLAGS. CI environments typically don't have OpenJPH installed, and cross-compilation with CGO adds complexity. The `cgo_ojph` build tag keeps the main build clean while allowing developers with the required libraries to run the full comparison suite.

### Why a two-pass SIMD architecture?

The original Go pipeline fuses RLE and Delta decoding in a single pass through the `DecodeNext2` iterator. While memory-efficient, this prevents vectorization because each RLE symbol must be decoded before the delta predictor can consume it.

The C SIMD path separates these into two passes:
1. RLE → intermediate buffer (SIMD-friendly: bulk fills and copies)
2. Delta decode from the buffer (SIMD-assisted: batch delimiter scanning and top-row preloading)

The intermediate buffer costs extra memory but enables SIMD operations on both passes.

### Why is the delta decode still partially serial?

The avg(left, top) predictor creates a left-to-right data dependency chain within each row: pixel[x] depends on pixel[x-1]. This dependency cannot be broken by SIMD. The SIMD path helps by:
- Scanning for delimiter-free stretches in bulk (avoiding per-pixel branches)
- Preloading top-row values and computing diffs in SIMD registers
- Reducing total memory loads in the inner loop

Full row-parallel delta decoding would require a different predictor (e.g., top-only), which would sacrifice compression ratio.
