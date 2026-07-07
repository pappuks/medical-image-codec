#!/usr/bin/env bash
# build.sh — Compile the pure-C MIC decoder (ojph/mic_decompress_c.c) to WASM
# with Emscripten, producing the vendored decoder the PACS dashboard loads for
# the MIC-C-WASM codec rows. Output is committed to web/vendor/mic-c/ (same
# precedent as the OpenJPH/CharLS vendored WASM).
#
# Prereq: Emscripten (emcc) on PATH — `brew install emscripten` or the emsdk.
# Run from anywhere:  bash web/wasm-c/build.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"       # web/wasm-c
repo="$(cd "$here/../.." && pwd)"           # repo root
out="$repo/web/vendor/mic-c"
mkdir -p "$out"

command -v emcc >/dev/null 2>&1 || {
  echo "error: emcc not found on PATH. Install Emscripten (brew install emscripten) first." >&2
  exit 1
}

echo "emcc: $(emcc --version | head -1)"
emcc \
  "$repo/ojph/mic_decompress_c.c" \
  "$here/mic_c_wasm.c" \
  -O3 \
  -I "$repo/ojph" \
  -sMODULARIZE=1 \
  -sEXPORT_NAME=createMicCModule \
  -sEXPORTED_FUNCTIONS=_mic_c_decode,_malloc,_free \
  -sEXPORTED_RUNTIME_METHODS=ccall,cwrap,getValue,HEAPU8,HEAPU16 \
  -sALLOW_MEMORY_GROWTH=1 \
  -sSTACK_SIZE=4MB \
  -sINITIAL_MEMORY=32MB \
  -sENVIRONMENT=web \
  -sEXPORT_ES6=0 \
  -o "$out/mic-c-decoder.js"

echo "Wrote $out/mic-c-decoder.js + .wasm"
ls -la "$out"

# Record provenance next to the other vendored decoders.
{
  echo ""
  echo "## MIC-C-WASM (built from ojph/mic_decompress_c.c)"
  echo ""
  echo "Built by \`web/wasm-c/build.sh\` with:"
  echo "\`\`\`"
  emcc --version | head -1
  echo "\`\`\`"
  echo "| file | sha256 |"
  echo "|------|--------|"
  echo "| mic-c/mic-c-decoder.wasm | $(shasum -a 256 "$out/mic-c-decoder.wasm" | awk '{print $1}') |"
} >> "$repo/web/vendor/VERSIONS.md"
echo "Appended provenance to web/vendor/VERSIONS.md"
