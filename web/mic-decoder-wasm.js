// MIC WASM Decoder Loader
// Loads the Go WebAssembly binary and provides the same API as mic-decoder.js.
//
// Usage:
//   import { loadMICWasm } from './mic-decoder-wasm.js';
//   const decoder = await loadMICWasm();
//   const pixels = decoder.decode(compressedBytes, width, height);

/**
 * Load the MIC WASM decoder.
 * @param {string} [wasmPath='mic-decoder.wasm'] Path to the .wasm file
 * @returns {Promise<{decode, decodeFile, fseDecompress, deltaDecompress, version}>}
 */
export async function loadMICWasm(wasmPath = 'mic-decoder.wasm') {
  // Load wasm_exec.js if not already loaded
  if (typeof Go === 'undefined') {
    await new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = 'wasm_exec.js';
      script.onload = resolve;
      script.onerror = () => reject(new Error('Failed to load wasm_exec.js'));
      document.head.appendChild(script);
    });
  }

  const go = new Go();
  const result = await WebAssembly.instantiateStreaming(
    fetch(wasmPath),
    go.importObject
  );

  // Start the Go runtime (non-blocking)
  go.run(result.instance);

  // Wait for the MICWasm global to be set
  await new Promise((resolve) => {
    if (globalThis.MICWasm) {
      resolve();
    } else {
      document.addEventListener('mic-wasm-ready', resolve, { once: true });
    }
  });

  const wasm = globalThis.MICWasm;

  return {
    /**
     * Decode FSE-compressed Delta+RLE data.
     * @param {Uint8Array} compressedBytes
     * @param {number} width
     * @param {number} height
     * @returns {Uint16Array}
     */
    decode(compressedBytes, width, height) {
      const result = wasm.decode(compressedBytes, width, height);
      if (result instanceof Error) throw result;
      return result;
    },

    /**
     * Decode a .mic container file.
     * @param {Uint8Array} fileBytes
     * @returns {{ pixels: Uint16Array, width: number, height: number }}
     */
    decodeFile(fileBytes) {
      const result = wasm.decodeFile(fileBytes);
      if (result instanceof Error) throw result;
      return result;
    },

    /**
     * FSE-only decompression.
     * @param {Uint8Array} compressedBytes
     * @returns {Uint16Array}
     */
    fseDecompress(compressedBytes) {
      const result = wasm.fseDecompress(compressedBytes);
      if (result instanceof Error) throw result;
      return result;
    },

    /**
     * Delta-only decompression.
     * @param {Uint16Array} deltaData
     * @param {number} width
     * @param {number} height
     * @returns {Uint16Array}
     */
    deltaDecompress(deltaData, width, height) {
      const result = wasm.deltaDecompress(deltaData, width, height);
      if (result instanceof Error) throw result;
      return result;
    },

    /** @returns {string} */
    version() {
      return wasm.version();
    },
  };
}
