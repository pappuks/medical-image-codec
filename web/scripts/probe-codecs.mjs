// probe-codecs.mjs — Ground-truth probe for the three reference-codec WASM
// decoders, run in Node against the real reference files produced by
// `mic-refgen`. Verifies (a) the actual decode API surface for each package
// (so the browser adapters aren't written from guesses) and (b) whether each
// decoder reproduces the original 16-bit grayscale pixels bit-exactly.
//
// This is the JXL "probe-then-decide" gate (design §6.4): JPEG-XL becomes a
// live-decoded browser codec only if it round-trips 16-bit losslessly here.
//
// Run:  node scripts/probe-codecs.mjs
import { createRequire } from 'node:module';
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { fnv1a32Hex } from '../pacs-model.mjs';

const require = createRequire(import.meta.url);
const __dir = dirname(fileURLToPath(import.meta.url));
const testdata = resolve(__dir, '../testdata');
const manifest = JSON.parse(readFileSync(resolve(testdata, 'manifest.json'), 'utf8'));

const IMG = 'MR'; // small 16-bit (11-bit) image; enough to validate correctness
const want = manifest.images[IMG].checksum;

function checksumOfU16(u16) {
  const bytes = new Uint8Array(u16.buffer, u16.byteOffset, u16.byteLength);
  return fnv1a32Hex(bytes);
}

// Interpret a decoded byte buffer + frameInfo into a Uint16Array of samples.
function toU16(decodedBytes, frameInfo) {
  const { bitsPerSample } = frameInfo;
  if (bitsPerSample > 8) {
    return new Uint16Array(decodedBytes.buffer, decodedBytes.byteOffset, decodedBytes.byteLength / 2);
  }
  // 8-bit output — widen (won't match 16-bit source, but report it)
  return Uint16Array.from(decodedBytes);
}

async function probeCornerstone(pkg, decoderClass, file, label) {
  console.log(`\n=== ${label} (${pkg}) ===`);
  if (!existsSync(resolve(testdata, file))) { console.log(`  SKIP: ${file} missing (run mic-refgen)`); return; }
  const factory = require(pkg);
  const Module = await factory();
  console.log('  embind exports:', Object.keys(Module).filter(k => /Decoder|Encoder/.test(k)).join(', ') || '(none found by name)');
  const Decoder = Module[decoderClass];
  if (!Decoder) { console.log(`  FAIL: ${decoderClass} not found on Module`); return; }
  const decoder = new Decoder();
  const encoded = new Uint8Array(readFileSync(resolve(testdata, file)));
  const inBuf = decoder.getEncodedBuffer(encoded.length);
  inBuf.set(encoded);
  decoder.decode();
  const fi = decoder.getFrameInfo();
  console.log('  frameInfo:', JSON.stringify(fi));
  const decoded = decoder.getDecodedBuffer();
  const u16 = toU16(decoded, fi);
  const got = checksumOfU16(u16);
  console.log(`  checksum got=${got} want=${want}  => ${got === want ? 'LOSSLESS MATCH ✔' : 'MISMATCH �’'}`);
  if (typeof decoder.delete === 'function') decoder.delete();
}

async function probeJXL(file) {
  console.log(`\n=== JPEG-XL (@jsquash/jxl) ===`);
  if (!existsSync(resolve(testdata, file))) { console.log(`  SKIP: ${file} missing`); return; }
  try {
    const mod = await import('@jsquash/jxl/decode.js');
    const encoded = new Uint8Array(readFileSync(resolve(testdata, file)));
    const result = await mod.default(encoded.buffer);
    console.log('  decode() returned:', result?.constructor?.name,
      `width=${result?.width} height=${result?.height} data=${result?.data?.constructor?.name}(${result?.data?.length})`);
    // ImageData is RGBA8; check whether R channel alone could carry 16-bit (it can't).
    if (result?.data && result.data.length === result.width * result.height * 4) {
      console.log('  => 8-bit RGBA output. Cannot represent lossless 16-bit grayscale. LIVE DECODE: NO (fall back to informational).');
    } else {
      console.log('  => unexpected output shape; needs manual inspection.');
    }
  } catch (e) {
    console.log('  JXL probe error:', e.message);
    console.log('  => LIVE DECODE: NO (fall back to informational).');
  }
}

await probeCornerstone('@cornerstonejs/codec-openjph', 'HTJ2KDecoder', `${IMG}.jph`, 'HTJ2K');
await probeCornerstone('@cornerstonejs/codec-charls', 'JpegLSDecoder', `${IMG}.jls`, 'JPEG-LS');
await probeJXL(`${IMG}.jxl`);
console.log('\nProbe complete.');
