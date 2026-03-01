# WSI Codec Extension Plan

## Executive Summary

Extend the MIC codec to support Whole Slide Imaging (WSI) for digital pathology. WSI files are typically 2-4 GB of RGB image data at resolutions up to 100,000 x 100,000 pixels, stored as tiled TIFF pyramids. The pathology world currently lacks a purpose-built lossless codec — JPEG2000 is slow, JPEG-XL adoption is nascent, and generic codecs ignore the domain's structure. MIC's existing Delta+RLE+FSE pipeline generalizes naturally to RGB via a reversible color transform (YCoCg-R), and the tiled architecture maps cleanly onto a new MIC3 container format with per-tile random access and parallel encode/decode.

**Target**: Lossless RGB compression at 3-6x ratio (tissue tiles), 50-100x (background tiles), with decompression throughput exceeding 4 GB/s on modern hardware through tile-level parallelism.

---

## 1. Current Codec Architecture (Baseline)

### What We Have

| Capability | Current State |
|---|---|
| Data type | `[]uint16` — single-channel greyscale |
| Bit depth | Dynamic 8-16 bit via `bits.Len16(maxValue)` |
| Pipeline | Delta (avg(top,left) predictor) → RLE → FSE/Huffman |
| Container | MIC2 — linear frame sequence with offset table |
| Multi-frame | Sequential frames, temporal delta between consecutive frames |
| Max file size | ~4 GB (uint32 offsets in MIC2) |
| Parallelism | None (single-threaded) |

### Key Reusable Components

The entire compression pipeline can be reused without modification:

- **`CompressSingleFrame(pixels []uint16, width, height int, maxValue uint16)`** — This function accepts any `[]uint16` data with arbitrary dimensions and bit depth. A 256×256 tile of 8-bit luminance values (maxValue=255) compresses identically to a 256×256 medical image of the same depth.

- **Delta encoding** (`deltacompressu16.go`) — The spatial predictor (average of top and left neighbors) works on any 2D grid. WSI tiles have even stronger spatial correlation than medical greyscale, since tissue staining produces smooth color gradients.

- **RLE** (`rlecompressu16.go`) — Operates on the delta-coded symbol stream. Background tiles (all-white) will produce extremely long same-runs.

- **FSE/ANS** (`fsecompressu16.go`, `fsedecompressu16.go`) — Entropy coder is symbol-agnostic. The adaptive tableLog (bumping to 12 for high symbol density) is beneficial for delta-coded tile data.

- **Bit depth auto-sizing** — `bits.Len16(maxValue)` determines thresholds and delimiters. For 8-bit RGB channels (maxValue=255), this naturally produces 8-bit pipeline parameters. After YCoCg-R, Co/Cg channels have maxValue up to 510, which still fits comfortably in uint16.

---

## 2. WSI Domain Analysis

### Image Characteristics

| Property | Typical Value |
|---|---|
| Color space | RGB, 8 bits per channel (24-bit) |
| Full resolution | 20,000 × 20,000 to 150,000 × 100,000 pixels |
| Raw size | 1.2 GB to 45 GB |
| Tile size | 256×256 or 512×512 pixels (standard) |
| Pyramid levels | 5-9 levels (each ½ the previous dimension) |
| Background ratio | 30-70% of tiles are near-white background |
| Staining | H&E (hematoxylin: blue/purple nuclei, eosin: pink cytoplasm) |
| Scanners | Aperio, Hamamatsu, Leica, Philips, 3DHistech |

### Why Current Codecs Fall Short

| Codec | Issue for WSI |
|---|---|
| JPEG2000 | Very slow decompression (10-50 MB/s). Patent concerns. Default in DICOM but universally disliked. |
| JPEG-XL | Promising but adoption is early. No hardware acceleration. Complex implementation. |
| PNG | No tiled access. Poor compression on stained tissue. |
| LZ4/Zstd | Generic byte compressors — miss the spatial structure of image data. |
| JPEG (lossy) | Not acceptable for primary diagnosis in many jurisdictions. |

### DICOM WSI (Supplement 145/185)

DICOM Supplement 145 defines the VL Whole Slide Microscopy Image IOD. Key elements:

- **Tiled storage**: Image is divided into tiles, each stored as a separate frame in the DICOM Pixel Data element
- **Dimension Organization**: Tiles indexed by (column, row) position within the slide
- **Total Pixel Matrix**: Full image dimensions stored in header attributes
- **Transfer Syntax**: Currently supports JPEG2000 (lossless/lossy), JPEG, JPEG-LS
- **Pyramid**: Multi-resolution encoded as separate DICOM instances or concatenation

A new MIC transfer syntax could be registered for DICOM, but the immediate goal is a standalone format that can be embedded in DICOM's encapsulated pixel data.

---

## 3. RGB Compression Strategy: YCoCg-R Color Transform

### Why Not Compress RGB Directly?

If we split RGB into 3 independent channels and compress each:
- R, G, B channels are highly correlated (a pink eosin region has similar patterns in all three)
- Delta coding on each channel independently wastes bits encoding the same spatial structure three times
- Inter-channel redundancy is untapped

### YCoCg-R: Reversible Luma-Chroma Transform

The YCoCg-R transform decorrelates RGB into luminance (Y) and chrominance (Co, Cg) with perfect integer reversibility:

```
Forward (RGB → YCoCg):
  Co = R - B
  t  = B + (Co >> 1)
  Cg = G - t
  Y  = t + (Cg >> 1)

Inverse (YCoCg → RGB):
  t  = Y  - (Cg >> 1)
  G  = Cg + t
  B  = t  - (Co >> 1)
  R  = Co + B
```

### Properties

| Property | Value |
|---|---|
| Reversibility | Bit-exact for integer inputs |
| Y range | [0, 255] for 8-bit RGB input |
| Co range | [-255, 255] → ZigZag maps to [0, 510] (9-bit, fits uint16) |
| Cg range | [-255, 255] → ZigZag maps to [0, 510] (9-bit, fits uint16) |
| Compression benefit | Y captures most energy; Co/Cg are sparse (concentrated near 0) |
| Background tiles | White (255,255,255) → Y=255, Co=0, Cg=0 → extremely compressible |

### Per-Plane Compression

After YCoCg-R, each plane is compressed independently through the existing pipeline:

```
RGB tile (256×256×3 bytes)
  → YCoCg-R transform
  → Y plane (256×256 uint16, maxValue ≤ 255):   Delta+RLE+FSE
  → Co plane (256×256 uint16, maxValue ≤ 510):  Delta+RLE+FSE
  → Cg plane (256×256 uint16, maxValue ≤ 510):  Delta+RLE+FSE
  → Compressed tile blob = [Y_compressed | Co_compressed | Cg_compressed]
```

The compressed tile blob stores the three plane blobs sequentially with per-plane length headers:

```
Tile blob layout:
  Bytes 0-3:    Y compressed length (uint32 LE)
  Bytes 4-7:    Co compressed length (uint32 LE)
  Bytes 8..:    Y compressed data
  After Y:      Co compressed data
  After Co:     Cg compressed data (length = total - 8 - Y_len - Co_len)
```

### Expected Compression Ratios

| Tile Type | Y ratio | Co ratio | Cg ratio | Overall |
|---|---|---|---|---|
| Background (white) | ~100x | near-infinite (all zeros) | near-infinite | ~50-100x |
| Dense tissue (H&E) | 2-3x | 5-10x | 5-10x | 3-5x |
| Mixed (typical slide) | Weighted average across tiles | | | 4-8x |

The Co/Cg channels after YCoCg-R on H&E stained tissue are extremely sparse — hematoxylin and eosin produce relatively uniform color shifts, so inter-pixel chroma differences are small. This plays directly into the RLE stage's strength.

---

## 4. MIC3 Container Format

### Design Goals

1. **Tiled random access**: Read any tile at any pyramid level in O(1) seeks
2. **Large file support**: uint64 offsets for files up to 16 EB
3. **Pyramid native**: Multi-resolution levels are first-class
4. **Streaming write**: Tiles can be written incrementally
5. **Self-describing**: Header contains all metadata needed to decompress

### Binary Layout

```
MIC3 Container Format
═══════════════════════════════════════════════════════════

HEADER (48 bytes fixed)
├─ Bytes  0-3:   Magic "MIC3"
├─ Bytes  4-7:   Format version (uint32 LE) = 1
├─ Bytes  8-11:  Full-res width (uint32 LE)
├─ Bytes 12-15:  Full-res height (uint32 LE)
├─ Bytes 16-19:  Tile width (uint32 LE)
├─ Bytes 20-23:  Tile height (uint32 LE)
├─ Bytes 24-25:  Channels (uint16 LE): 1=grey, 3=RGB
├─ Byte  26:     Bits per sample (uint8): 8 or 16
├─ Byte  27:     Flags:
│                  bit0 = spatial delta (always 1)
│                  bit1 = color transform (YCoCg-R)
│                  bit2 = greyscale bypass (direct uint16)
├─ Bytes 28-29:  Pyramid level count (uint16 LE)
├─ Bytes 30-31:  Reserved (zero)
├─ Bytes 32-39:  Total tile count across all levels (uint64 LE)
├─ Bytes 40-47:  Reserved (zero)

LEVEL DESCRIPTORS (N × 20 bytes)
├─ Per level:
│  ├─ Bytes 0-3:   Level width (uint32 LE)
│  ├─ Bytes 4-7:   Level height (uint32 LE)
│  ├─ Bytes 8-11:  Tiles in X (uint32 LE)
│  ├─ Bytes 12-15: Tiles in Y (uint32 LE)
│  └─ Bytes 16-19: First tile index in global tile table (uint32 LE)

TILE OFFSET TABLE (M × 16 bytes)
├─ Per tile:
│  ├─ Bytes 0-7:   Byte offset from data section start (uint64 LE)
│  └─ Bytes 8-15:  Compressed length in bytes (uint64 LE)

DATA SECTION
└─ Concatenated compressed tile blobs (in tile index order)
```

### Tile Indexing

Tiles are indexed in row-major order within each level:

```
Global tile index = level.firstTileIndex + (tileY * level.tilesX) + tileX
```

For a 100,000 × 80,000 image with 256×256 tiles:
- Level 0: 391 × 313 = 122,383 tiles
- Level 1: 196 × 157 = 30,772 tiles
- Level 2: 98 × 79 = 7,742 tiles
- Level 3: 49 × 40 = 1,960 tiles
- Level 4: 25 × 20 = 500 tiles
- Level 5: 13 × 10 = 130 tiles
- Level 6: 7 × 5 = 35 tiles
- Level 7: 4 × 3 = 12 tiles
- Level 8: 2 × 2 = 4 tiles
- **Total**: ~163,538 tiles
- **Tile table overhead**: 163,538 × 16 = ~2.6 MB (negligible vs multi-GB image)

### Edge Tiles

Tiles at the right and bottom edges may be smaller than the standard tile size. The compressor pads them to full tile dimensions with zeros and records the actual image dimensions in the level descriptor. The decompressor returns only the valid region.

Alternative: store actual tile dimensions per-tile. This adds 4 bytes per tile entry but avoids wasted compression on padding. Recommendation: **pad with zeros** — the padding compresses to nearly nothing via RLE, and it simplifies the pipeline (all tiles are the same dimensions).

---

## 5. API Design

### Core Types

```go
// WSIHeader holds metadata for a MIC3 WSI file.
type WSIHeader struct {
    Width        int      // Full-resolution width
    Height       int      // Full-resolution height
    TileWidth    int      // Tile width (typically 256 or 512)
    TileHeight   int      // Tile height
    Channels     int      // 1 (greyscale) or 3 (RGB)
    BitsPerSample int     // 8 or 16
    ColorTransform bool   // true if YCoCg-R was applied
    Levels       []WSILevel
}

// WSILevel describes one pyramid level.
type WSILevel struct {
    Width         int  // Level width in pixels
    Height        int  // Level height in pixels
    TilesX        int  // Number of tile columns
    TilesY        int  // Number of tile rows
    FirstTileIdx  int  // Index of first tile in global table
}

// WSIOptions configures WSI compression.
type WSIOptions struct {
    TileWidth      int  // Default: 256
    TileHeight     int  // Default: 256
    PyramidLevels  int  // 0 = auto (ceil(log2(max(w,h)/tileSize)) + 1)
    ColorTransform bool // Default: true for RGB
    Workers        int  // Parallel goroutines. 0 = runtime.GOMAXPROCS
}
```

### Compression API

```go
// CompressWSI compresses a full-resolution RGB or greyscale image into MIC3 format.
// pixels is row-major, channel-interleaved (RGBRGBRGB...) for RGB.
// Returns the complete MIC3 file as bytes.
func CompressWSI(pixels []byte, width, height, channels, bitsPerSample int,
    opts WSIOptions) ([]byte, error)

// CompressWSITile compresses a single tile. Used internally and for streaming.
func CompressWSITile(tile []byte, tileWidth, tileHeight, channels,
    bitsPerSample int, colorTransform bool) ([]byte, error)
```

### Decompression API

```go
// ReadWSIHeader parses the MIC3 header without decompressing any tiles.
func ReadWSIHeader(data []byte) (*WSIHeader, error)

// DecompressTile decompresses a single tile at the given pyramid level.
// Returns channel-interleaved pixel data (RGBRGB... for RGB).
func DecompressTile(data []byte, level, tileX, tileY int) ([]byte, error)

// DecompressRegion decompresses a rectangular region at a specific level.
// Internally reads all tiles that overlap the region and crops.
func DecompressRegion(data []byte, level, x, y, w, h int) ([]byte, error)
```

### Streaming API (for very large images)

```go
// WSIWriter writes MIC3 files incrementally, tile by tile.
// Usage: create writer, write tiles in any order, close to finalize.
type WSIWriter struct { ... }

func NewWSIWriter(w io.WriteSeeker, width, height, channels,
    bitsPerSample int, opts WSIOptions) (*WSIWriter, error)
func (w *WSIWriter) WriteTile(level, tileX, tileY int, pixels []byte) error
func (w *WSIWriter) Close() error

// WSIReader provides random access to tiles in a MIC3 file.
type WSIReader struct { ... }

func NewWSIReader(r io.ReaderAt, size int64) (*WSIReader, error)
func (r *WSIReader) Header() WSIHeader
func (r *WSIReader) ReadTile(level, tileX, tileY int) ([]byte, error)
func (r *WSIReader) ReadRegion(level, x, y, w, h int) ([]byte, error)
```

---

## 6. Implementation Plan

### Phase 1: YCoCg-R Color Transform

**New files:**
- `ycocgr.go` — Forward and inverse YCoCg-R transform
- `ycocgr_test.go` — Roundtrip tests, edge cases, known-value tests

**Functions:**
```go
// YCoCgRForward converts interleaved RGB pixels to separate Y, Co, Cg planes.
// Input: []byte of length width*height*3 (RGBRGB...)
// Output: three []uint16 planes (Y: 0-255, Co/Cg: 0-510 via ZigZag)
func YCoCgRForward(rgb []byte, width, height int) (y, co, cg []uint16)

// YCoCgRInverse converts Y, Co, Cg planes back to interleaved RGB.
func YCoCgRInverse(y, co, cg []uint16, width, height int) []byte
```

**Validation:**
- Roundtrip: `YCoCgRInverse(YCoCgRForward(rgb)) == rgb` for all inputs
- Edge cases: all-black, all-white, pure red/green/blue, random
- Known values: verify specific pixel transforms

**Estimated effort:** Small. Pure arithmetic, no dependencies.

### Phase 2: Per-Tile Compression Pipeline

**New files:**
- `wsicompress.go` — Tile compression/decompression using existing pipeline

**Functions:**
```go
// compressRGBTile compresses a single RGB tile:
//   RGB → YCoCg-R → 3 planes → each: Delta+RLE+FSE → combined blob
func compressRGBTile(rgb []byte, width, height int) ([]byte, error)

// decompressRGBTile reverses the process.
func decompressRGBTile(blob []byte, width, height int) ([]byte, error)

// compressGreyTile compresses a single greyscale tile (existing pipeline).
func compressGreyTile(pixels []uint16, width, height int, maxValue uint16) ([]byte, error)
```

**Key detail:** Each plane is compressed by calling `CompressSingleFrame` with the tile dimensions and per-plane maxValue. The three compressed blobs are concatenated with length headers into a single tile blob.

**Validation:**
- Roundtrip on synthetic tiles (gradient, noise, constant)
- Verify losslessness for all 8-bit RGB inputs
- Benchmark single-tile throughput

**Estimated effort:** Medium. Plumbing existing components, new tile blob format.

### Phase 3: MIC3 Container Format

**New files:**
- `wsiformat.go` — MIC3 header, level descriptors, tile offset table I/O

**Functions:**
```go
func WriteMIC3Header(w io.Writer, hdr WSIHeader) error
func ReadMIC3Header(data []byte) (*WSIHeader, []WSITileEntry, int, error)

type WSITileEntry struct {
    Offset uint64
    Length uint64
}
```

**Validation:**
- Header roundtrip tests
- Tile table addressing: verify correct tile index calculation
- Truncated file detection

**Estimated effort:** Small-medium. Similar pattern to existing MIC2 code.

### Phase 4: Pyramid Generation

**New files:**
- `wsipyramid.go` — Downsampling for pyramid level generation

**Functions:**
```go
// Downsample2x reduces image dimensions by half using box filter (2x2 average).
// For RGB: averages each channel independently.
func Downsample2xRGB(src []byte, width, height int) ([]byte, int, int)
func Downsample2xGrey(src []uint16, width, height int) ([]uint16, int, int)
```

**Design decision:** Generate all pyramid levels during compression. The downsampling cost is negligible compared to entropy coding. Callers who already have pre-built pyramids (e.g., from a scanner) can use the streaming API to write tiles directly.

**Validation:**
- Dimension correctness (odd sizes, 1-pixel edge cases)
- Visual quality (box filter is appropriate for lossless intermediate pyramids)

**Estimated effort:** Small. Pure arithmetic.

### Phase 5: Full WSI Compress/Decompress

**Modified files:**
- `wsicompress.go` — Add top-level `CompressWSI`, `DecompressTile`, `DecompressRegion`

**Orchestration for `CompressWSI`:**
1. Generate pyramid levels (Downsample2x iteratively)
2. For each level, tile the image into TileWidth × TileHeight chunks
3. Compress each tile (with color transform for RGB)
4. Build tile offset table
5. Write MIC3 header + level descriptors + tile table + compressed blobs

**Orchestration for `DecompressTile`:**
1. Parse MIC3 header
2. Look up tile entry in offset table: `globalIdx = level.firstTileIdx + tileY*tilesX + tileX`
3. Extract compressed blob at `dataOffset + entry.offset`
4. Decompress tile (with inverse color transform for RGB)
5. Crop if edge tile

**Orchestration for `DecompressRegion`:**
1. Determine which tiles overlap `(x, y, w, h)` at the given level
2. Decompress those tiles
3. Assemble and crop to the exact requested region

**Validation:**
- Full roundtrip on synthetic 1024×1024 RGB image with 256×256 tiles
- Pyramid level correctness
- Edge tile handling
- Region extraction accuracy

**Estimated effort:** Medium-large. Main integration work.

### Phase 6: Parallel Compression/Decompression

**Modified files:**
- `wsicompress.go` — Add worker pool for tile compression

**Approach:**
```go
// Worker pool pattern
type tileResult struct {
    index int
    blob  []byte
    err   error
}

func compressTilesParallel(tiles [][]byte, width, height, channels int,
    colorTransform bool, workers int) ([][]byte, error) {

    ch := make(chan tileResult, len(tiles))
    sem := make(chan struct{}, workers)

    for i, tile := range tiles {
        sem <- struct{}{}
        go func(idx int, t []byte) {
            defer func() { <-sem }()
            blob, err := compressRGBTile(t, width, height)
            ch <- tileResult{idx, blob, err}
        }(i, tile)
    }
    // ... collect results
}
```

**Thread safety:** The existing pipeline uses per-invocation `ScratchU16` and `DeltaRleCompressU16` structs — no shared mutable state. Each goroutine creates its own instances. This is already safe.

**Validation:**
- Correctness: parallel result == sequential result
- Benchmark: scaling with GOMAXPROCS

**Estimated effort:** Small-medium. The pipeline is already goroutine-safe by design.

### Phase 7: Streaming API

**New files:**
- `wsireader.go` — `WSIReader` with `io.ReaderAt`-based random access
- `wsiwriter.go` — `WSIWriter` with `io.WriteSeeker`-based incremental write

**WSIWriter strategy:**
1. Write placeholder header + tile table on construction
2. Append compressed tile blobs as `WriteTile` is called, recording offsets
3. On `Close()`, seek back and overwrite the tile table with actual offsets

**WSIReader strategy:**
1. Read and parse header + tile table on construction
2. Each `ReadTile` call seeks to the tile's offset and reads `length` bytes
3. Decompress in-place

**Validation:**
- Roundtrip: write with WSIWriter, read with WSIReader
- Verify random access (read tiles in arbitrary order)
- Large file simulation (>4 GB offsets)

**Estimated effort:** Medium. io.ReaderAt/WriteSeeker plumbing.

---

## 7. File Manifest

### New Files

| File | Phase | Purpose |
|---|---|---|
| `ycocgr.go` | 1 | YCoCg-R forward/inverse color transform |
| `ycocgr_test.go` | 1 | Color transform roundtrip and edge case tests |
| `wsiformat.go` | 3 | MIC3 container: header, level descriptors, tile table I/O |
| `wsicompress.go` | 2,5,6 | Tile compression, full WSI compress/decompress, parallelism |
| `wsicompress_test.go` | 2,5 | WSI roundtrip tests with synthetic and real data |
| `wsipyramid.go` | 4 | Pyramid generation via 2x downsampling |
| `wsireader.go` | 7 | Streaming random-access reader (io.ReaderAt) |
| `wsiwriter.go` | 7 | Streaming incremental writer (io.WriteSeeker) |

### Modified Files

| File | Change |
|---|---|
| `CLAUDE.md` | Add WSI section with new files, test commands, architecture notes |
| None others | The WSI extension builds entirely on top of existing pipeline functions |

### No Modifications Needed

The following files are used as-is, called from the WSI layer:
- `deltacompressu16.go` / `deltarlecompressu16.go` — spatial prediction
- `rlecompressu16.go` / `rledecompressu16.go` — run-length encoding
- `fsecompressu16.go` / `fsedecompressu16.go` — entropy coding
- `fseu16.go` — shared FSE types and constants
- `multiframecompress.go` — `CompressSingleFrame` / `DecompressSingleFrame` called per-tile per-plane

---

## 8. Testing Strategy

### Unit Tests (per phase)

```bash
# Phase 1: Color transform
go test -run TestYCoCgR -v

# Phase 2: Single tile compression
go test -run TestWSITileCompress -v

# Phase 3: MIC3 container format
go test -run TestMIC3Header -v

# Phase 4: Pyramid generation
go test -run TestDownsample -v

# Phase 5: Full WSI roundtrip
go test -run TestWSICompress -v

# Phase 6: Parallel compression
go test -run TestWSIParallel -v

# Phase 7: Streaming API
go test -run TestWSIStreaming -v
```

### Benchmark Suite

```bash
# Single tile throughput (256x256 RGB)
go test -bench BenchmarkWSITileCompress -benchtime=100x

# Full WSI throughput (parallel, 4096x4096 synthetic)
go test -bench BenchmarkWSICompress -benchtime=10x

# Decompression throughput
go test -bench BenchmarkWSITileDecompress -benchtime=100x

# Region read performance
go test -bench BenchmarkWSIRegionRead -benchtime=100x
```

### Integration Tests

- Real DICOM WSI test (similar to `TestMultiFrameTomoCompress`):
  - Read WSI DICOM or TIFF file
  - Compress to MIC3
  - Decompress random tiles, verify losslessness
  - Compare compression ratio vs JPEG2000 lossless
  - Report throughput metrics

### Test Data

- **Synthetic**: Gradient + noise RGB images (256×256, 1024×1024, 4096×4096)
- **Simulated H&E**: Pink/purple regions with noise to mimic tissue staining
- **Real WSI** (optional, gitignored): Small SVS/TIFF crops from public datasets (TCGA, Camelyon)

---

## 9. Performance Projections

### Single-Tile Throughput (256×256 RGB, single-threaded)

Based on existing codec benchmarks on 16-bit greyscale:

| Stage | Est. Time | Notes |
|---|---|---|
| YCoCg-R transform | ~5 µs | 196K pixels × 3 ops, cache-local |
| Delta encode (per plane) | ~10 µs | Same as current 256×256 greyscale |
| RLE encode (per plane) | ~5 µs | Background tiles: near-instant |
| FSE encode (per plane) | ~15 µs | Histogram + normalization + encoding |
| **Total encode** | **~100 µs** | 3 planes × 30 µs + overhead |
| **Total decode** | **~50 µs** | FSE decode + RLE decode + delta decode + inverse color |

At ~100 µs per tile encode:
- Single-thread: ~10,000 tiles/sec = **640 MB/s raw** (256×256×3 bytes per tile)
- 8 threads: ~80,000 tiles/sec = **~5 GB/s raw**

At ~50 µs per tile decode:
- Single-thread: ~20,000 tiles/sec = **1.3 GB/s raw**
- 8 threads: ~160,000 tiles/sec = **~10 GB/s raw**

### Compression Ratio Projections

| Image Type | Estimated Ratio | Reasoning |
|---|---|---|
| Background tiles | 50-100x | White → Y=255/Co=0/Cg=0, extreme RLE |
| Dense tissue (H&E) | 3-5x | Smooth staining gradients, good delta prediction |
| Mixed slide (typical) | 4-8x | 30-50% background, 50-70% tissue |
| Cytology (sparse cells) | 8-15x | Mostly background with isolated cells |

Compared to JPEG2000 lossless (typically 2-3x on tissue), MIC3 should be **competitive on ratio and dramatically faster** on throughput.

---

## 10. DICOM Integration Path

### Short-term: Standalone Format

MIC3 as a standalone file format, convertible to/from DICOM WSI:

```
openslide / DICOM WSI → read tiles → compress to MIC3 → store
MIC3 → read tile → decompress → serve to viewer
```

### Medium-term: DICOM Transfer Syntax

Register a private or standard DICOM Transfer Syntax UID for MIC3-compressed pixel data:

- Each DICOM frame = one compressed tile blob
- Frame offset table in DICOM encapsulated pixel data provides random access
- Total Pixel Matrix Columns/Rows + tile dimensions in DICOM attributes
- Pyramid levels as separate instances or concatenation (per DICOM convention)

### Long-term: OpenSlide Backend

Implement an OpenSlide vendor backend for MIC3, allowing existing pathology viewers (QuPath, ASAP, etc.) to read MIC3 files natively.

---

## 11. Open Questions

1. **Tile size default**: 256×256 is the DICOM WSI standard, but 512×512 gives better delta prediction (more context). Should we default to 512×512 and document that 256×256 is also supported?

2. **16-bit RGB**: Some fluorescence microscopy WSI is 16-bit per channel. The pipeline handles this natively (YCoCg-R output fits in uint16 for 16-bit input too, since Co/Cg max = 2×65535 which doesn't fit uint16). For 16-bit, we may need uint32 intermediates or skip the color transform and compress planes independently. Defer to Phase 8?

3. **Alpha channel**: Some WSI formats include an alpha/mask channel (e.g., label/macro images). Support 4-channel RGBA? Or treat alpha as a separate greyscale plane?

4. **Compression level**: Should we offer a speed/ratio tradeoff? E.g., skip delta coding for fastest encode (RLE+FSE only), or bump tableLog for best ratio? The current codec has a single "mode" which is a strength (simplicity), but WSI users may want to tune.

5. **Checksum**: Should each tile blob include a CRC32 for integrity verification? Adds 4 bytes per tile but catches corruption. Medical data integrity is paramount.

---

## 12. Dependencies

### Required (already in go.mod)
- `github.com/suyashkumar/dicom` — For DICOM WSI test data reading

### Optional (for testing/benchmarking)
- OpenSlide Go bindings — For reading SVS/TIFF WSI files in integration tests
- JPEG2000 Go library — For compression ratio comparison benchmarks

### No New Dependencies for Core Implementation
The WSI extension requires zero new dependencies. YCoCg-R is pure arithmetic, the container format is custom binary I/O, and parallelism uses only the standard library (`sync`, `runtime`).
