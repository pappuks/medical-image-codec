#!/usr/bin/env bash
#
# run-paper-benchmarks.sh
#
# Runs every Go benchmark that produces a number reported in
#   paper/mic-paper-v8-ieee-tmi.tex
#
# Mapping of paper tables -> benchmarks driven here:
#
#   Table 1  (tab:ratios)            Lossless compression ratios
#                                      BenchmarkAllCodecs              (MIC/HTJ2K/JPEG-LS/PICS ratios)
#                                      BenchmarkMICCDeltaZstdDecomp    (Delta+Zstd-19 column, in-process libzstd)
#                                      BenchmarkWaveletV2SIMDRLEFSECompress  (Wavelet column)
#
#   Table 2  (tab:enc-amd64)         Encoding throughput, AMD64
#   Table 3  (tab:enc-arm64-full)    Encoding throughput, ARM64
#                                      BenchmarkAllCodecsEncode
#                                      BenchmarkMICCDeltaZstdEnc       (Delta+Zstd-19 encoding column)
#
#   Table 4  (tab:decomp-arm)        Decomp throughput, ARM64
#   Table 5  (tab:decomp-amd64)      Decomp throughput, AMD64
#                                      BenchmarkAllCodecs
#                                      BenchmarkMICCDeltaZstdDecomp    (Delta+Zstd-19 decoding column)
#                                      BenchmarkWaveletV2SIMDRLEFSECompress  (Wavelet+SIMD column)
#
#   Table 6  (tab:fse-combined)      FSE microbench, 1-state vs 4-state
#                                      BenchmarkFSEDecompress          (1-state)
#                                      BenchmarkFSEDecompress4State    (4-state)
#
# The Delta+Zstd-19 row is produced by BenchmarkMICCDeltaZstdDecomp /
# BenchmarkMICCDeltaZstdEnc in ojph/delta_zstd_micc_test.go (build tags
# cgo_ojph + cgo_zstd). They link libzstd in-process for a fair C-vs-C
# comparison against MIC's four-state C decoder. The older
# BenchmarkDeltaZstdDecompress uses the zstd CLI subprocess and is no
# longer the source of paper numbers — see .claude/benchmark-rules.md.
#
# JavaScript tables (tab:js-perf, tab:pics-js) are produced by a separate
# Node.js harness and are out of scope for this script.
#
# Usage:
#   ./run-paper-benchmarks.sh                       # paper default: -benchtime=10x
#   BENCHTIME=3x ./run-paper-benchmarks.sh          # quicker smoke-run
#   OUTDIR=/tmp/mic-bench ./run-paper-benchmarks.sh # custom output dir
#
# Per-benchmark output is written to a timestamped directory under results/.

set -euo pipefail

cd "$(dirname "$0")"

BENCHTIME="${BENCHTIME:-10x}"
STAMP="$(date +"%Y%m%d-%H%M%S")"
OUTDIR="${OUTDIR:-results/${STAMP}}"
mkdir -p "$OUTDIR"

echo "MIC paper benchmark suite"
echo "  benchtime : ${BENCHTIME}  (paper uses 10x)"
echo "  output    : ${OUTDIR}"
echo "  arch      : $(uname -m)  $(uname -sr)"
echo

# Preflight — make sure the cgo_ojph build can link against libopenjph/libcharls.
echo "=== preflight: cgo_ojph build ==="
if ! go build -tags cgo_ojph ./ojph/ 2>"${OUTDIR}/preflight.err"; then
  cat "${OUTDIR}/preflight.err" >&2
  echo >&2
  echo "ERROR: cgo_ojph build failed. Required:" >&2
  echo "  brew install openjph charls            (Apple Silicon: /opt/homebrew)" >&2
  echo "  ojph/ojph.go must include -I/opt/homebrew/include and -L/opt/homebrew/lib" >&2
  exit 1
fi
echo "  ok"

# Preflight — cgo_zstd build (needed for the Delta+Zstd-19 throughput row).
echo "=== preflight: cgo_zstd build ==="
if ! go build -tags "cgo_ojph cgo_zstd" ./ojph/ 2>"${OUTDIR}/preflight-zstd.err"; then
  cat "${OUTDIR}/preflight-zstd.err" >&2
  echo >&2
  echo "ERROR: cgo_zstd build failed. Required:" >&2
  echo "  brew install zstd                      (Apple Silicon: /opt/homebrew)" >&2
  echo "  apt install libzstd-dev                (Linux)" >&2
  exit 1
fi
echo "  ok"

run_bench() {
  # run_bench <label> <output-file> <go-test-args...>
  local label="$1"; shift
  local file="$1"; shift
  local out="${OUTDIR}/${file}"
  echo
  echo "=== ${label} ==="
  echo "    -> ${out}"
  # Stream to the log file; only print a tail to the terminal so the screen
  # stays readable. Full output lives in the per-benchmark file.
  if ! go test "$@" 2>&1 | tee "${out}" | tail -n 4; then
    echo "FAILED: see ${out}" >&2
    return 1
  fi
}

# -------- Tables 1, 4, 5: decompression throughput + ratios --------
run_bench "Decompression throughput + ratios   (Tables 1/4/5: BenchmarkAllCodecs)" \
  "01-all-codecs-decompress.txt" \
  -tags cgo_ojph -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkAllCodecs$' ./ojph/

# -------- Tables 2, 3: encoding throughput --------
run_bench "Encoding throughput                 (Tables 2/3:   BenchmarkAllCodecsEncode)" \
  "02-all-codecs-encode.txt" \
  -tags cgo_ojph -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkAllCodecsEncode$' ./ojph/

# -------- Table 6: FSE microbenchmarks (1-state vs 4-state) --------
run_bench "FSE 1-state microbench              (Table 6:      BenchmarkFSEDecompress)" \
  "03-fse-1state.txt" \
  -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkFSEDecompress$' .

run_bench "FSE 4-state microbench              (Table 6:      BenchmarkFSEDecompress4State)" \
  "04-fse-4state.txt" \
  -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkFSEDecompress4State$' .

# -------- Tables 1, 4, 5: Delta+Zstd-19 decoding column (in-process libzstd) --------
run_bench "Delta+Zstd-19 decoding              (Tables 1/4/5 column)" \
  "05a-delta-zstd-decomp.txt" \
  -tags "cgo_ojph cgo_zstd" -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkMICCDeltaZstdDecomp$' ./ojph/

# -------- Tables 2, 3: Delta+Zstd-19 encoding column (in-process libzstd) --------
run_bench "Delta+Zstd-19 encoding              (Tables 2/3 column)" \
  "05b-delta-zstd-enc.txt" \
  -tags "cgo_ojph cgo_zstd" -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkMICCDeltaZstdEnc$' ./ojph/

# -------- Tables 1, 4, 5: Wavelet+SIMD column --------
run_bench "Wavelet+SIMD pipeline               (Tables 1/4/5: Wavelet column)" \
  "06-wavelet-simd.txt" \
  -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkWaveletV2SIMDRLEFSECompress$' .

# -------- Single-codec deep dives (sanity vs BenchmarkAllCodecs) --------
run_bench "HTJ2K fair decomp deep dive         (cross-check)" \
  "07-htj2k-fair.txt" \
  -tags cgo_ojph -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkHTJ2KFairDecomp$' ./ojph/

run_bench "JPEG-LS decomp deep dive            (cross-check)" \
  "08-jpegls-decomp.txt" \
  -tags cgo_ojph -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkJPEGLSDecomp$' ./ojph/

# -------- PICS strip-count scaling (covers the PICS-N columns in all tables) --------
run_bench "PICS strip scaling                  (PICS-N columns)" \
  "09-pics-strips.txt" \
  -benchmem -run=^$ -benchtime="${BENCHTIME}" \
  -bench '^BenchmarkParallelStrips' .

echo
echo "All benchmarks complete."
echo "Results in: ${OUTDIR}"
ls -1 "${OUTDIR}"

# Post-process: emit paper-style tables (Tables 1, 2/3, 4/5, 6).
# The script writes paper-tables.txt into ${OUTDIR} and also prints to stdout.
if command -v python3 >/dev/null 2>&1 && [ -x ./paper-tables.py ]; then
  echo
  echo "=== Paper-format tables ==="
  ./paper-tables.py "${OUTDIR}"
else
  echo
  echo "Skipping paper-format tables: python3 or paper-tables.py not available."
fi
