# MIC Developer Guide

This document covers the optimization strategy, performance-sensitive code paths, and common pitfalls for contributors to MIC.

---

## Applied Optimizations

The following optimizations are implemented and active in the codebase:

### 1. FSE Decode Loop Inlining (`fsedecompressu16.go:decompress`)

State transitions are inlined directly into the hot loop with a local `dt` slice reference. This eliminates function call overhead and pointer indirections that the Go compiler cannot reliably eliminate across function boundaries. The `dt` local variable pins the decode table slice header on the stack so every table access is a single indexed load.

### 2. Dual-Buffer Histogram (`fsecompressu16.go:countSimple`)

Two count arrays process symbol pairs in the same loop iteration:
- Even-indexed pixels → `count[]`
- Odd-indexed pixels → `count2[]`

This avoids store-to-load forwarding stalls when consecutive pixels have similar values — very common in medical images after delta coding where long runs of the same residual occur. The arrays are merged in a single backward scan.

### 3. Adaptive `tableLog` (`fsecompressu16.go:optimalTableLog`)

`tableLog` is automatically raised from 11 → 12 when symbol density exceeds a threshold (>128 distinct symbols, >32 data points per symbol). Higher `tableLog` gives better frequency precision for the wide, sparse distributions produced by delta coding of 12–16 bit medical images. Improves compression ratio 4–7% on CR and MG modalities with negligible decompression speed impact.

### 4. Branch-Free Delta Decompression (`deltacompressu16.go:DeltaDecompressU16`)

Separate loops for corner, first-row, first-column, and interior pixels eliminate per-pixel boundary branching in the hot interior loop. The interior loop can run unconditionally without checking `x == 0` or `y == 0` on every iteration.

### 5. RLE Fast-Path (`rledecompressu16.go:DecodeNext2`)

"Same" runs (the most common output after delta coding) are fast-pathed to return the recurring value without consuming any bytes from the input slice. The slow path (reading a new run header) is only taken at run boundaries. This keeps the common case at ~3 instructions.

**Critical:** `c == midCount` means "diff-run exhausted" (new header needed), NOT "same-run continuing". This is the counterintuitive sentinel that must be preserved if the RLE layer is modified.

### 6. Dynamic Table Sizing (`fsecompressu16.go:allocCtable`, `fsedecompressu16.go:allocDtable`)

`symbolTT` and `stateTable` are allocated to the actual symbol range (`maxSymbol + 1`) rather than always 65 536. For 8-bit images after delta coding this reduces the working set from ~512 KB to ~2 KB, fitting entirely in L1 cache. For CT images with full 16-bit range the tables remain at their maximum size.

---

## Performance-Sensitive Areas

These functions appear in the hot path of decompression and should be modified with caution. Any regression in their performance directly affects the decompression throughput numbers reported in benchmarks.

| Function | File | Called |
|----------|------|--------|
| `decompress()` | `fsedecompressu16.go` | Innermost FSE decode loop; drives all FSE throughput |
| `DecodeNext2()` | `rledecompressu16.go` | Once per output symbol during RLE decompression |
| `DeltaDecompressU16()` | `deltacompressu16.go` | Once per pixel during delta decompression |
| `DecodeNextSymbolNC()` | `fsedecompressu16.go` | Called from within `decompress()` |
| `countSimple()` | `fsecompressu16.go` | Histogram building; memory-bandwidth limited on large images |

Use `go test -benchmem -run=^$ -bench ^BenchmarkDeltaRLEFSECompress$ -benchtime=10x` before and after any changes to these functions.

---

## Things to Watch Out For

### FSE Encoding Direction

The FSE encoder writes **backwards** (last symbol first) while the decoder reads **forwards**. This is fundamental to how tANS works — the final encoder state becomes the initial decoder state. Do not change the encoding direction without inverting the decoding direction too.

### `symbolTT` Indexing

`symbolTT` is indexed by raw symbol value (uint16), so it must be allocated with at least `symbolLen` entries where `symbolLen = maxSymbol + 1`. Writing to `symbolTT[maxSymbol]` must not go out of bounds.

### The `zeroBits` Flag

When any symbol has probability > 50%, some state transitions in the decode table emit 0 bits. The `zeroBits` flag enables a safe decode path that bounds-checks every `getBits` call. Without this flag, a zero-width transition would read a stale bit from the bitstream. If you modify the table normalization or spreading code, verify `zeroBits` is set correctly.

### Huffman vs FSE Bit I/O

Huffman and FSE use **different** bit reader/writer implementations:

| Stage | Direction | Files |
|-------|-----------|-------|
| FSE | Reverse (encoder writes backwards) | `bitwriter.go`, `bitreader.go` |
| Huffman | Forward | `bitwriterhuff.go`, `bitreaderhuff.go` |

Do not mix these implementations. The `bitreaderhuff.go` reader expects bits in the order they were written by `bitwriterhuff.go`.

### `cumul` Array Size

The `cumul` array in `buildCTable` has size `maxSymbolValue + 2` (up to 65 537 entries) due to a sentinel at `cumul[maxSymbol + 1]`. Allocating only `maxSymbol + 1` entries will cause an off-by-one write.

### RLE `midCount` Sentinel

- Same runs count **DOWN** from `midCount` to 0.
- Diff runs count **DOWN** from above `midCount` to `midCount`.
- `c == midCount` is the sentinel for diff-run completion — it does NOT mean the same-run is continuing.

This protocol is easy to invert accidentally. If you see incorrect output with correct-looking counts, check whether you are checking `c == 0` where you should be checking `c == midCount` or vice versa.

---

## Running Tests

```bash
# Run all tests
go test -v ./...

# Single-frame compression pipelines
go test -run TestDeltaRleFSECompress -v     # Delta+RLE+FSE pipeline
go test -run TestDeltaRleHuffCompress -v    # Delta+RLE+Huffman pipeline
go test -run TestFSECompress -v             # FSE only
go test -run TestHuffCompress -v            # Huffman only

# Multi-frame
go test -run TestTemporalDelta -v           # Temporal delta encode/decode
go test -run TestMultiFrame -v              # Multi-frame roundtrip (both modes)
go test -run TestMultiFrameTomo -v          # Real DICOM 69-frame tomo test

# WSI
go test -run TestYCoCgR -v                  # YCoCg-R color transform roundtrip
go test -run TestWSITileCompress -v         # WSI tile compression (white, tissue, gradient)
go test -run TestWSICompress -v             # Full WSI compress/decompress roundtrip
go test -run TestWSIPyramidLevels -v        # Pyramid level generation
go test -run TestWSIRegion -v              # Cross-tile region decompression

# Comparison tests
go test -run TestDeltaZstdComparison -v     # MIC vs Delta+Zstandard
go test -run TestMEDPredictorComparison -v  # avg predictor vs MED predictor
go test -run TestHTJ2KComparison -v -timeout 300s  # MIC vs HTJ2K (requires ojph)
```

## Running Benchmarks

```bash
# Full Delta+RLE+FSE pipeline (parallel, 200 goroutines — matches real-world multi-frame rendering)
go test -benchmem -run=^$ -benchtime=200x -bench ^BenchmarkDeltaRLEFSECompress$ mic

# Single-state vs two-state FSE (isolated FSE decompression)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSEDecompress mic

# Single-state vs two-state: full Delta+RLE+FSE pipeline
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkDeltaRLEFSEDecompress mic

# Human-readable speedup table for 1-state vs 2-state
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSE2StateSummary -v mic

# Huffman pipeline
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEHuffCompress$ mic

# WSI benchmarks
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkWSITileCompressTissue$ mic
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkWSICompress mic

# All benchmarks
go test -bench=. -benchtime=10x
```

> **Benchmark methodology:** All decompression benchmarks spawn one goroutine per iteration and run all `b.N` goroutines concurrently. The reported MB/s reflects **aggregate multi-core throughput**, not single-core speed. Use `-benchtime=1x` for single-goroutine measurements.

---

## Adding a New Modality or Test Image

1. Place the raw binary (little-endian uint16) under `testdata/`.
2. Add a `testCase` entry in `fseu16_test.go` with the filename, width, height, and expected compression ratio range.
3. Run `go test -run TestDeltaRleFSECompress -v` and verify the roundtrip passes.
4. Optionally add a benchmark entry to `BenchmarkDeltaRLEFSECompress`.

## Modifying the Container Format (MIC2 / MIC3)

- Magic bytes and header field offsets are defined in `multiframe.go` (MIC2) and `wsiformat.go` (MIC3).
- Both formats use little-endian byte order throughout.
- The frame/tile offset table must be written **before** the compressed data blobs so readers can seek without scanning the entire file.
- Increment the version field in MIC3 if the header layout changes in a backward-incompatible way.
