#!/usr/bin/env bash
# vendor-onnx.sh — vendor onnxruntime-web browser ESM bundle into web/vendor/onnx/.
# Mirrors scripts/vendor-wasm.sh (OpenJPH/CharLS). The bundle is fully
# self-contained (no bare imports); the jsep .wasm powers the WebGPU path.

set -euo pipefail
cd "$(dirname "$0")/.."

SRC=node_modules/onnxruntime-web/dist
DST=vendor/onnx
mkdir -p "$DST"

cp "$SRC/ort.all.bundle.min.mjs" "$DST/ort.all.bundle.min.mjs"
cp "$SRC/ort-wasm-simd-threaded.jsep.wasm" "$DST/ort-wasm-simd-threaded.jsep.wasm"
cp "$SRC/ort-wasm-simd-threaded.wasm" "$DST/ort-wasm-simd-threaded.wasm"

echo "vendored:"
ls -la "$DST"