#!/usr/bin/env bash
# build-pics.sh — Compile the pure-C PICS parallel-strip decoder
# (ojph/mic_parallel.c + mic_decompress_c.c) to WASM with Emscripten PTHREADS,
# for the dashboard's MIC-C-WASM-PICS codec. Output ES module + wasm are
# committed to web/vendor/mic-pics/.
#
# pthreads notes:
#   -pthread                      : real threads = Web Workers (needs COOP/COEP,
#                                   which serve.py sets; the page is crossOriginIsolated).
#   -sPTHREAD_POOL_SIZE=8         : pre-spawn 8 workers so pthread_create doesn't
#                                   have to spin one up mid-decode.
#   -sEXPORT_ES6=1                : ES-module output so the pthread bootstrap can
#                                   resolve its own URL (import.meta.url) to spawn
#                                   nested workers — loaded via dynamic import()
#                                   in a module Web Worker (NOT the new Function
#                                   loader used for the single-thread builds).
#   fixed INITIAL_MEMORY, no growth: avoids growable-SharedArrayBuffer edge cases
#                                   with pthreads; 512 MB covers the largest test
#                                   image (MG3, 27 MB raw) with headroom.
#
# Prereq: Emscripten (emcc). Run: bash web/wasm-c/build-pics.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="$repo/web/vendor/mic-pics"
mkdir -p "$out"

command -v emcc >/dev/null 2>&1 || {
  echo "error: emcc not found on PATH. Install Emscripten (brew install emscripten) first." >&2
  exit 1
}

echo "emcc: $(emcc --version | head -1)"
emcc \
  "$repo/ojph/mic_decompress_c.c" \
  "$repo/ojph/mic_parallel.c" \
  "$here/mic_pics_wasm.c" \
  -O3 \
  -I "$repo/ojph" \
  -pthread \
  -sPTHREAD_POOL_SIZE=8 \
  -sMODULARIZE=1 \
  -sEXPORT_ES6=1 \
  -sEXPORT_NAME=createMicPicsModule \
  -sEXPORTED_FUNCTIONS=_mic_pics_decode,_malloc,_free \
  -sEXPORTED_RUNTIME_METHODS=HEAPU8,HEAPU16 \
  -sALLOW_MEMORY_GROWTH=0 \
  -sINITIAL_MEMORY=512MB \
  -sSTACK_SIZE=4MB \
  -sENVIRONMENT=web,worker \
  -o "$out/mic-pics-decoder.mjs"

echo "Wrote $out/mic-pics-decoder.mjs + .wasm"
ls -la "$out"

{
  echo ""
  echo "## MIC-C-WASM-PICS (built from ojph/mic_parallel.c + mic_decompress_c.c, pthreads)"
  echo ""
  echo "Built by \`web/wasm-c/build-pics.sh\`."
  echo "| file | sha256 |"
  echo "|------|--------|"
  echo "| mic-pics/mic-pics-decoder.wasm | $(shasum -a 256 "$out/mic-pics-decoder.wasm" | awk '{print $1}') |"
} >> "$repo/web/vendor/VERSIONS.md"
echo "Appended provenance to web/vendor/VERSIONS.md"
