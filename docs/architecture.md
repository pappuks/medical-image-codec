# MIC Architecture Reference

This document describes the internal architecture of MIC: source file layout, bit-depth handling, FSE/ANS internals, and the RLE protocol.

---

## Key Source Files

| File | Purpose |
|------|---------|
| `fseu16.go` | FSE constants, data structures (`ScratchU16`, `symbolTransformU16`, `decSymbolU16`, `cTableU16`), table stepping |
| `fsecompressu16.go` | FSE compression: histogram, normalization, table building, encoding loop |
| `fsedecompressu16.go` | FSE decompression: header parsing, decode table building, decode loop |
| `deltacompressu16.go` | Delta encoding/decoding with overflow delimiter for large differences |
| `deltazigzagcompressu16.go` | Delta + ZigZag encoding variant (maps signed diffs to unsigned) |
| `deltazzrlecompressu16.go` | Combined Delta + ZigZag + RLE pipeline |
| `deltarlecompressu16.go` | Combined Delta + RLE pipeline |
| `rlecompressu16.go` | RLE compression with same/diff run modes |
| `rledecompressu16.go` | RLE decompression (`DecodeNext2` is the hot path) |
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
| `ycocgr.go` | YCoCg-R forward/inverse color transform (reversible, bit-exact) |
| `wsiformat.go` | MIC3 container: header, level descriptors, tile offset table I/O |
| `wsicompress.go` | Tile compression, full WSI compress/decompress, parallel support |
| `wsipyramid.go` | Pyramid generation via 2×2 box filter downsampling |
| `wsi_test.go` | WSI tests: color transform, tiles, full roundtrip, benchmarks |
| `fse2state.go` | Two-state FSE decoder (ILP via dual ANS chains) |
| `fse4state.go` | Four-state FSE decoder (further ILP) |
| `rans8state.go` | 8-state rANS decoder |
| `asm_amd64.go` / `asm_amd64.s` | amd64 assembly: interleaved histogram, CPUID dispatch, YCoCg-R stubs |
| `asm_arm64.go` / `asm_arm64.s` | arm64 assembly: interleaved histogram, NEON dispatch stubs |
| `asm_generic.go` | Pure-Go fallbacks for non-amd64/arm64 platforms |

---

## Compression Pipeline

```
Raw 16-bit pixels
    -> Delta Encoding (spatial prediction: avg of top+left neighbors)
    -> RLE (run-length encoding with same/diff run distinction)
    -> FSE (Finite State Entropy / ANS) or Canonical Huffman
    -> Compressed byte stream
```

---

## Bit-Depth Handling

The codec handles all bit depths (8–16 bit) dynamically using `bits.Len16(maxValue)`:

- `pixelDepth = bits.Len16(maxValue)`
- `deltaThreshold = (1 << (pixelDepth - 1)) - 1`
- `delimiterForOverflow = (1 << pixelDepth) - 1`

There are no separate 8-bit vs 16-bit code paths — everything derives from the actual `maxValue` observed in the image. For 8-bit images this keeps `symbolTT` and `stateTable` at ~2 KB working set instead of the 512 KB needed for a full 16-bit table.

---

## FSE / ANS Internals

MIC uses a Tabled Asymmetric Numeral Systems (tANS) entropy coder extended from the reference implementation to support up to 65 535 distinct symbols (vs 4 095 in the original).

### Encoding

- Processes the input stream **backwards** (last symbol first).
- State transitions use `symbolTT[symbol]` lookups.
- Final ANS state is written as the bitstream header so the decoder can initialize.

### Decoding

- Reads the bitstream **forwards**.
- State transitions use `decTable[state]` lookups (pre-built from the compressed frequency table).
- Each step: read bits → new state → emit symbol.

### Table Parameters

| Parameter | Default | Range |
|-----------|---------|-------|
| `actualTableLog` | 11 | 5–16 |
| Table size | `1 << tableLog` | 32–65 536 |
| Step (spreading) | `(size >> 1) + (size >> 3) + 3` | — |

`tableLog` is automatically raised from 11 → 12 when symbol density is high (>128 distinct symbols with >32 data points per symbol). This improves compression ratio 4–7% on CR and MG modalities.

### The `zeroBits` Flag

When any symbol has probability > 50%, some decode steps emit 0 bits. This flag enables a slower safe-path decode loop with a bounds check on every `getBits` call to handle zero-width state transitions correctly. High-entropy images (medical images after delta coding) rarely trigger this path.

---

## RLE Protocol

The RLE layer distinguishes two run types using a shared `midCount` sentinel:

| Condition | Meaning |
|-----------|---------|
| `count <= midCount` | **Same run** — next word is the single repeated value |
| `count > midCount` | **Diff run** — next `count - midCount` words are distinct values |
| `c == 0` | Same-run exhausted — read new run header |
| `c == midCount` | Diff-run exhausted — read new run header |

**Key invariant:** `c == midCount` signals diff-run completion (new header needed), NOT a continuing same-run. This is the most common source of bugs when modifying the RLE layer.

The minimum encoded run length is 3, which guarantees the RLE output is never larger than its input:
- A same-run of 3 encodes 3 symbols as 2 (header + value), saving 1.
- A diff-run of N costs N+1 symbols, but diff runs only follow same runs that already saved at least 1.

---

## Multi-Frame / MIC2 Format

The MIC2 container supports multi-frame DICOM images (e.g., Breast Tomosynthesis).

### Format Layout

```
Bytes 0-3:    Magic "MIC2"
Bytes 4-7:    Width (uint32 LE)
Bytes 8-11:   Height (uint32 LE)
Bytes 12-15:  Frame count (uint32 LE)
Byte 16:      Pipeline flags (bit0=spatial, bit1=temporal)
Bytes 17-19:  Reserved
Bytes 20+:    Frame offset table (N × 8 bytes: offset_u32 + length_u32)
After table:  Concatenated compressed frame blobs
```

### Compression Modes

| Mode | Frame 0 | Frames 1..N |
|------|---------|-------------|
| **Independent** | Delta+RLE+FSE | Delta+RLE+FSE |
| **Temporal** | Delta+RLE+FSE | ZigZag(residual)+RLE+FSE |

In temporal mode, `residual = current_frame - previous_frame` and ZigZag maps the signed difference to an unsigned value for entropy coding.

### Key Functions

- `CompressMultiFrame` / `DecompressMultiFrame` — full multi-frame encode/decode
- `DecompressFrame` — random access to a single frame by index
- `TemporalDeltaEncode` / `TemporalDeltaDecode` — ZigZag inter-frame residuals

---

## WSI / MIC3 Format

The MIC3 container supports RGB whole slide images for digital pathology.

### Format Layout

```
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

### WSI Pipeline

```
RGB pixels → YCoCg-R transform
  → Y plane:  Delta+RLE+FSE (maxValue ≤ 255)
  → Co plane: Delta+RLE+FSE (ZigZag, maxValue ≤ 510)
  → Cg plane: Delta+RLE+FSE (ZigZag, maxValue ≤ 510)
  → Tile blob: [Y_len][Co_len][Cg_len][Y_data][Co_data][Cg_data]
```

### Per-Plane Encoding Modes

| Mode | Size | Used when |
|------|------|-----------|
| `planeConstantZero` | 1 byte | All pixels are zero |
| `planeConstant` | 3 bytes (mode + uint16) | All pixels are the same non-zero value |
| `planeCompressed` | Variable | Normal case; calls `CompressSingleFrame` |
| `planeRaw` | Uncompressed | Fallback for incompressible data |

### Key Functions

- `CompressWSI` / `DecompressWSITile` / `DecompressWSIRegion`
- `ReadWSIHeader` — parse header without decompressing
- `YCoCgRForward` / `YCoCgRInverse` — reversible color transform

---

## Test Data

Test images in `testdata/`:

| File | Dimensions | Modality | Notes |
|------|-----------|----------|-------|
| MR | 256×256 | Brain/cardiac MRI | 8–12 bit effective depth |
| CT | 512×512 | Computed tomography | Full 16-bit range |
| CR | 2140×1760 | Computed radiography | — |
| XR | 2048×2577 | X-ray | — |
| MG1–MG4 | Various large | Mammography | Best compression ratios |
| MG_TOMO | 2457×1890, 69 frames | Breast Tomosynthesis | 10-bit depth, multiframe DICOM |
| wsi_tissue_512x384.rgb | 512×384 | Pathology (H&E) | Synthetic tissue, RGB 8-bit |
| wsi_background_256x256.rgb | 256×256 | WSI background | White background, RGB 8-bit |
