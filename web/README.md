# MIC Web Decoder

Browser-based lossless decoder for 16-bit medical images (DICOM) compressed with the Delta+RLE+FSE pipeline. Part of the [MIC (Medical Image Codec)](../) project.

Provides two decoder implementations — a pure JavaScript ES module and a Go WebAssembly build — both producing pixel-identical output to the native Go codec. Supports both single-frame (MIC1) and multi-frame (MIC2) files, with a built-in movie player for multi-frame image playback.

## Table of Contents

- [Quick Start](#quick-start)
- [Decoder Implementations](#decoder-implementations)
- [API Reference](#api-reference)
- [Integration Guide](#integration-guide)
- [.mic Container Format](#mic-container-format)
- [Compressing Images](#compressing-images)
- [Decompression Pipeline Details](#decompression-pipeline-details)
- [Browser Compatibility](#browser-compatibility)
- [Test Results](#test-results)
- [Troubleshooting](#troubleshooting)
- [File Structure](#file-structure)

## Quick Start

### 1. Generate test data

```bash
# From the repository root:
go run ./cmd/mic-compress/ -testdata
```

This compresses the test images (MR, CT, CR, MG1-3, plus the DX_HAND and PET1 new-modality representatives) into single-frame `.mic` files and multi-frame DICOM images (MG_TOMO) into MIC2 `.mic` files under `web/testdata/`. Each grayscale image is emitted three times — 1-state (default), 4-state (`_4s.mic`), and 8-state (`_8s.mic`) FSE — plus PICS parallel-strip variants (`_pics4.mic`, `_pics8.mic`, `_pics4_8s.mic`, `_pics8_8s.mic`) so the JS decoder and benchmark can be exercised against every entropy-coder variant. It also writes `manifest.json` (a raw-pixel FNV-1a32 checksum per image) that the PACS dashboard uses to verify every codec's decoded output.

For the [PACS dashboard](#pacs-web-viewer-benchmark) to compare against the reference codecs *live in the browser*, also generate their compressed files (requires the native libs libopenjph / libcharls / libjxl) and vendor their WASM decoders once:

```bash
# From the repo root:
go run -tags cgo_ojph ./cmd/mic-refgen               # HTJ2K/.jph, JPEG-LS/.jls, JPEG-XL/.jxl + refcodecs-manifest.json
cd web && npm install && bash scripts/vendor-wasm.sh # vendor OpenJPH + CharLS WASM into web/vendor/
```

`mic-refgen` round-trip-verifies every reference file natively before writing it, so a broken reference file can never reach `web/testdata/`.

### 2. Build the WASM decoder (optional)

```bash
GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js
```

> Note: The `wasm_exec.js` location varies by Go version. If `misc/wasm/` doesn't exist, try `lib/wasm/`.

### 3. Serve the web directory

```bash
cd web
npx serve .
# or: python3 -m http.server 3000
# or: php -S localhost:3000
```

Open `http://localhost:3000` in your browser. The demo page lets you:
- Click test image buttons (MR, CT, CR, MG1-3, MG Tomo) to load pre-compressed samples
- Drag & drop any `.mic` file (MIC1 or MIC2) onto the page
- Switch between JavaScript and Go WASM decoders via dropdown
- Adjust Window/Level sliders for diagnostic viewing of 16-bit dynamic range
- **Multi-frame playback**: When a MIC2 file is loaded, movie controls appear — play/pause, prev/next frame, frame slider, FPS control, loop toggle. Keyboard shortcuts: Space (play/pause), Left/Right arrows (prev/next frame)

### 4. Run the pixel-perfect verification tests

```bash
cd web
node test-decoder.mjs
```

Output:

```
Testing MR (256x256)...     PASS: all 65536 pixels match
Testing CT (512x512)...     PASS: all 262144 pixels match
Testing CR (1760x2140)...   PASS: all 3766400 pixels match
Testing MG1 (1996x2457)...  PASS: all 4904172 pixels match
Testing MG2 (1996x2457)...  PASS: all 4904172 pixels match
Testing MG3 (3064x4774)...  PASS: all 14627536 pixels match
Results: 6 passed, 0 failed
```

Each `.mic` file is decompressed by the JavaScript decoder and compared pixel-by-pixel against the original raw image. Every pixel must match the Go implementation exactly.

## Decoder Implementations

### JavaScript (`mic-decoder.js`)

A pure ES module with zero dependencies. Works in any modern browser or Node.js without a build step.

| Property | Value |
|----------|-------|
| Bundle size | ~54 KB source, ~19 KB minified (`mic-decoder.min.js`) |
| Dependencies | None |
| Build step | None required |
| Browser requirement | `BigInt` support (all major browsers since 2020) |
| Module format | ES module (`import`/`export`) |

The JavaScript decoder uses `BigInt` for the 64-bit reverse bit reader required by FSE/ANS decoding. This is the only non-trivial portability requirement — the FSE encoder writes bits backwards into a 64-bit accumulator, and the decoder must replicate the same bit-exact arithmetic. `BigInt` provides correct 64-bit unsigned semantics without the precision loss of `Number` (which is limited to 53-bit mantissa).

All other data passes through standard `Uint8Array`, `Uint16Array`, and `Int32Array` typed arrays for performance.

### Go WASM (`mic-decoder.wasm`)

The existing Go codec compiled to WebAssembly. Guaranteed format-compatible since it runs the exact same decompression code as the native Go binary.

| Property | Value |
|----------|-------|
| Binary size | ~2.5 MB (includes Go runtime + GC) |
| Dependencies | `wasm_exec.js` (Go WASM glue, ~17 KB) |
| Build step | `GOOS=js GOARCH=wasm go build` |
| API surface | Same as JavaScript decoder |

The WASM decoder is useful when you need absolute certainty of format compatibility or when decoding very large images where the Go runtime's optimized code may outperform the BigInt-based JavaScript implementation.

### When to use which

| Scenario | Recommended |
|----------|-------------|
| Web app with bundler (Webpack, Vite, etc.) | **JavaScript** — tree-shakeable, zero overhead |
| Minimal page weight / mobile | **JavaScript** — 19 KB vs 2.5 MB |
| Maximum decode speed on very large images | **WASM** — no BigInt overhead |
| Offline / service worker use | **JavaScript** — no async WASM loading |
| Must guarantee format compatibility | **WASM** — same Go code |
| Server-side Node.js | **JavaScript** — simpler, no WASM instantiation |

## API Reference

### JavaScript decoder

```js
import { MICDecoder } from './mic-decoder.js';
```

#### `MICDecoder.decodeFile(fileBytes)`

Decode a `.mic` container file. Reads dimensions and pipeline type from the header.

```js
const fileBytes = new Uint8Array(arrayBuffer);
const { pixels, width, height } = MICDecoder.decodeFile(fileBytes);
// pixels: Uint16Array of width*height elements (0-65535 range)
// width:  number
// height: number
```

#### `MICDecoder.decode(compressedBytes, width, height)`

Decode raw FSE-compressed bytes when you already know the image dimensions. This is the entry point to use when you have compressed data without the `.mic` container header.

```js
const compressedBytes = new Uint8Array(fseData);
const pixels = MICDecoder.decode(compressedBytes, 512, 512);
// pixels: Uint16Array of 262144 elements
```

#### `MICDecoder.decodeFile(fileBytes)` — multi-frame

When the file is MIC2, `decodeFile` returns additional metadata and decodes the first frame:

```js
const result = MICDecoder.decodeFile(fileBytes);
if (result.isMIC2) {
  // result.pixels:     Uint16Array (first frame)
  // result.width:      number
  // result.height:     number
  // result.isMIC2:     true
  // result.frameCount: number
  // result.temporal:   boolean
}
```

#### `MICDecoder.parseMIC2Header(fileBytes)`

Parse MIC2 header without decompressing any frames:

```js
const hdr = MICDecoder.parseMIC2Header(fileBytes);
// hdr.width, hdr.height, hdr.frameCount, hdr.temporal, hdr.entries
```

#### `MICDecoder.decodeMIC2Frame(fileBytes, frameIndex, prevPixels, hdr)`

Decode a single frame from a MIC2 file:

```js
// Independent mode: prevPixels can be null
const pixels = MICDecoder.decodeMIC2Frame(fileBytes, 0, null, hdr);

// Temporal mode (frame > 0): pass previous frame's pixels
const frame1 = MICDecoder.decodeMIC2Frame(fileBytes, 1, frame0Pixels, hdr);
```

#### Individual pipeline stages

For debugging, testing, or building alternative pipelines, each decompression stage is exposed separately:

```js
// FSE decompression only (bytes -> uint16 RLE symbols)
const rleSymbols = MICDecoder.fseDecompress(compressedBytes);

// RLE decompression only (uint16 RLE stream -> uint16 delta values)
// Input must start with maxValue word followed by length words
const deltaValues = MICDecoder.rleDecompress(rleData);

// Delta decompression only (uint16 delta stream -> original pixels)
// Input must start with maxValue word
const pixels = MICDecoder.deltaDecompress(deltaData, width, height);

// Combined Delta+RLE in one pass (the standard DeltaRle format from Go)
// This is what decode() uses internally after FSE decompression
const pixels = MICDecoder.deltaRleDecompress(rleSymbols, width, height);
```

### WASM decoder

```js
import { loadMICWasm } from './mic-decoder-wasm.js';

// Load WASM (async — downloads and compiles the .wasm binary)
const decoder = await loadMICWasm();
// or with a custom path:
const decoder = await loadMICWasm('/assets/mic-decoder.wasm');

// Same API as the JavaScript decoder
const { pixels, width, height } = decoder.decodeFile(micFileBytes);
const pixels = decoder.decode(compressedBytes, width, height);
const rleSymbols = decoder.fseDecompress(compressedBytes);
const pixels = decoder.deltaDecompress(deltaData, width, height);

// Version string
console.log(decoder.version());
// "MIC WASM Decoder v2.0 (Delta+RLE+FSE, 16-bit, MIC1+MIC2)"

// Multi-frame support
const hdr = decoder.parseMIC2Header(fileBytes);
const frame0 = decoder.decodeFrame(fileBytes, 0);
```

The WASM loader eagerly loads `wasm_exec.js` via a `<script>` tag injection and fires a `mic-wasm-ready` CustomEvent on `document` when the Go runtime has initialized.

### Error handling

Both decoders throw on corrupt or malformed input:

```js
try {
  const result = MICDecoder.decodeFile(data);
} catch (e) {
  // e.message examples:
  //   "Invalid .mic file (bad magic: 0x00000000)"
  //   "corrupt stream: too short"
  //   "corrupt stream: did not find end of stream"
  //   "corrupted input (position != 0)"
  //   "unexpected EOF in bitstream"
}
```

## Integration Guide

### Using in a web application

```html
<script type="module">
  import { MICDecoder } from './mic-decoder.js';

  // Fetch a .mic file and decode it
  const resp = await fetch('/images/scan.mic');
  const data = new Uint8Array(await resp.arrayBuffer());
  const { pixels, width, height } = MICDecoder.decodeFile(data);

  // Render to canvas with window/level
  const canvas = document.getElementById('viewer');
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext('2d');
  const imgData = ctx.createImageData(width, height);

  const windowWidth = 4096;
  const windowCenter = 2048;
  const low = windowCenter - windowWidth / 2;
  const range = windowWidth;

  for (let i = 0; i < pixels.length; i++) {
    const gray = Math.max(0, Math.min(255, ((pixels[i] - low) / range) * 255)) | 0;
    imgData.data[i * 4] = gray;
    imgData.data[i * 4 + 1] = gray;
    imgData.data[i * 4 + 2] = gray;
    imgData.data[i * 4 + 3] = 255;
  }
  ctx.putImageData(imgData, 0, 0);
</script>
```

### Using in Node.js

```js
import { MICDecoder } from './mic-decoder.js';
import { readFileSync, writeFileSync } from 'fs';

const micData = new Uint8Array(readFileSync('scan.mic'));
const { pixels, width, height } = MICDecoder.decodeFile(micData);

// pixels is a Uint16Array — write raw for further processing
const buf = Buffer.from(pixels.buffer, pixels.byteOffset, pixels.byteLength);
writeFileSync('output.raw', buf);

console.log(`Decoded ${width}x${height} (${pixels.length} pixels)`);
```

### Using with a bundler (Webpack / Vite / Rollup)

`mic-decoder.js` is a standard ES module with no dependencies. It works with any bundler out of the box:

```js
// In your application code:
import { MICDecoder } from 'mic-decoder';  // or relative path

export function decodeMedicalImage(arrayBuffer) {
  return MICDecoder.decodeFile(new Uint8Array(arrayBuffer));
}
```

For the WASM decoder with Vite, you may need to configure the WASM file as a static asset:

```js
// vite.config.js
export default {
  assetsInclude: ['**/*.wasm'],
};
```

### Using with Web Workers

For large images, offload decoding to a Web Worker to keep the UI responsive:

```js
// worker.js
import { MICDecoder } from './mic-decoder.js';

self.onmessage = (e) => {
  const { data, width, height } = e.data;
  const pixels = MICDecoder.decode(new Uint8Array(data), width, height);
  self.postMessage({ pixels: pixels.buffer }, [pixels.buffer]);
};

// main.js
const worker = new Worker('./worker.js', { type: 'module' });
worker.postMessage({ data: compressedArrayBuffer, width: 512, height: 512 });
worker.onmessage = (e) => {
  const pixels = new Uint16Array(e.data.pixels);
  renderToCanvas(pixels, 512, 512);
};
```

## Minification

Pre-minified versions of the three JavaScript files are checked into the repository and are what `index.html` imports. After editing any source file, regenerate them:

```bash
cd web

# Core decoder
npx terser mic-decoder.js --compress --mangle --output mic-decoder.min.js

# Parallel decoder — patch both import paths after minifying
npx terser mic-decoder-parallel.js --compress --mangle --output mic-decoder-parallel.min.js
sed -i 's|./mic-decoder.js|./mic-decoder.min.js|g' mic-decoder-parallel.min.js
sed -i 's|./mic-worker.js|./mic-worker.min.js|g' mic-decoder-parallel.min.js

# Worker — patch import path after minifying
npx terser mic-worker.js --compress --mangle --output mic-worker.min.js
sed -i 's|./mic-decoder.js|./mic-decoder.min.js|g' mic-worker.min.js
```

| File | Source | Minified |
|------|--------|----------|
| `mic-decoder.js` | ~54 KB | ~19 KB |
| `mic-decoder-parallel.js` | ~9.5 KB | ~3.3 KB |
| `mic-worker.js` | ~4 KB | ~1 KB |
| **Total** | **~68 KB** | **~23 KB** |

> The `sed` step is required because terser does not rewrite string literals — the `import … from './mic-decoder.js'` and `new URL('./mic-worker.js', …)` references inside the source files must be updated to point to the `.min.js` counterparts.

## .mic Container Formats

### MIC1 — Single Frame

A minimal container that wraps FSE-compressed data with image dimensions.

```
Offset  Size  Field                Description
──────  ────  ───────────────────  ──────────────────────────────────────────
0       4     Magic                "MIC1" (0x4D 0x49 0x43 0x31, little-endian)
4       4     Width                Image width in pixels (uint32 LE)
8       4     Height               Image height in pixels (uint32 LE)
12      4     Pipeline type        1 = Delta+RLE+FSE (uint32 LE)
16      4     Compressed length    Byte count of the FSE payload (uint32 LE)
20      N     Compressed data      FSE-compressed Delta+RLE encoded pixels
```

Total header size: 20 bytes. Maximum image size: 2^32 x 2^32 pixels. Maximum compressed payload: ~4 GB.

The pipeline type field is reserved for future expansion (e.g., 2 = Delta+RLE+Huffman, 3 = Delta+ZigZag+RLE+FSE).

### MIC2 — Multi-Frame

Container for multi-frame images (e.g., Breast Tomosynthesis). Supports independent and temporal compression modes.

```
Offset    Size    Field                Description
────────  ──────  ───────────────────  ──────────────────────────────────────────
0         4       Magic                "MIC2" (0x4D 0x49 0x43 0x32, little-endian)
4         4       Width                Frame width in pixels (uint32 LE)
8         4       Height               Frame height in pixels (uint32 LE)
12        4       Frame count          Number of frames (uint32 LE)
16        1       Pipeline flags       bit0=spatial (0x01, always set)
                                       bit1=temporal (0x02)
17        3       Reserved             Zero
20        N*8     Frame offset table   N × {offset_u32, length_u32}
                                       Offsets relative to data section start
20+N*8    ...     Compressed frames    Concatenated compressed frame blobs
```

**Independent mode** (flags=0x01): Each frame blob is a self-contained Delta+RLE+FSE payload (same as MIC1 payload). Any frame can be decoded independently.

**Temporal mode** (flags=0x03): Frame 0 uses spatial Delta+RLE+FSE. Frames 1+ contain ZigZag-encoded inter-frame residuals compressed with RLE+FSE only. Decoding frame N requires sequentially decoding frames 0 through N.

### Parsing in JavaScript

```js
const dv = new DataView(fileBytes.buffer, fileBytes.byteOffset);
const magic      = dv.getUint32(0, true);   // 0x3143494D = "MIC1"
const width      = dv.getUint32(4, true);
const height     = dv.getUint32(8, true);
const pipeline   = dv.getUint32(12, true);  // 1
const compLen    = dv.getUint32(16, true);
const compressed = fileBytes.subarray(20, 20 + compLen);
```

### Parsing in Go

```go
magic   := string(data[0:4])                          // "MIC1"
width   := binary.LittleEndian.Uint32(data[4:8])
height  := binary.LittleEndian.Uint32(data[8:12])
pipeline := binary.LittleEndian.Uint32(data[12:16])   // 1
compLen := binary.LittleEndian.Uint32(data[16:20])
compressed := data[20 : 20+compLen]
```

## Compressing Images

### Using the `mic-compress` CLI

```bash
# Build
go build -o mic-compress ./cmd/mic-compress/

# Compress a single raw image (MIC1)
./mic-compress -input image.bin -width 512 -height 512 -output image.mic

# Compress a DICOM file (single frame → MIC1, multi-frame → MIC2)
./mic-compress -dicom scan.dcm -output scan.mic

# Compress with temporal inter-frame prediction (multi-frame only)
./mic-compress -dicom tomo.dcm -output tomo.mic -temporal

# Generate all test .mic files at once (MIC1 + MIC2)
./mic-compress -testdata
```

Raw input files must be little-endian uint16 pixel data with no headers. Expected file size: `width * height * 2` bytes. DICOM input is parsed automatically using the `suyashkumar/dicom` library.

### Compressing programmatically in Go

```go
import "mic"

// shortData is []uint16 of width*height pixels
// maxValue is the maximum pixel value in the image
var drc mic.DeltaRleCompressU16
rleData, err := drc.Compress(shortData, width, height, maxValue)

var s mic.ScratchU16
compressed, err := mic.FSECompressU16(rleData, &s)
// compressed is []byte — write to .mic container or transmit directly
```

### Extracting pixels from DICOM files

The test suite includes a DICOM parser example. To extract raw pixels from a DICOM file for compression:

```go
import (
    "github.com/suyashkumar/dicom"
    "github.com/suyashkumar/dicom/pkg/tag"
)

dataset, _ := dicom.ParseFile("scan.dcm", nil)
pixelDataElement, _ := dataset.FindElementByTag(tag.PixelData)
pixelDataInfo := dicom.MustGetPixelDataInfo(pixelDataElement.Value)
frame := pixelDataInfo.Frames[0]
nativeFrame, _ := frame.GetNativeFrame()

pixels := make([]uint16, nativeFrame.Cols*nativeFrame.Rows)
var maxVal uint16
for j, sample := range nativeFrame.Data {
    pixels[j] = uint16(sample[0])
    if pixels[j] > maxVal { maxVal = pixels[j] }
}
// Now compress: drc.Compress(pixels, nativeFrame.Cols, nativeFrame.Rows, maxVal)
```

## Decompression Pipeline Details

The full decompression pipeline reverses three encoding stages. The browser decoder executes them in sequence:

```
FSE compressed bytes (Uint8Array)
    │
    ▼  FSEDecompressor.decompress()
    │    1. readNCount()  — parse symbol frequency table from header
    │    2. buildDtable()  — construct decode state machine
    │    3. decompressStream() — decode bitstream via state transitions
    │
RLE-encoded uint16 symbols (Uint16Array)
    │
    ▼  deltaRleDecompress()  [combined RLE + Delta in one pass]
    │    1. Read RLE maxValue header (word 0 = delimiterForOverflow)
    │    2. RLE-decode first symbol → actual image maxValue
    │    3. Derive pixelDepth, deltaThreshold, delimiterForOverflow
    │    4. For each pixel: RLE-decode → delta-reverse → output
    │
Original 16-bit pixels (Uint16Array)
```

### Stage 1: FSE (Finite State Entropy / tANS) Decompression

FSE is an asymmetric numeral system (ANS) — a modern entropy coder that achieves near-optimal compression like arithmetic coding but with table-based O(1) encode/decode per symbol.

**Stream-format auto-detection.** `MICDecoder.decompress` looks at the first two bytes of the FSE payload and dispatches to the matching decode loop:

| Magic prefix | Decoder | Notes |
|--------------|---------|-------|
| `[0xFF, 0x84]` | 8-state (`_decompressStream8State`) | Eight independent state chains A..H, default for C/Go encoder. |
| `[0xFF, 0x04]` | 4-state (`_decompressStream4State`) | Four chains A..D, practical JS sweet spot on V8 (see [Benchmarks](#benchmark)). |
| `[0xFF, 0x02]` | 2-state (`_decompressStream2State`) | Two chains A, B. |
| (no magic)     | 1-state (`_decompressStream`)       | Single chain — legacy / fallback. |

All four variants share the same `readNCount` + `buildDtable` front-end; only the inner state-machine count differs. Each variant produces bit-identical output for the same compressed bytes — they are alternative *encoders*, not alternative *formats*.

**Header parsing (`readNCount`):** The first bytes of the compressed stream contain a variable-length encoded frequency table:
- Bits 0-3: `tableLog - 5` (table size = 2^tableLog states)
- Remaining bits: Normalized symbol probabilities encoded with a Huffman-like variable-length scheme
- Symbols with probability 0 are run-length encoded (groups of 24)
- Probability -1 represents a "low probability" symbol (count = 1)

**Decode table construction (`buildDtable`):** From the normalized frequencies, a state transition table is built:
1. Low-probability symbols are placed at the high end of the table
2. Other symbols are spread using the step function: `step = (tableSize >> 1) + (tableSize >> 3) + 3`
3. For each table entry: `{ symbol, nbBits, newState }` — the symbol to output, bits to read from the stream, and the next state

**Bitstream decoding:** The FSE bitstream is read **in reverse** (from the last byte towards the first). This is fundamental to how ANS works — the encoder writes bits backwards so the decoder can read them forwards through a state machine:
1. Initialize the 64-bit bit buffer from the last 8 bytes of input
2. The alignment marker (highest set bit of the final byte) synchronizes the reader
3. Read `tableLog` bits for *each* initial state (1, 2, 4, or 8 of them depending on the variant), with `br.fill()` calls between groups so the 64-bit buffer never empties
4. Main loop: look up `decTable[state_k]` → emit symbol, read `nbBits` bits, compute next state as `newState + lowBits`; the multi-state variants interleave 2/4/8 such lookups per iteration so a wide out-of-order engine can run them in parallel
5. Two code paths: fast (no symbol has probability > 50%) and safe (some symbols emit 0 bits, requiring bounds checks)

### Stage 2: RLE (Run-Length Encoding) Decompression

After FSE decoding, the uint16 symbol stream contains RLE-encoded data. The RLE scheme distinguishes two run types based on a `midCount` threshold derived from the pixel bit depth:

- **"Same" run** (count <= midCount): The next word is a value repeated `count` times
- **"Different" run** (count > midCount): The next `count - midCount` words are distinct values
- The fast path caches the recurring value for same-runs, avoiding input reads for repeated pixels (very common in medical images after delta encoding)

### Stage 3: Delta Prediction Reversal

Each pixel was encoded as the difference from a predicted value. The prediction depends on position:

| Position | Prediction | Formula |
|----------|-----------|---------|
| Corner (0,0) | None | `pixel = delta - threshold` |
| First row (y=0) | Left neighbor | `pixel = left + delta - threshold` |
| First column (x=0) | Top neighbor | `pixel = top + delta - threshold` |
| Interior | Average of left + top | `pixel = ((left + top) >> 1) + delta - threshold` |

When the delta exceeds the threshold (the difference is too large to represent), an overflow delimiter signals that the next word is the raw pixel value.

### JavaScript-specific implementation notes

- **64-bit bit buffer:** JavaScript's `Number` type only has 53 bits of mantissa precision, insufficient for the FSE bit reader which requires exact 64-bit unsigned arithmetic. The decoder uses `BigInt` with explicit masking (`& 0xFFFFFFFFFFFFFFFFn`) to emulate uint64 overflow behavior.

- **Signed/unsigned conversion:** JavaScript's bitwise operators work on signed 32-bit integers. When reading 4 bytes where the high byte has bit 7 set, the OR chain produces a negative value. The decoder applies `>>> 0` to the entire expression before converting to `BigInt`, ensuring correct unsigned interpretation.

- **Typed arrays:** All bulk data uses `Uint16Array` and `Uint8Array` for cache-friendly access patterns. The decode table uses a plain `Array` of objects (V8 optimizes this well for monomorphic access patterns in tight loops).

## Browser Compatibility

| Feature | Required | Supported Since |
|---------|----------|-----------------|
| `BigInt` | Yes (JS decoder) | Chrome 67, Firefox 68, Safari 14, Edge 79 |
| ES Modules (`import`/`export`) | Yes | Chrome 61, Firefox 60, Safari 11, Edge 79 |
| `Uint8Array` / `Uint16Array` | Yes | All modern browsers |
| `DataView` | Yes (container parsing) | All modern browsers |
| `WebAssembly` | WASM decoder only | Chrome 57, Firefox 52, Safari 11, Edge 16 |
| `WebAssembly.instantiateStreaming` | WASM decoder only | Chrome 61, Firefox 58, Safari 15 |

The JavaScript decoder works in all browsers released since 2020. The WASM decoder requires `WebAssembly.instantiateStreaming` for efficient loading.

### Node.js

The JavaScript decoder works in Node.js 10.4+ (BigInt support). The test suite uses Node.js ES module syntax (`import`/`export`), which requires Node.js 12+ or the `--experimental-modules` flag.

## Test Results

All images verified pixel-perfect against the Go implementation:

| Image | Modality | Dimensions | Original | Compressed | Ratio | JS Decode |
|-------|----------|-----------|----------|------------|-------|-----------|
| MR | Brain MRI | 256x256 | 128 KB | 55 KB | 2.35:1 | ~3 ms |
| CT | Computed Tomography | 512x512 | 512 KB | 229 KB | 2.24:1 | ~12 ms |
| CR | Computed Radiography | 1760x2140 | 7.2 MB | 1.95 MB | 3.69:1 | ~148 ms |
| MG1 | Mammography | 1996x2457 | 9.4 MB | 1.07 MB | 8.79:1 | ~65 ms |
| MG2 | Mammography | 1996x2457 | 9.4 MB | 1.07 MB | 8.77:1 | ~70 ms |
| MG3 | Mammography | 3064x4774 | 27.3 MB | 11.9 MB | 2.29:1 | ~553 ms |

Decode times are the median single-threaded (1-state) decode on Apple M4 Pro, Node.js v22 (V8); browser performance varies by engine and device. Mammography images (MG1/MG2) achieve the best compression ratios because large areas of the detector are unexposed (uniform background), which delta encoding + RLE compress extremely well.

### Benchmark — Apple M4 Pro (Node.js v22.16, 20 iterations + 3 warm-up)

Run with `node bench-decoder.mjs`. Reports median decode time, throughput (decompressed MB/s), and megapixels/s. Numbers below are from the 14-core M4 Pro (10 P-cores + 4 E-cores) — the same reference platform the [IEEE paper](../paper/mic-paper-v9-ieee-tmi.pdf) uses for the C and Go benchmarks.

#### Single-threaded (pure JS decoder) — 4-state vs 8-state FSE

The JS decoder auto-detects the FSE state count from the 2-byte magic prefix and dispatches to the matching loop (see [Stage 1](#stage-1-fse-finite-state-entropy--tans-decompression)). All four variants produce bit-identical output.

| Image | Dimensions | Ratio | 1-state ms / MB/s | 4-state ms / MB/s | 8-state ms / MB/s |
|-------|------------|-------|-------------------|-------------------|-------------------|
| MR  | 256×256   | 2.35× | 3.0 / 42  | **2.9 / 43**  | 2.7 / 46  |
| CT  | 512×512   | 2.24× | 12.2 / 41 | **12.3 / 41** | 13.1 / 38 |
| CR  | 1760×2140 | 3.69× | 134.7 / 53 | **137.2 / 52** | 165.5 / 43 |
| MG1 | 1996×2457 | 8.79× | 70.9 / 132 | **70.4 / 133** | 80.0 / 117 |
| MG2 | 1996×2457 | 8.77× | 73.1 / 128 | **74.2 / 126** | 79.0 / 118 |
| MG3 | 3064×4774 | 2.29× | 552.4 / 51 | **560.8 / 50** | 607.1 / 46 |
| DX_HAND | 1410×1480 | 2.24× | 86.3 / 46 | **82.6 / 48** | 96.1 / 41 |
| PET1 | 256×256   | 2.74× | 2.2 / 57 | **2.1 / 60** | 2.5 / 50 |

DX_HAND (digital radiography) and PET1 (PET) are the two new-modality representatives added to the corpus; their `.mic` files are produced by `mic-compress -testdata` alongside the others.

**Why 4-state is the JS default.** In the C/Go decoder, 8 independent state chains expose enough instruction-level parallelism for a wide out-of-order engine (or AVX-512 gather) to retire ~8 lookups per cycle. V8's single-thread scheduler saturates around 4 chains, so the extra 4 chains in the 8-state variant add per-symbol bookkeeping (state initialisation, tail handling) without shortening the critical path — the 8-state column is 6–21% slower than 4-state on the images large enough to time reliably (MR, under 3 ms, is within timing noise). The 1- and 4-state variants are within a few percent of each other; 4-state is preferred because it lets the JS decoder read C-encoded streams without transcoding, while staying close to 1-state speed.

#### Parallel worker_threads (PICS strips, SharedArrayBuffer, M4 Pro 14 cores)

PICS files encode an image as independent strips, enabling parallel decoding across `worker_threads`. The bench sweeps worker counts {1, 2, 4, 8, 14}; the table below reports the best wall-time for each variant.

| Image | strips | 4-state strips ms / MB/s | 8-state strips ms / MB/s |
|-------|--------|--------------------------|--------------------------|
| MR  256×256   | 4 | 0.9 / 134 (4 workers)      | **0.9 / 135** (14 workers) |
| CT  512×512   | 4 | **4.1 / 123** (8 workers)  | 4.5 / 112 (4 workers)    |
| CR  1760×2140 | 8 | **22.9 / 313** (8 workers) | 28.0 / 257 (8 workers)   |
| MG1 1996×2457 | 8 | 17.1 / 547 (14 workers)    | **16.7 / 559** (8 workers) |
| DX_HAND 1410×1480 | 8 | **17.1 / 233** (8 workers) | 17.8 / 224 (14 workers) |

Workers beyond the strip count are capped to the strip count by the pool. For large images (CR, MG1) the 8-strip layout scales to roughly 4.5–6.7× speedup and pushes throughput to **230–560 MB/s** — well into real-time territory for diagnostic workloads. The 4- and 8-state strip variants are within a few percent of each other; 4-state is the practical default, with 8-state occasionally ahead on the longer-strip images (MG1) where V8 has time to amortise the wider state-init cost over more symbols.

## PACS Web Viewer Benchmark

Two front-ends share one model (`pacs-model.mjs` — network profiles, image set,
codec registry, and timing math), so the Node console report and the browser
dashboard can never drift apart. Both model the thing a radiologist actually
experiences in a browser-based PACS viewer: click a study, wait for pixels. That
wait has two stages — network transfer of the compressed bytes, then in-browser
decompression — and both are measured together instead of decode alone.

### Interactive browser dashboard — `pacs-dashboard.html`

The dashboard decodes **every codec live in a real browser** and simulates all
eight network profiles. It must be *served* (not opened as `file://`) because it
needs COOP/COEP headers for `SharedArrayBuffer` (PICS Web Workers), streams
multi-MB WASM, and fetches the testdata blobs:

```bash
# From the repo root — generate MIC + reference-codec test files first:
bash testdata/multiframe/fetch-cine-sources.sh      # cine source DICOMs (one-time; needs .venv)
go run ./cmd/mic-compress -testdata                 # MIC/.mic + manifest.json (no cgo)
go run -tags cgo_ojph ./cmd/mic-refgen              # HTJ2K/.jph, JPEG-LS/.jls, JPEG-XL/.jxl
                                                    #   (needs libopenjph/libcharls/libjxl)
cd web && npm install && bash scripts/vendor-wasm.sh # vendor the WASM decoders (one-time)
python3 serve.py 8080                                # serve with COOP/COEP
# open http://localhost:8080/pacs-dashboard.html
```

Beyond the single-frame images, the dashboard has a **cine / multi-frame** section
over seven studies (five public-domain fetched datasets — cardiac cine MR, XA
angiography, nuclear-medicine gated heart, enhanced MR & CT — plus two reused from
the whole-image demo corpus). Every frame is emitted as an independent
single-frame image, so the full codec matrix runs per frame and the section
reports full cine-loop decode time and frames/s. The `fetch-cine-sources.sh` step
above downloads the first five source DICOMs (and transcodes the JPEG-Lossless XA
sample to uncompressed); `mic-compress`/`mic-refgen` then emit the per-frame files
for all seven datasets. See
[docs/benchmarks.md §12](../docs/benchmarks.md#12-browser-pacs-web-viewer-benchmark).

Which codecs decode **live** vs. **informational**:

| Codec | Browser decode | How |
|-------|----------------|-----|
| MIC 1/4/8-state | ✅ live | pure-JS `MICDecoder` |
| MIC-WASM (Go, 4-state) | ✅ live | the Go codec compiled to WASM (`cmd/mic-wasm` → `mic-decoder.wasm`), decoding the same 4-state stream as MIC-4state — a direct pure-JS vs Go/WASM comparison |
| MIC-C-WASM (4/8-state) | ✅ live | the pure-C codec (`ojph/mic_decompress_c.c`) compiled to WASM with Emscripten (`web/wasm-c/build.sh` → `web/vendor/mic-c/`, ~20 KB, no runtime), decoding the same 4/8-state streams |
| MIC-PICS 4/8 strips | ✅ live | `createPICSDecoder()` real Web Worker pool (SharedArrayBuffer) |
| MIC-C-WASM-PICS (8 strips) | ✅ live | the pure-C PICS decoder (`ojph/mic_parallel.c`, pthreads) compiled to WASM with Emscripten pthreads (`web/wasm-c/build-pics.sh` → `web/vendor/mic-pics/`); runs in a Web Worker and fans out to pthread workers — the C scheduler + C inner decoder |
| HTJ2K | ✅ live | vendored OpenJPH WASM (`@cornerstonejs/codec-openjph`) |
| JPEG-LS | ✅ live | vendored CharLS WASM (`@cornerstonejs/codec-charls`) |
| JPEG-XL | ℹ️ informational | see below |

> **The three MIC WASM/JS builds decode identical bytes**, so the dashboard is a
> clean head-to-head. A representative result (CR, 7.18 MB, this machine):
> pure-JS MIC-4state ≈ 140 ms, **Go→WASM ≈ 330 ms** (Go runtime + GC overhead —
> *slower* than JS), **C→WASM ≈ 17 ms** (~8× faster than JS, ~19× faster than
> Go/WASM). The C→WASM binary is ~20 KB vs the Go build's ~2.9 MB. All three are
> pixel-verified bit-exact.
>
> Parallel decode, same story (CR): the pure-JS **MIC-PICS-8** worker pool ≈ 24 ms,
> while **MIC-C-WASM-PICS-8** — the C pthreads scheduler compiled to WASM, fanning
> out to pthread Web Workers — decodes in ≈ 4 ms (the fastest path here; ~6× the
> JS pool, ~4× single-thread C, confirming the pthread parallelism), from a 30 KB
> wasm. Bit-exact.
>
> The WASM variants need a build step and are skipped (row omitted, rest of the
> run unaffected) if their artifacts are absent:
> - **MIC-WASM (Go)** — Quick Start step 2:
>   `GOOS=js GOARCH=wasm go build -o web/mic-decoder.wasm ./cmd/mic-wasm/` + copy `wasm_exec.js`.
> - **MIC-C-WASM** (single-thread 4/8-state) — needs Emscripten
>   (`brew install emscripten` or emsdk), then `bash web/wasm-c/build.sh`
>   (outputs the committed `web/vendor/mic-c/`).
> - **MIC-C-WASM-PICS** (pthreads) — Emscripten, then `bash web/wasm-c/build-pics.sh`
>   (outputs the committed `web/vendor/mic-pics/`). Needs the page served with
>   COOP/COEP (`serve.py` sets them) so `SharedArrayBuffer`/pthreads work.

**JPEG-XL is informational, and here's why** (recorded so it isn't re-litigated):
the only mature browser JXL decoder, `@jsquash/jxl`, exposes a `decode()` that
returns an 8-bit RGBA `ImageData`. That cannot represent lossless 16-bit
grayscale medical pixels, so a live JXL row would be a lie about correctness.
`scripts/probe-codecs.mjs` confirms this. JXL therefore shows its **real**
compressed size (from the actual `.jxl` file `mic-refgen` produces and
round-trip-verifies natively) plus a native-C reference decode throughput,
clearly flagged. HTJ2K and JPEG-LS both round-trip 16-bit losslessly through
their WASM decoders (also verified by the probe), so they decode live.

The dashboard shows per-image tables (size, ratio, decode ms, time-to-display
per network profile), a study-level simulation, an optional pixel-correctness
pass (decoded pixels are checksummed against `manifest.json` — every live codec
must match bit-for-bit), and charts (time-to-display stacked as network vs.
decode, decode-time per codec, compression ratio per codec).

### Headless CI runner — Playwright

`tests/pacs-bench.spec.mjs` drives the dashboard in headless Chromium, asserts
every live codec pixel-verifies, and writes the full result JSON to `results/`:

```bash
cd web && npx playwright install chromium   # one-time
npx playwright test                          # auto-launches serve.py, runs the bench
```

### Node console report — `bench-pacs-viewer.mjs`

```bash
cd web
node bench-pacs-viewer.mjs                          # 15 iterations per codec/image
node bench-pacs-viewer.mjs --iterations 30           # more iterations
node bench-pacs-viewer.mjs --json results.json       # also write machine-readable results
```

The Node script measures MIC + PICS live (PICS via `worker_threads` as a
stand-in for Web Workers) across all eight network profiles, and lists HTJ2K /
JPEG-LS / JPEG-XL as informational rows (real `.jph/.jls/.jxl` sizes when
`mic-refgen` has run, else the paper's ratio table; native-C reference decode
times, flagged with `*`). Unlike the dashboard, the Node script does **not**
load the WASM decoders — "live in the browser" is the dashboard's job.

The headline finding is the same in both: on fast networks (Gigabit/hospital
LAN/Wi-Fi) decode speed is the bottleneck and codec/threading choices matter
most; on slow networks (home broadband, cellular, 3G, satellite) network
transfer dominates by many multiples, so compression ratio matters far more
than decode speed.

## Troubleshooting

### "corrupt stream: too short" or "did not find end of stream"

The input data is too small or truncated. Ensure the complete `.mic` file was transferred (check `Content-Length` in network tab).

### "unexpected EOF in bitstream"

The FSE bitstream was consumed before all symbols were decoded. This indicates data corruption — verify the file was not modified after compression.

### "Invalid .mic file (bad magic: 0x...)"

The file does not start with a "MIC1" or "MIC2" header. Ensure you're loading a `.mic` file and not a raw `.bin` or `.dcm`.

### WASM decoder shows "(WASM unavailable)"

- Ensure `wasm_exec.js` and `mic-decoder.wasm` are in the same directory as the HTML page
- Check the browser console for CORS errors (WASM loading requires proper CORS headers)
- Verify the WASM was built with the same Go version as the `wasm_exec.js`

### Test images not loading in demo

Run `go run ./cmd/mic-compress/ -testdata` from the repository root to generate the `.mic` test files. They are not checked into git (listed in `web/.gitignore`).

### Large images decode slowly

- The JavaScript decoder processes ~10-30M pixels/s depending on compression ratio
- For images > 10M pixels, consider using a Web Worker to avoid blocking the UI
- The WASM decoder may be faster for very large images due to native integer arithmetic

## File Structure

```
web/
├── mic-decoder.js             # Pure JS decoder — source (~54 KB)
├── mic-decoder.min.js         # Pure JS decoder — minified (~19 KB, used by index.html)
├── mic-decoder-parallel.js    # Parallel PICS/RGB decoder — source (~9.5 KB)
├── mic-decoder-parallel.min.js# Parallel PICS/RGB decoder — minified (~3.3 KB, used by index.html)
├── mic-worker.js              # Web Worker for parallel decoding — source (~4 KB)
├── mic-worker.min.js          # Web Worker — minified (~1 KB, used by mic-decoder-parallel.min.js)
├── mic-decoder-wasm.js        # WASM loader wrapper (same API as JS decoder)
├── index.html                 # Demo: drag-and-drop, W/L controls, movie player
├── test-decoder.mjs           # Node.js pixel-perfect verification test
├── bench-decoder.mjs          # Node.js throughput benchmark (single-threaded + worker_threads)
├── bench-worker.mjs           # worker_threads worker used by bench-decoder.mjs
├── bench-pacs-viewer.mjs      # End-to-end PACS viewer simulation (network transfer + decode, see below)
├── README.md                  # This file
├── .gitignore                 # Ignores generated files below
├── mic-decoder.wasm           # [generated] Go WASM binary (~2.5 MB)
├── wasm_exec.js               # [generated] Go WASM runtime glue (~17 KB)
└── testdata/                  # [generated] compressed test images
    ├── MR.mic             #   MR brain MRI, 256x256 (MIC1)
    ├── CT.mic             #   CT scan, 512x512 (MIC1)
    ├── CR.mic             #   Computed radiography, 1760x2140 (MIC1)
    ├── MG1.mic            #   Mammogram, 1996x2457 (MIC1)
    ├── MG2.mic            #   Mammogram, 1996x2457 (MIC1)
    ├── MG3.mic            #   Mammogram, 3064x4774 (MIC1)
    ├── DX_HAND.mic        #   Digital radiography, 1410x1480 (MIC1)
    ├── PET1.mic           #   PET, 256x256 (MIC1)
    └── MG_TOMO.mic        #   Breast Tomosynthesis, 2457x1890, 69 frames (MIC2)

cmd/
├── mic-compress/main.go   # CLI: compress raw/DICOM images to .mic (MIC1/MIC2)
└── mic-wasm/main.go       # Go WASM entry point (syscall/js bindings)
```
