# CLAUDE.md - Development Guide for MIC (Medical Image Codec)

## Paper Editorial Rules

When editing or creating versions of the MIC IEEE paper (`paper/mic-paper-*.tex`),
follow the rules in [`.claude/paper-rules.md`](.claude/paper-rules.md). These
capture peer-review feedback applied in v5 covering: SE explainability, acronym
expansion, em-dash reduction, duplicate removal, numerical consistency, and
platform descriptions.

## Paper Benchmark Rules

When (re)producing, quoting, or modifying benchmark numbers that appear in the
paper, follow the rules in [`.claude/benchmark-rules.md`](.claude/benchmark-rules.md).
These cover: the canonical `run-paper-benchmarks.sh` workflow, serial vs
parallel benchmark semantics, hardware/platform requirements, re-running and
variance policy, and the paper-table → source-benchmark map. The accompanying
inventory of every benchmark in the repo is in
[`docs/benchmarks.md`](docs/benchmarks.md).

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
go test -run TestParallelStripsRoundtrip -v     # PICS parallel strip round-trip (all modalities)

# Run benchmarks (decompression speed + compression ratio)
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEFSECompress$ mic
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkDeltaRLEHuffCompress$ mic

# Parallel single-image (PICS) benchmarks — strips 1/2/4/8
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkParallelStrips mic

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

# Fair in-process JPEG XL comparison (requires: go build -tags cgo_ojph)
# Prereq: libjxl installed (brew install jpeg-xl), headers in /opt/homebrew/include/jxl
go test -tags cgo_ojph -run TestJXLComparison -v ./ojph/
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkJXLDecomp$ ./ojph/
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkJXLEncode$ ./ojph/

# Full multi-variant comparison: MIC-Go, MIC-4state, MIC-4state-C, MIC-4state-SIMD, MIC-C, MIC-SIMD, HTJ2K, JPEG-LS, JPEG-XL, PICS-2/4/8
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkAllCodecs$ ./ojph/

# Correctness tests for C 4-state implementation
go test -tags cgo_ojph -run TestMICCorrectnessFourStateC -v ./ojph/

# Run all benchmarks
go test -bench=. -benchtime=10x

# Fetch cine / multi-frame source DICOMs (one-time; public-domain samples,
# transcodes the JPEG-Lossless XA to uncompressed via the project .venv)
bash testdata/multiframe/fetch-cine-sources.sh

# Generate browser testdata (MIC .mic variants + manifest.json checksums;
# includes per-frame cine files <id>_f<NNN>*.mic for the multi-frame section)
go run ./cmd/mic-compress -testdata

# Generate reference-codec browser test files (HTJ2K/.jph, JPEG-LS/.jls, JPEG-XL/.jxl)
# for the PACS dashboard. Requires libopenjph/libcharls/libjxl; native round-trip
# verified before each file is written.
go run -tags cgo_ojph ./cmd/mic-refgen

# PACS web-viewer benchmark (see web/README.md "PACS Web Viewer Benchmark"):
#   - Node console report (MIC/PICS live; HTJ2K/JLS/JXL informational):
cd web && node bench-pacs-viewer.mjs
#   - Interactive browser dashboard (all codecs live except JPEG-XL; needs COOP/COEP):
cd web && npm install && bash scripts/vendor-wasm.sh   # one-time: vendor OpenJPH+CharLS WASM
GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/  # for the MIC-WASM (Go) variant
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js
bash web/wasm-c/build.sh                                 # for the MIC-C-WASM variant (needs emscripten/emcc)
bash web/wasm-c/build-pics.sh                            # for the MIC-C-WASM-PICS variant (C pthreads → WASM)
cd web && python3 serve.py 8080                         # open http://localhost:8080/pacs-dashboard.html
#   - Headless CI runner (drives the dashboard, asserts pixel-correctness):
cd web && npx playwright install chromium && npx playwright test
```

## AI Pipeline Build & Test

MIC as an AI data-plane codec, proven on two fronts (full doc:
[`docs/ai-pipeline.md`](docs/ai-pipeline.md); design/plan:
`.hermes/plans/2026-08-27_mic-ai-pipeline.md`):

- **Part A — PyTorch adapter over the C PICS-8 decoder** (`ai/`): decode
  PICS blobs via `libmic_pics.{dylib,so}` (built from `ojph/*.c`) inside a
  `torch.utils.data.Dataset`; GPU-feed benchmark with a headroom verdict.
  MPS measured: decode 1.6–3.2 GB/s vs 0.35 GB/s device consume → **4.7–9.0×
  headroom, decode NOT the bottleneck**. CUDA: run `--device cuda` on the
  Linux GPU box (runbook in `ai/README.md`).
- **Part B — in-browser AI inference** (`web/pacs-dashboard.html?ai=1`):
  MIC decode → preprocess → ONNX inference (WebGPU, WASM fallback) entirely
  in the browser. Runs the live codec set (`AI_DEFAULT_CODEC_IDS` =
  pics-c-wasm-8, mic-4state, htj2k, jpegls). Headless Chromium measured:
  PICS-C-WASM-8 decodes CR 7.2 MB in ~4 ms (31× faster than pure-JS),
  pipeline 289→157 ms. Model: MIT-licensed brain-MRI U-Net (7.76M params,
  `web/models/brain-segmentation-unet.onnx`) — demo only, **not for
  clinical use** (`web/pacs-ai-model.md`).

```bash
# --- Part A (PyTorch; .venv at repo root) ---
make -C ai                                          # build libmic_pics.{dylib,so} from ojph/*.c
.venv/bin/pip install torch numpy pytest            # torch 2.13 has MPS on Apple Silicon
.venv/bin/python -m pytest ai/tests/ -v             # 22 tests; bit-exact vs fnv1a32 ground truth
.venv/bin/python ai/benchmark_feed.py --device mps --iterations 30 --threads 8
.venv/bin/python ai/benchmark_feed.py --device auto --iterations 30 --workers -1  # + worker sweep
# CUDA machine: make && pytest, then --device cuda (see ai/README.md)

# --- Part B (browser AI) ---
cd web && npm install && bash scripts/vendor-onnx.sh # one-time: vendor ort-web bundle into vendor/onnx/
cd web && node scripts/probe-onnx.mjs                # Node WASM smoke test ([1,3,256,256] -> [1,1,256,256])
cd web && python3 serve.py 8080                      # open .../pacs-dashboard.html?ai=1 → Start
cd web && npx playwright test tests/onnx-adapter.spec.mjs tests/pacs-ai.spec.mjs
```

## Web / JavaScript Minification

The `web/` directory ships pre-minified JS files alongside the sources. After editing any of the three source files, regenerate the minified versions:

```bash
cd web

# Minify core decoder
npx terser mic-decoder.js --compress --mangle --output mic-decoder.min.js

# Minify parallel decoder and fix import paths
npx terser mic-decoder-parallel.js --compress --mangle --output mic-decoder-parallel.min.js
sed -i 's|./mic-decoder.js|./mic-decoder.min.js|g' mic-decoder-parallel.min.js
sed -i 's|./mic-worker.js|./mic-worker.min.js|g' mic-decoder-parallel.min.js

# Minify worker and fix import path
npx terser mic-worker.js --compress --mangle --output mic-worker.min.js
sed -i 's|./mic-decoder.js|./mic-decoder.min.js|g' mic-worker.min.js
```

`index.html` imports the `.min.js` versions. Sizes (terser, no gzip):

| File | Source | Minified |
|------|--------|----------|
| `mic-decoder.js` | ~54 KB | ~19 KB |
| `mic-decoder-parallel.js` | ~9.5 KB | ~3.3 KB |
| `mic-worker.js` | ~4 KB | ~1 KB |
| **Total** | **~68 KB** | **~23 KB** |

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
| `parallelstrips.go` | PICS parallel strip compress/decompress (Go); `CompressParallelStrips`, `DecompressParallelStrips` |
| `parallelstrips_test.go` | PICS roundtrip, ratio, format validation, and benchmark tests |
| `ojph/mic_parallel.h` | C header for PICS parallel decompressor (pthreads, AMD64/ARM64) |
| `ojph/mic_parallel.c` | C pthreads implementation; bounded thread pool; dispatches to SIMD or scalar inner decoder |
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

### Single-Frame RGB / MICR Format (Ultrasound, Visible Light)

Single-frame RGB images (US, VL) use `CompressRGB`/`DecompressRGB` — **not** `CompressWSI`. The pipeline is identical (YCoCg-R → Delta+RLE+FSE per plane) but operates on the whole image without tiling.

**Critical**: Using `CompressWSI` for single-frame US/VL images causes 30–45% ratio loss because the delta predictor restarts at every 256×256 tile boundary, destroying spatial correlation. Always use `CompressRGB` for non-tiled images.

The output blob has no magic or dimension metadata; for browser delivery it is wrapped in a **MICR container** written by `cmd/mic-compress`:

```
MICR format:
  Bytes 0-3:  Magic "MICR" (0x4D 0x49 0x43 0x52)
  Bytes 4-7:  Width  (uint32 LE)
  Bytes 8-11: Height (uint32 LE)
  Bytes 12+:  CompressRGB blob ([Y_len][Co_len][Cg_len][Y_data][Co_data][Cg_data])
```

The JS decoder (`web/mic-decoder.js`) detects `MICR_MAGIC` in `decodeFile` and calls `decompressRGBTileBlob(blob, width, height, true)` — the same function used for MIC3 tiles. `index.html` handles `result.isMICR` separately from `result.isMIC3` (no pyramid level selector shown).

Compression ratios on NEMA compsamples (lossless, Delta+RLE+FSE with YCoCg-R):
- US1 (640×480): 6.24×
- VL1–VL3 (756×486): 3.2–3.5×
- VL4 (2226×1868): 1.86×
- VL5 (2670×3340): 1.56×
- VL6 (756×486): 1.93×

Key files: `rgbcompress.go` (`CompressRGB`/`DecompressRGB`), `rgbbench_test.go` (benchmarks + `ReadTIFFRGB`), `cmd/mic-compress/main.go` (`writeMICRFile`, `readTIFFRGB`, `rgbTIFFTestImages`).

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

## AI Pipeline (MIC as an AI data plane)

Two consumers of the codec beyond the PACS viewer; both prove the same
property — **decode is never the bottleneck** for medical-AI workloads.
Canonical doc: [`docs/ai-pipeline.md`](docs/ai-pipeline.md); measured
numbers: `ai/benchmark-notes.md`.

### Part A — PyTorch training ingest (`ai/`)

The C PICS-8 decoder (`ojph/mic_parallel.c` → `mic_decompress_parallel`,
the paper's fastest decoder) is built into a shared library and called from
Python via ctypes — **no codec code is reimplemented in Python**; bit-exact
round-trips against `fnv1a32`-verified ground truth are enforced by tests.

```
web/testdata/<NAME>_pics8.mic (or _pics4)  →  libmic_pics.dylib/.so  →  uint16 numpy
    → PICSDataset (torch)  →  .to(mps/cuda)  →  model
```

Key files:

| File | Purpose |
|------|---------|
| `ai/Makefile` | Builds `libmic_pics.{dylib,so}` from `../ojph/mic_decompress_c.c` + `mic_parallel.c` (no new codec code) |
| `ai/mic_loader.py` | ctypes wrapper over `mic_decompress_parallel`; per-platform lib resolution; **rejects plain MIC1** (PICS blobs only) |
| `ai/mic_dataset.py` | `PICSDataset`/`RawDataset`; module-level `passthrough_collate` + `_worker_init` so `num_workers>0` works with variable-size samples |
| `ai/benchmark_feed.py` | Raw-vs-MIC feed benchmark; `--device {auto,mps,cuda,cpu}`, `--threads` (PICS strip count), `--workers -1` sweep |
| `ai/tests/` | 22 tests: ABI round-trips bit-exact vs ground-truth bins / manifest checksums |

Measured (MPS, Apple Silicon, torch 2.13): decode **1.6–3.2 GB/s** vs device
consume **0.35 GB/s** (24-conv pacer @ 512²) → **headroom 4.7–9.0×, decode
NOT the bottleneck**; `num_workers=2` optimal in the sweep. CUDA runbook in
`ai/README.md` (build the `.so`, install cuXXX torch, `--device cuda`).

Gotchas: the C decoder reads **PICS blobs only** (never plain MIC1);
small images ship `_pics4`, large `_pics8`; metric definitions matter —
`consume_gbps` is compute-only (the honest headroom baseline), `gbps_fed`
is loop-inclusive, and the paper's MB/s is in-process. Don't mix them.

### Part B — Browser AI inference (`web/`, `?ai=1`)

`runAIInference()` in `web/pacs-runner.mjs` decodes each image with each
live codec (`AI_DEFAULT_CODEC_IDS = ['pics-c-wasm-8', 'mic-4state',
'htj2k', 'jpegls']` — PICS-C-WASM-8 first, the fastest browser decoder),
then runs an ONNX model on the decoded pixels, using the same
warmup+median timing discipline as the codec tables.

```
PICS blob → codec adapter decode (timed) → preprocess (grayscale → 3ch, 256×256, [0,1])
    → onnxruntime-web session.run (WebGPU, WASM fallback) → mask [1,1,256,256]
```

Key files:

| File | Purpose |
|------|---------|
| `web/codecs/onnx-adapter.mjs` | ort-web adapter: WebGPU → WASM fallback; **lazy-imported** so the decode dashboard never loads the runtime without `?ai=1`; browser path uses the vendored bundle (no bare imports — no bundler in this repo) |
| `web/scripts/vendor-onnx.sh` | Vendors `ort.all.bundle.min.mjs` + jsep/plain `.wasm` into `web/vendor/onnx/` (mirrors `vendor-wasm.sh`) |
| `web/models/brain-segmentation-unet.onnx` | MIT-licensed pretrained brain-MRI U-Net (`mateuszbuda/brain-segmentation-pytorch`), 7.76M params, opset 17, self-contained 31 MB; export verified to 1.28e-07 vs torch |
| `web/pacs-ai-model.md` | Model provenance, I/O contract, export recipe; **demo only — not for clinical use** |
| `web/tests/pacs-ai.spec.mjs`, `web/tests/onnx-adapter.spec.mjs` | Headless end-to-end AI specs (part of the 3-test Playwright suite) |

Measured (headless Chromium, WASM): on CR 7.2 MB, PICS-C-WASM-8 decodes in
**~4.3 ms** (31× faster than pure-JS MIC-4state's 134 ms) → AI pipeline
**157 ms vs 289 ms**. Inference (~150 ms WASM) dominates small slices;
WebGPU (headful Chrome/Edge) is the regime where decode differences matter
again. CI/headless numbers are WASM — don't quote them as WebGPU.

Gotchas: ort-web must be **vendored** (`scripts/vendor-onnx.sh`) because
the repo serves raw ES modules; the model's input is **3-channel
[1,3,256,256]**, so the preprocessor triplicates grayscale; PICS blobs may
need the `_pics8`↔`_pics4` fallback (recorded as a ⚠ note, never silent).
