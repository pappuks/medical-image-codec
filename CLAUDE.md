# CLAUDE.md - Development Guide for MIC (Medical Image Codec)

## Project Overview

MIC is a lossless compression codec for 16-bit medical images (DICOM) implemented in Go. It uses a pipeline of Delta Encoding + RLE + FSE/Huffman to achieve compression ratios of 1.7x-8.9x with very high decompression throughput (up to 16 GB/s).

## Build & Test

```bash
# Run all tests
go test -v ./...

# Run specific test suites
go test -run TestDeltaRleFSECompress -v    # Delta+RLE+FSE pipeline
go test -run TestDeltaRleHuffCompress -v   # Delta+RLE+Huffman pipeline
go test -run TestFSECompress -v            # FSE only
go test -run TestHuffCompress -v           # Huffman only

# Run benchmarks (decompression speed + compression ratio)
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEFSECompress$ mic
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEHuffCompress$ mic

# Run all benchmarks
go test -bench=. -benchtime=10x
```

## Architecture

### Compression Pipeline

```
Raw 16-bit pixels
    -> Delta Encoding (spatial prediction: avg of top+left neighbors)
    -> RLE (run-length encoding with same/diff run distinction)
    -> FSE (Finite State Entropy / ANS) or Canonical Huffman
    -> Compressed byte stream
```

### Key Source Files

| File | Purpose |
|------|---------|
| `fseu16.go` | FSE constants, data structures (ScratchU16, symbolTransformU16, decSymbolU16, cTableU16), table stepping |
| `fsecompressu16.go` | FSE compression: histogram, normalization, table building, encoding loop |
| `fsedecompressu16.go` | FSE decompression: header parsing, decode table building, decode loop |
| `deltacompressu16.go` | Delta encoding/decoding with overflow delimiter for large differences |
| `deltazigzagcompressu16.go` | Delta + ZigZag encoding variant (maps signed diffs to unsigned) |
| `deltazzrlecompressu16.go` | Combined Delta + ZigZag + RLE pipeline |
| `deltarlecompressu16.go` | Combined Delta + RLE pipeline |
| `rlecompressu16.go` | RLE compression with same/diff run modes |
| `rledecompressu16.go` | RLE decompression (DecodeNext2 is the hot path) |
| `canhuffmancompressu16.go` | Canonical Huffman compression with adaptive symbol selection |
| `canhuffmandecompressu16.go` | Canonical Huffman decompression with lookup table |
| `bitwriter.go` / `bitreader.go` | Bit-level I/O for FSE (reverse direction) |
| `bitwriterhuff.go` / `bitreaderhuff.go` | Bit-level I/O for Huffman (forward direction) |
| `wordreader.go` / `bytereader.go` | Word/byte-level readers |
| `fseu16_test.go` | All tests and benchmarks |

### Bit-Depth Handling

The codec handles all bit depths (8-16 bit) dynamically using `bits.Len16(maxValue)`:
- Thresholds: `deltaThreshold = (1 << (pixelDepth-1)) - 1`
- Delimiters: `delimiterForOverflow = (1 << pixelDepth) - 1`
- No separate 8-bit vs 16-bit code paths; everything derives from actual maxValue

### FSE/ANS Internals

- **Encoding**: Processes input backwards; state transitions via `symbolTT[symbol]` lookup
- **Decoding**: Forward processing; state transitions via `decTable[state]` lookup
- **Table spreading**: Uses `step = (tableSize >> 1) + (tableSize >> 3) + 3` to distribute symbols
- **State machine**: `actualTableLog` bits determine table size (default 11, range 5-16)
- **zeroBits flag**: When any symbol has probability > 50%, some decode steps output 0 bits; requires slower safe-path decoding

### RLE Protocol

- `count <= midCount`: "same" run — next word is the repeated value
- `count > midCount`: "diff" run — next `count - midCount` words are distinct values
- `c == 0`: same-run exhausted, read new header
- `c == midCount`: diff-run exhausted, read new header

## Optimization Notes

### Applied Optimizations

1. **FSE decode loop inlining** (`fsedecompressu16.go:decompress`): State transitions are inlined directly into the hot loop with a local `dt` slice reference, reducing function call overhead and pointer indirections.

2. **Dual-buffer histogram** (`fsecompressu16.go:countSimple`): Two count arrays process symbol pairs, reducing store-to-load forwarding stalls when consecutive pixels have similar values (very common in medical images).

3. **Adaptive tableLog** (`fsecompressu16.go:optimalTableLog`): Automatically bumps tableLog from 11 to 12 when symbol density is high enough (>128 distinct symbols with >32 data points per symbol). This gives better frequency precision for 12-16 bit medical images after delta coding. Improves compression ratio by 4-7% on CR and MG modalities.

4. **Branch-free delta decompression** (`deltacompressu16.go:DeltaDecompressU16`): Separate loops for corner, first-row, first-column, and interior pixels eliminate per-pixel boundary branching in the hot interior loop.

5. **RLE fast-path** (`rledecompressu16.go:DecodeNext2`): "Same" runs (most common after delta coding) are fast-pathed to return the recurring value without touching the input slice. Critical: `c == midCount` means "diff-run exhausted" (new header needed), NOT "same-run continuing".

6. **Dynamic table sizing** (`fsecompressu16.go:allocCtable`, `fsedecompressu16.go:allocDtable`): symbolTT and stateTable are sized to actual symbol range instead of always 65536. For 8-bit images this reduces working set from 512KB to ~2KB.

### Performance-Sensitive Areas

- `decompress()` in `fsedecompressu16.go` — the innermost FSE decode loop; any change here affects all decompression throughput
- `DecodeNext2()` in `rledecompressu16.go` — called once per output symbol during RLE decompression
- `DeltaDecompressU16()` / `DecodeNextSymbolNC()` — called once per pixel during delta decompression
- `countSimple()` in `fsecompressu16.go` — histogram building; memory-bandwidth limited on large images

### Things to Watch Out For

- The FSE encoder writes **backwards** (last symbol first) while the decoder reads **forwards** — this is fundamental to how ANS works
- `symbolTT` is indexed by raw symbol value (uint16), so it must be at least `symbolLen` in size
- The `zeroBits` flag changes the decode path; when any symbol probability > 50%, some state transitions emit 0 bits which requires bounds-checking on every `getBits` call
- Huffman and FSE use **different** bit reader/writer implementations (forward vs reverse)
- The `cumul` array in `buildCTable` has size `maxSymbolValue + 2` (65537 entries) due to the sentinel
- RLE midCount protocol: same runs count DOWN from midCount, diff runs count DOWN from above midCount. `c == midCount` is the sentinel for diff-run completion

## Test Data

Test images in `testdata/`:
- MR (256x256) — Brain/cardiac MRI, 8-12 bit effective depth
- CT (512x512) — Computed tomography, full 16-bit range
- CR (2140x1760) — Computed radiography
- XR (2048x2577) — X-ray
- MG1-MG4 (various large sizes) — Mammography, best compression ratios
