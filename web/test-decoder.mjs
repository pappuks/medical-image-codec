// End-to-end test for the MIC JavaScript decoder.
// Run: node test-decoder.mjs
//
// This reads .mic files from testdata/ and corresponding .raw files,
// decodes the .mic with the JS decoder, and compares pixel-by-pixel.

import { MICDecoder } from './mic-decoder.js';
import { readFileSync, existsSync } from 'fs';

function testMicFile(micPath, rawPath, name) {
  console.log(`\nTesting ${name}...`);

  const micData = readFileSync(micPath);
  const micBytes = new Uint8Array(micData);

  // Read expected dimensions from .mic header
  const dv = new DataView(micBytes.buffer);
  const width = dv.getUint32(4, true);
  const height = dv.getUint32(8, true);
  console.log(`  Dimensions: ${width}x${height}`);
  console.log(`  Compressed: ${micBytes.length} bytes`);

  // Decode
  const t0 = performance.now();
  let result;
  try {
    result = MICDecoder.decodeFile(micBytes);
  } catch (e) {
    console.error(`  FAIL: decode error: ${e.message}`);
    console.error(e.stack);
    return false;
  }
  const t1 = performance.now();
  console.log(`  Decode time: ${(t1 - t0).toFixed(2)} ms`);
  console.log(`  Output: ${result.pixels.length} pixels (${result.width}x${result.height})`);

  if (result.width !== width || result.height !== height) {
    console.error(`  FAIL: dimension mismatch`);
    return false;
  }

  // Compare against raw file if available
  if (existsSync(rawPath)) {
    const rawData = readFileSync(rawPath);
    // Go's ReadBinaryFile reads only as many pixels as the file contains and
    // zero-fills the remainder. Mirror that: allocate width*height uint16s (zeros),
    // then copy in however many bytes the file actually has.
    const expectedBytes = width * height * 2;
    const alignedBuf = new ArrayBuffer(expectedBytes);
    new Uint8Array(alignedBuf).set(rawData.subarray(0, Math.min(rawData.length, expectedBytes)));
    const expected = new Uint16Array(alignedBuf);

    if (expected.length !== result.pixels.length) {
      console.error(`  FAIL: length mismatch: expected ${expected.length}, got ${result.pixels.length}`);
      return false;
    }

    for (let i = 0; i < expected.length; i++) {
      if (expected[i] !== result.pixels[i]) {
        console.error(`  FAIL: pixel mismatch at index ${i}: expected ${expected[i]}, got ${result.pixels[i]}`);
        // Show a few surrounding values for debugging
        const start = Math.max(0, i - 3);
        const end = Math.min(expected.length, i + 4);
        console.error(`    Expected[${start}..${end}]: ${Array.from(expected.slice(start, end))}`);
        console.error(`    Got     [${start}..${end}]: ${Array.from(result.pixels.slice(start, end))}`);
        return false;
      }
    }
    console.log(`  PASS: all ${expected.length} pixels match`);
    return true;
  } else {
    console.log(`  SKIP verification: ${rawPath} not found`);
    console.log(`  (Decode succeeded without errors)`);
    return true;
  }
}

// Run tests
const tests = [
  { mic: 'testdata/MR.mic', raw: '../testdata/MR_256_256_image.bin', name: 'MR (256x256)' },
  { mic: 'testdata/CT.mic', raw: '../testdata/CT_512_512_image.bin', name: 'CT (512x512)' },
  { mic: 'testdata/CR.mic', raw: '../testdata/CR_1760_2140_image.bin', name: 'CR (1760x2140)' },
  { mic: 'testdata/MG1.mic', raw: '../testdata/MG_image_bin2.bin', name: 'MG1 (1996x2457)' },
  { mic: 'testdata/MG2.mic', raw: '../testdata/MG_Image_2_frame.bin', name: 'MG2 (1996x2457)' },
  { mic: 'testdata/MG3.mic', raw: '../testdata/MG1.RAW', name: 'MG3 (3064x4774)' },
];

let passed = 0;
let failed = 0;

for (const t of tests) {
  if (!existsSync(t.mic)) {
    console.log(`\nSkipping ${t.name}: ${t.mic} not found`);
    console.log(`  Run 'go run ./cmd/mic-compress/ -testdata' first`);
    continue;
  }
  if (testMicFile(t.mic, t.raw, t.name)) {
    passed++;
  } else {
    failed++;
  }
}

console.log(`\n${'='.repeat(50)}`);
console.log(`Results: ${passed} passed, ${failed} failed`);

if (failed > 0) {
  process.exit(1);
}
