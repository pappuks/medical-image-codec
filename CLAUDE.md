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
go test -run TestTemporalDelta -v          # Temporal delta encode/decode
go test -run TestMultiFrame -v             # Multi-frame roundtrip (both modes)
go test -run TestMultiFrameTomo -v         # Real DICOM 69-frame tomo test
go test -run TestYCoCgR -v                # YCoCg-R color transform roundtrip
go test -run TestWSITileCompress -v       # WSI tile compression (white, tissue, gradient)
go test -run TestWSICompress -v           # Full WSI compress/decompress roundtrip
go test -run TestWSIPyramidLevels -v      # Pyramid level generation
go test -run TestWSIRegion -v             # Cross-tile region decompression
go test -run TestWaveletSIMD2DRoundTrip -v      # SIMD 2D wavelet lossless roundtrip
go test -run TestWaveletSIMDMatchesScalar -v    # SIMD vs scalar bit-exact comparison
go test -run TestWaveletV2SIMDRLEFSECompress -v # SIMD wavelet end-to-end pipeline

# Run benchmarks (decompression speed + compression ratio)
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEFSECompress$ mic
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEHuffCompress$ mic

# WSI benchmarks
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkWSITileCompressTissue$ mic
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkWSICompress mic

# Wavelet SIMD vs scalar comparison
go test -benchmem -run=^$ -benchtime=5x -bench "^(BenchmarkWaveletV2RLEFSECompress|BenchmarkWaveletV2SIMDRLEFSECompress)$" mic

# FSE 1-state vs 2-state vs 4-state isolated decompression
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkFSEDecompress4State$ mic

# Fair in-process HTJ2K comparison (requires: go build -tags cgo_ojph)
# Prereq: libopenjph installed in /usr/local/lib, headers in /usr/local/include/openjph
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkHTJ2KFairDecomp$ ./ojph/

# Fair in-process JPEG-LS comparison (requires: go build -tags cgo_ojph)
# Prereq: libcharls installed in /usr/local/lib, headers in /usr/local/include/charls
go test -tags cgo_ojph -run TestJPEGLSComparison -v ./ojph/
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkJPEGLSDecomp$ ./ojph/

# Full multi-variant comparison: MIC-Go, MIC-4state, MIC-4state-C, MIC-4state-SIMD, MIC-C, MIC-SIMD, HTJ2K
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkThreeWay$ ./ojph/

# Correctness tests for C 4-state implementation
go test -tags cgo_ojph -run TestMICCorrectnessFourStateC -v ./ojph/

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
| `temporaldelta.go` | Inter-frame temporal delta encode/decode using ZigZag mapping |
| `multiframe.go` | MIC2 container format: header, frame offset table, read/write |
| `multiframecompress.go` | Multi-frame compress/decompress orchestration (single + multi) |
| `multiframe_test.go` | Multi-frame roundtrip tests (independent + temporal + real DICOM) |
| `fseu16_test.go` | All single-frame tests and benchmarks |
| `waveletu16.go` | 5/3 integer wavelet: 1D lifting, 2D separated transform, scalar helpers |
| `waveletfsecompressu16.go` | Wavelet V1/V2/SIMD compress/decompress pipelines, subband ordering |
| `wavelet_simd_amd64.go` | AMD64 dispatch: AVX2 gate for predict/update kernel calls |
| `wavelet_simd_amd64.s` | AVX2 kernels: `wt53PredictAVX2`, `wt53UpdateAVX2` and inverses |
| `wavelet_simd_arm64.go` | ARM64 dispatch: scalar fallback (blocked layout still improves cache) |
| `wavelet_simd_generic.go` | Generic fallback for non-AMD64/ARM64 targets |
| `waveletu16_test.go` | Wavelet tests: roundtrip, SIMD correctness, benchmarks |

### Wavelet Pipeline (WaveletV2 / SIMD)

An alternative lossless pipeline using the Le Gall 5/3 integer wavelet (same as JPEG 2000 lossless):

```
Raw 16-bit pixels
    -> 5/3 wavelet forward transform (multi-level, separated Mallat layout)
    -> Subband-order coefficient scan
    -> ZigZag encode signed coefficients → uint16 (with escape for overflow)
    -> RLE
    -> FSE
    -> Compressed byte stream
```

The 2D transform applies horizontal rows first, then vertical columns, storing coefficients in the Mallat layout (LL subband top-left, detail subbands in remaining quadrants). Five levels are used by default.

**SIMD variant** (`WaveletV2SIMDRLEFSECompressU16`): uses `wt53Forward2DSeparatedSIMD` which combines two optimizations — blocked column access (8 columns per cache-line) and AVX2 predict/update kernels — for +14–47% throughput on AMD64 (Haswell+). Compressed output is bit-identical to the scalar variant.

### Multi-Frame / MIC2 Format

The codec supports multi-frame images (e.g., Breast Tomosynthesis) via the MIC2 container format with two compression modes:

- **Independent mode**: Each frame compressed separately with spatial Delta+RLE+FSE. Allows random access to any frame.
- **Temporal mode**: Frame 0 uses spatial Delta+RLE+FSE; subsequent frames use inter-frame ZigZag-encoded residuals compressed with RLE+FSE only (no spatial delta, since temporal residuals lack spatial correlation).

```
MIC2 format:
  Bytes 0-3:    Magic "MIC2"
  Bytes 4-7:    Width (uint32 LE)
  Bytes 8-11:   Height (uint32 LE)
  Bytes 12-15:  Frame count (uint32 LE)
  Byte 16:      Pipeline flags (bit0=spatial, bit1=temporal)
  Bytes 17-19:  Reserved
  Bytes 20+:    Frame offset table (N × 8 bytes: offset_u32 + length_u32)
  After table:  Concatenated compressed frame blobs
```

Key functions: `CompressMultiFrame`, `DecompressMultiFrame`, `DecompressFrame` (single frame access), `TemporalDeltaEncode`/`TemporalDeltaDecode` (ZigZag inter-frame residuals).

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

7. **Blocked column transform + AVX2 kernels** (`waveletu16.go:wt53Forward2DSeparatedSIMD`, `wavelet_simd_amd64.s`): Two complementary improvements for the 2D wavelet transform:
   - *Blocked column pass*: processes 8 consecutive columns per loop iteration so each 32-byte cache-line load serves all 8 columns, reducing cache misses ~8× versus the per-column scalar loop. Gain is largest on wide images (CR: +47% throughput).
   - *AVX2 predict/update kernels*: `wt53PredictAVX2`/`wt53UpdateAVX2` and their inverses process 8 `int32` values per cycle using `VPADDD`/`VPSUBD`/`VPSRAD`; dispatched via `cpuHasAVX2` (Haswell+). The kernels operate on contiguous arrays produced by the blocked layout — no gather/scatter needed.
   - Overall decompression speedup: +14–47% across modalities (single-threaded, 5-level transform).
   - Compressed stream is **bit-identical** to scalar V2; only the transform kernel differs.

### Performance-Sensitive Areas

- `decompress()` in `fsedecompressu16.go` — the innermost FSE decode loop; any change here affects all decompression throughput
- `DecodeNext2()` in `rledecompressu16.go` — called once per output symbol during RLE decompression
- `DeltaDecompressU16()` / `DecodeNextSymbolNC()` — called once per pixel during delta decompression
- `countSimple()` in `fsecompressu16.go` — histogram building; memory-bandwidth limited on large images
- `wt53Forward2DSeparatedSIMD()` / `wt53Inverse2DSeparatedSIMD()` — blocked SIMD wavelet transform; column pass is cache-bandwidth limited on large images
- `wt53PredictAVX2()` / `wt53UpdateAVX2()` in `wavelet_simd_amd64.s` — AVX2 inner loops for predict and update steps

### Things to Watch Out For

- The FSE encoder writes **backwards** (last symbol first) while the decoder reads **forwards** — this is fundamental to how ANS works
- `symbolTT` is indexed by raw symbol value (uint16), so it must be at least `symbolLen` in size
- The `zeroBits` flag changes the decode path; when any symbol probability > 50%, some state transitions emit 0 bits which requires bounds-checking on every `getBits` call
- Huffman and FSE use **different** bit reader/writer implementations (forward vs reverse)
- The `cumul` array in `buildCTable` has size `maxSymbolValue + 2` (65537 entries) due to the sentinel
- RLE midCount protocol: same runs count DOWN from midCount, diff runs count DOWN from above midCount. `c == midCount` is the sentinel for diff-run completion
- Wavelet column de-interleave is done in a separate scalar pass after all column blocks; in-place de-interleave for low-pass rows is safe (source row `2i` is always ahead of destination row `i`), but high-pass must use a temp buffer to avoid overwriting source rows before reading them
- The SIMD wavelet functions (`wt53Forward2DSeparatedSIMD`, `wt53Inverse2DSeparatedSIMD`) produce the same Mallat subband layout as the scalar equivalents; the compressed stream is interchangeable between `WaveletV2RLEFSECompressU16` and `WaveletV2SIMDRLEFSECompressU16`

### WSI / MIC3 Format (Whole Slide Imaging)

The codec supports RGB whole slide images for digital pathology via the MIC3 tiled container format with pyramid levels:

- **RGB support**: YCoCg-R reversible color transform decorrelates RGB into Y (luminance) + Co/Cg (chrominance). Each plane is compressed independently through the existing Delta+RLE+FSE pipeline.
- **Tiled architecture**: Images divided into tiles (default 256×256) for O(1) random access
- **Pyramid levels**: Multi-resolution levels (each ½ the previous dimension) generated via 2×2 box filter downsampling
- **Parallel compression**: Tiles are independent — goroutine worker pool for parallel encode/decode
- **Constant-plane optimization**: Background tiles (all white/black) compress to 15-17 bytes total

```
WSI Pipeline:
  RGB pixels → YCoCg-R transform
    → Y plane:  Delta+RLE+FSE (maxValue ≤ 255)
    → Co plane: Delta+RLE+FSE (ZigZag, maxValue ≤ 510)
    → Cg plane: Delta+RLE+FSE (ZigZag, maxValue ≤ 510)
    → Tile blob: [Y_len][Co_len][Cg_len][Y_data][Co_data][Cg_data]
```

```
MIC3 format:
  Bytes 0-3:    Magic "MIC3"
  Bytes 4-7:    Version (uint32 LE)
  Bytes 8-15:   Width × Height (uint32 LE each)
  Bytes 16-23:  TileWidth × TileHeight (uint32 LE each)
  Bytes 24-25:  Channels (uint16 LE: 1=grey, 3=RGB)
  Byte 26:      Bits per sample (8 or 16)
  Byte 27:      Flags (bit0=spatial, bit1=color_transform)
  Bytes 28-29:  Pyramid level count
  Bytes 32-39:  Total tile count (uint64 LE)
  After header: Level descriptors (N × 20 bytes)
  After levels: Tile offset table (M × 16 bytes: offset_u64 + length_u64)
  After table:  Concatenated compressed tile blobs
```

Key files:

| File | Purpose |
|------|---------|
| `ycocgr.go` | YCoCg-R forward/inverse color transform (reversible, bit-exact) |
| `wsiformat.go` | MIC3 container: header, level descriptors, tile offset table I/O |
| `wsicompress.go` | Tile compression, full WSI compress/decompress, parallel support |
| `wsipyramid.go` | Pyramid generation via 2×2 box filter downsampling |
| `wsi_test.go` | WSI tests: color transform, tiles, full roundtrip, benchmarks |

Key functions: `CompressWSI`, `DecompressWSITile`, `DecompressWSIRegion`, `ReadWSIHeader`, `YCoCgRForward`/`YCoCgRInverse`.

Per-plane encoding modes: `planeConstantZero` (1 byte), `planeConstant` (3 bytes: mode + uint16), `planeCompressed` (CompressSingleFrame), `planeRaw` (fallback for incompressible data).

## Test Data

Test images in `testdata/`:
- MR (256x256) — Brain/cardiac MRI, 8-12 bit effective depth
- CT (512x512) — Computed tomography, full 16-bit range
- CR (2140x1760) — Computed radiography
- XR (2048x2577) — X-ray
- MG1-MG4 (various large sizes) — Mammography, best compression ratios
- MG_TOMO (2457x1890, 69 frames) — Breast Tomosynthesis multiframe DICOM, 10-bit depth
- wsi_tissue_512x384.rgb — Synthetic H&E-stained tissue (RGB, 8-bit)
- wsi_background_256x256.rgb — White background tile (RGB, 8-bit)
