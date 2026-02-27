# MIC Web Decoder

Browser-based lossless decoder for 16-bit medical images compressed with the Delta+RLE+FSE pipeline.

## Quick Start

### 1. Generate test data

```bash
# From the repository root:
go run ./cmd/mic-compress/ -testdata
```

This compresses the test images into `.mic` files under `web/testdata/`.

### 2. Build the WASM decoder (optional)

```bash
GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js
```

### 3. Serve the web directory

```bash
cd web
npx serve .
# or: python3 -m http.server 3000
```

Open `http://localhost:3000` in your browser. You can:
- Click test image buttons (MR, CT, CR, MG1-3) to load pre-compressed samples
- Drag & drop any `.mic` file
- Switch between JavaScript and Go WASM decoders
- Adjust Window/Level for diagnostic viewing

### 4. Run the verification tests

```bash
cd web
node test-decoder.mjs
```

This decompresses each `.mic` file with the JavaScript decoder and compares pixel-by-pixel against the original raw images. All 6 test images (MR, CT, CR, MG1, MG2, MG3) must produce identical output to the Go implementation.

## Two Decoder Implementations

### JavaScript (`mic-decoder.js`)

A pure ES module with zero dependencies. Works in any modern browser that supports `BigInt` (all major browsers since 2020).

**Pros:** Small bundle (~15 KB), no build step, no WASM overhead, easy to debug and modify.

**Performance:** Decodes a 5 megapixel mammogram in ~150 ms, a 14.6 megapixel image in ~1.2 s (Node.js / V8).

### Go WASM (`mic-decoder.wasm`)

The existing Go codec compiled to WebAssembly. Guaranteed format-compatible since it runs the same code.

**Pros:** Exact same implementation as the Go codec, potentially faster for very large images.

**Cons:** ~2.5 MB binary (includes Go runtime), requires `wasm_exec.js` glue file.

**Build:** `GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/`

## API Reference

### JavaScript decoder

```js
import { MICDecoder } from './mic-decoder.js';

// Decode a .mic container file
const { pixels, width, height } = MICDecoder.decodeFile(micFileBytes);
// pixels: Uint16Array, width/height: number

// Decode raw FSE-compressed bytes (when you know the dimensions)
const pixels = MICDecoder.decode(compressedBytes, width, height);

// Individual pipeline stages (for debugging or alternative pipelines)
const rleSymbols = MICDecoder.fseDecompress(compressedBytes);         // FSE only
const deltaValues = MICDecoder.rleDecompress(rleData);                // RLE only
const pixels = MICDecoder.deltaDecompress(deltaData, width, height);  // Delta only
const pixels = MICDecoder.deltaRleDecompress(rleSymbols, w, h);      // Delta+RLE combined
```

### WASM decoder

```js
import { loadMICWasm } from './mic-decoder-wasm.js';

const decoder = await loadMICWasm();            // or loadMICWasm('path/to/mic-decoder.wasm')
const { pixels, width, height } = decoder.decodeFile(micFileBytes);
const pixels = decoder.decode(compressedBytes, width, height);
```

The WASM decoder exposes the same `decode`, `decodeFile`, `fseDecompress`, and `deltaDecompress` methods.

## .mic Container Format

```
Offset  Size  Description
0       4     Magic bytes: "MIC1" (0x4D 0x49 0x43 0x31)
4       4     Image width (uint32, little-endian)
8       4     Image height (uint32, little-endian)
12      4     Pipeline type (uint32 LE): 1 = Delta+RLE+FSE
16      4     Compressed data length in bytes (uint32 LE)
20      N     FSE-compressed data (Delta+RLE encoded 16-bit pixels)
```

## Compressing Images

Use the `mic-compress` CLI tool to create `.mic` files from raw 16-bit image data:

```bash
# Build
go build -o mic-compress ./cmd/mic-compress/

# Compress a single image (raw uint16 LE pixel data)
./mic-compress -input image.bin -width 512 -height 512 -output image.mic

# Generate all test .mic files
./mic-compress -testdata
```

Input files must be raw little-endian uint16 pixel data with no headers (width * height * 2 bytes).

## Compression Pipeline

```
Original 16-bit pixels (width x height)
    -> Delta Encoding (spatial prediction: average of top + left neighbors)
    -> RLE (run-length encoding with same/different run distinction)
    -> FSE (Finite State Entropy / tANS)
    -> Compressed byte stream (.mic file)
```

Decompression reverses the pipeline: FSE -> RLE -> Delta.

## Test Results

| Image | Size | Pixels | Ratio | JS Decode Time |
|-------|------|--------|-------|----------------|
| MR (256x256) | 128 KB | 65K | 2.35:1 | ~25 ms |
| CT (512x512) | 512 KB | 262K | 2.24:1 | ~90 ms |
| CR (1760x2140) | 7.2 MB | 3.8M | 3.63:1 | ~330 ms |
| MG1 (1996x2457) | 9.4 MB | 4.9M | 8.57:1 | ~150 ms |
| MG2 (1996x2457) | 9.4 MB | 4.9M | 8.55:1 | ~150 ms |
| MG3 (3064x4774) | 27.3 MB | 14.6M | 2.24:1 | ~1200 ms |

Times measured in Node.js v22 (V8). Browser performance may vary.

## File Structure

```
web/
├── mic-decoder.js         # Pure JS decoder (ES module, zero deps)
├── mic-decoder-wasm.js    # WASM loader (same API as JS decoder)
├── index.html             # Demo page with drag-and-drop + W/L controls
├── test-decoder.mjs       # Node.js end-to-end verification test
├── .gitignore             # Ignores generated WASM, wasm_exec.js, testdata/
├── mic-decoder.wasm       # [generated] Go WASM binary
├── wasm_exec.js           # [generated] Go WASM runtime glue
└── testdata/              # [generated] .mic test files
    ├── MR.mic
    ├── CT.mic
    ├── CR.mic
    ├── MG1.mic
    ├── MG2.mic
    └── MG3.mic

cmd/
├── mic-compress/main.go   # CLI: compress raw images to .mic
└── mic-wasm/main.go       # Go WASM entry point
```
