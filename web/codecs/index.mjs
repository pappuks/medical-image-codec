// index.mjs — Build a codec adapter for a CODEC_REGISTRY entry (pacs-model.mjs).
// Central dispatch so the dashboard never branches on codec kind itself.

import { makeMICAdapter, makePICSAdapter } from './mic-adapters.mjs';
import { makeMICWasmAdapter } from './mic-wasm-adapter.mjs';
import { makeMICCWasmAdapter } from './mic-c-wasm-adapter.mjs';
import { makeMICPicsCWasmAdapter } from './mic-pics-c-wasm-adapter.mjs';
import { makeHTJ2KAdapter } from './htj2k-adapter.mjs';
import { makeJPEGLSAdapter } from './jpegls-adapter.mjs';
import { makeJXLAdapter } from './jxl-adapter.mjs';

// registryEntry: one element of CODEC_REGISTRY.
// Returns an adapter implementing codec-interface.md.
export function makeAdapter(entry) {
  switch (entry.kind) {
    case 'mic':
      return makeMICAdapter(entry);
    case 'micwasm':
      return makeMICWasmAdapter(entry);
    case 'miccwasm':
      return makeMICCWasmAdapter(entry);
    case 'picscwasm':
      return makeMICPicsCWasmAdapter(entry);
    case 'pics':
      return makePICSAdapter(entry);
    case 'wasm':
      switch (entry.manifestKey) {
        case 'htj2k':  return makeHTJ2KAdapter(entry);
        case 'jpegls': return makeJPEGLSAdapter(entry);
        case 'jxl':    return makeJXLAdapter(entry);
        default: throw new Error(`unknown wasm codec: ${entry.manifestKey}`);
      }
    default:
      throw new Error(`unknown codec kind: ${entry.kind}`);
  }
}
