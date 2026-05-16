# MIC Paper Benchmark Rules

Apply these rules whenever producing, refreshing, or quoting benchmark numbers
that appear in `paper/mic-paper-*.tex`. The accompanying inventory of every
benchmark in the repo lives in [`docs/benchmarks.md`](../docs/benchmarks.md);
this file is the procedural rulebook on top of that inventory.

---

## 1. Single Source of Truth

All paper numbers must come from `./run-paper-benchmarks.sh`. Do **not**
hand-pick numbers from one-off `go test -bench` runs and paste them into the
paper.

- The driver is [`run-paper-benchmarks.sh`](../run-paper-benchmarks.sh). It
  runs exactly the seven benchmark invocations whose outputs feed the paper.
- The post-processor is [`paper-tables.py`](../paper-tables.py). It parses
  the per-benchmark text logs and emits the four paper tables in canonical
  layout.
- The canonical output file is `results/<timestamp>/paper-tables.txt`. Any
  number quoted in the paper must be traceable to one of those files.
- If a single number is being updated in isolation (rare), re-run the full
  suite anyway — partial reruns mix run-to-run variance across tables and
  hide regressions in adjacent columns.

```bash
./run-paper-benchmarks.sh                       # paper-default 10x iterations
BENCHTIME=3x ./run-paper-benchmarks.sh          # smoke run, not for paper
OUTDIR=/tmp/mic-bench ./run-paper-benchmarks.sh # custom results dir
```

The 10x default is intentional: it matches the run-to-run variance budget
stated in the paper (§Benchmark Procedure: <2% on Apple M2 Max, <5% on Intel
AMD64). Lower iteration counts violate that budget.

---

## 2. Serial vs Parallel — Don't Mix Units

Benchmarks in this repo come in two structurally different shapes (see
[`docs/benchmarks.md` §1](../docs/benchmarks.md#1-methodology--serial-vs-parallel-benchmarks)):

- **Serial**: `for i := 0; i < b.N; i++ { decode() }` — reports
  *single-thread* MB/s.
- **Parallel**: `b.N` goroutines launched concurrently with a `sync.WaitGroup`
  — reports *aggregate multi-core* MB/s.

Rules:

1. Paper Tables 4/5 (decompression) and Tables 2/3 (encoding) are
   **single-thread** columns. Only serial benchmarks may feed them.
2. Paper Table 6 (FSE 1-state vs 4-state) is **intentionally aggregate
   parallel** — it is the entropy coder microbench. Keep the goroutine
   pattern in `BenchmarkFSEDecompress` and `BenchmarkFSEDecompress4State`.
3. PICS-C-N entries in Tables 4/5 are serial benchmarks wrapping
   internally-parallel C pthread code. That is correct — wall-clock decode
   time of one image using N threads is the apples-to-apples comparison.
4. Never paste an aggregate-parallel MB/s number into a single-thread column.
   The numbers are not on the same scale (typically 5–10× different).
5. When adding a new decompression bench whose result will appear in a
   single-thread table, write a plain `for i := 0; i < b.N; i++` loop. Do
   not copy the goroutine pattern from `fseu16_test.go` or `fse2state_test.go`.
6. When auditing an existing benchmark before re-running it, verify its shape
   with: `grep -A6 "func BenchmarkXxx" file_test.go | grep -E "wg|go func"`.

The four wavelet decompression benchmarks
(`BenchmarkWaveletFSECompress`, `BenchmarkWaveletRLEFSECompress`,
`BenchmarkWaveletV2RLEFSECompress`, `BenchmarkWaveletV2SIMDRLEFSECompress`)
were corrected from parallel to serial. They must stay serial — they feed
the Wav+SIMD column in Tables 1, 4, 5.

---

## 3. Cross-Platform Table Parity

Paper tables that report the same metric on ARM64 and AMD64 (encoding,
decompression, FSE microbench) must have the same set of codec columns.
Silently omitting a codec from one platform creates a misleading
comparison: the reader cannot tell whether the codec is absent because it
performs poorly, because it is unavailable on that platform, or because
nobody ran the benchmark yet.

Rules:

- If a codec runs on both platforms, both tables must include it. No
  exceptions.
- If a codec is genuinely unavailable on one platform (e.g.\ MIC-4state-SIMD
  has no NEON kernel on ARM64 today), the corresponding column may be
  omitted, but the caption text must say so explicitly: "ARM64 has no
  equivalent of AMD64's BMI2 four-state kernel, so MIC-4state-SIMD is
  reported only for AMD64."
- A codec implemented with different mechanisms on the two platforms (e.g.\
  the wavelet variant: AVX2 on AMD64, scalar on ARM64) should be labelled
  to reflect what actually runs — for example, "Wav+SIMD" on AMD64 and
  "Wavelet" on ARM64. Do not paste a "+SIMD" label onto a column that runs
  scalar code.
- A codec that is *expected* to run on both platforms but has not yet been
  measured on one of them is a paper-blocking bug, not a column to omit
  silently. File the measurement gap explicitly in the paper's Future Work
  section as well as in this rules file's TODO list (below).

### TODO: known cross-platform gaps to close

These are open as of v8 of the paper. Each must be closed before the
corresponding column is treated as final in any future revision.

- **JPEG-LS on AMD64 decompression.** `BenchmarkAllCodecs` produces the
  ARM64 JPEG-LS column, but the AMD64 decompression table currently has
  no JPEG-LS row. Re-run on the AMD64 reference platform and update
  Table `tab:decomp-amd64`.
- **PICS-C-2 on AMD64.** ARM64 reports PICS-C-2/4/8; AMD64 reports only
  PICS-C-4/8. Add PICS-C-2 to the AMD64 run.
- **Wavelet NEON kernel on ARM64.** Until a NEON predict/update kernel
  exists, the Wavelet column on ARM64 must keep the *Wavelet* label, not
  *Wav+SIMD*. When the NEON kernel ships, rename the column and update
  the variant description in Section V.D.
- **JavaScript benchmarks on the current reference platform.** The
  paper's JavaScript tables (`tab:js-perf`, `tab:pics-js`) are M2 Max
  numbers carried over from v6. When the Go benchmarks move to a new
  ARM64 reference (currently M4 Pro), the JS tables must be re-measured
  on the same chip; until then, the JS captions must explicitly note the
  platform mismatch.

---

## 4. Hardware and Platform

The paper claims numbers on exactly two reference platforms. Numbers from
other hardware can be reported only as additional/historical results — they
must not silently replace M2 Max or 285K numbers in the canonical tables.

| Paper role | Hardware | Notes |
|---|---|---|
| ARM64 reference | Apple M2 Max (12-core: 8P+4E) | macOS, Go gc compiler defaults, CGO C compiled with `-O3` |
| AMD64 reference | Intel Core Ultra 9 285K (mixed P-core/E-core) | Always describe as "mixed P-core/E-core topology"; never as "24 P-core" |

Forbidden mistakes:

- Quoting numbers measured on M4 Pro, M1, M3, or any other Apple chip as if
  they were M2 Max numbers. If you re-run on a different chip, explicitly
  label the new column.
- Calling the 285K "24-core" or "P-core only" — it has mixed P/E cores and
  that affects benchmark variance.
- Mixing chip generations within a single table. If Table 4 has 21 rows from
  M2 Max, all 21 must be M2 Max — never patch a missing row from a different
  machine.

System hygiene before a paper-quality run:

- Quit Chrome, Slack, video calls, IDE indexers, and anything else that
  saturates a core.
- Plug in mains power (laptops down-clock on battery).
- Let the machine sit idle for ~30 seconds before launching — caches warm,
  bursty background jobs settle.
- The first iteration of each subtest is often noisier than later ones; the
  10x default amortizes this.

---

## 5. Paper Table → Benchmark Map

This map must be kept in sync with `run-paper-benchmarks.sh` and
`paper-tables.py`. If you change any of the three, change all three.

| Paper table | LaTeX label | Source benchmark(s) | Output file | Parallelism |
|---|---|---|---|---|
| Table 1 — Compression ratios | `tab:ratios` | `BenchmarkAllCodecs` (ratio metric) + `BenchmarkDeltaZstdDecompress` (zstd-19 column) + `BenchmarkWaveletV2SIMDRLEFSECompress` (wavelet column) | `01-…`, `05-…`, `06-…` | Serial |
| Table 2 — AMD64 encoding | `tab:enc-amd64` | `BenchmarkAllCodecsEncode` | `02-…` | Serial |
| Table 3 — ARM64 encoding | `tab:enc-arm64-full` | `BenchmarkAllCodecsEncode` | `02-…` | Serial |
| Table 4 — ARM64 decoding | `tab:decomp-arm` | `BenchmarkAllCodecs` + `BenchmarkWaveletV2SIMDRLEFSECompress` | `01-…`, `06-…` | Serial |
| Table 5 — AMD64 decoding | `tab:decomp-amd64` | `BenchmarkAllCodecs` + `BenchmarkWaveletV2SIMDRLEFSECompress` | `01-…`, `06-…` | Serial |
| Table 6 — FSE 1-state vs 4-state | `tab:fse-combined` | `BenchmarkFSEDecompress` + `BenchmarkFSEDecompress4State` | `03-…`, `04-…` | **Parallel** |

Any other benchmark in the repo (e.g. `BenchmarkDeltaRLEFSECompress`,
`BenchmarkRANSDecompress8State`, `BenchmarkPICSVsAllCodecs`,
`BenchmarkWaveletV2RLEFSECompress` scalar) is exploratory or historical and
**must not** feed a paper table.

---

## 6. CGO Prerequisites

`BenchmarkAllCodecs`, `BenchmarkAllCodecsEncode`, `BenchmarkHTJ2KFairDecomp`,
and `BenchmarkJPEGLSDecomp` require the `cgo_ojph` build tag plus two C
libraries:

```bash
brew install openjph charls         # Apple Silicon: /opt/homebrew
# Linux:  apt install libopenjph-dev libcharls-dev   (or build from source)
```

`run-paper-benchmarks.sh` preflights the cgo build and fails fast with a
helpful message. If the preflight fails, fix the install — do not skip the
cgo benchmarks and submit a paper with empty HTJ2K/JPEG-LS columns.

The `ojph/ojph.go` and `ojph/charls.go` files include
`-I/opt/homebrew/include` / `-L/opt/homebrew/lib` for macOS. On Linux you may
need to add `-I/usr/local/include` / `-L/usr/local/lib` instead.

---

## 7. Re-running Policy

Trigger a re-run when:

- A benchmarked code path changes (encoder, decoder, table-build,
  predictor, RLE, SIMD kernel, FSE state count, etc.).
- The compiler, Go version, or system C library is upgraded.
- A reported MB/s in a column changes by more than the platform's stated
  variance (>2% ARM64, >5% AMD64) compared to the previous run.

Do **not** treat a single >5% jump as the new number. Re-run the full suite
once; if the second run agrees with the first, accept. If it disagrees,
investigate (background process? thermal throttling? bug?). Variance is the
gate, not the average — outliers must be reproduced before they're paper.

Encoding numbers (Tables 2/3) have higher inherent variance than decoding
because the encoder allocates and builds tables. A 5–8% encoding jitter is
normal; investigate only if it exceeds 10%.

---

## 8. Adding a New Column to a Paper Table

Three artifacts must be changed together. If only some of them change, the
column will silently disappear from the next paper rebuild.

1. **Add the benchmark** — a serial loop (or a parallel one only if the
   column is in Table 6 / a microbench). Place it in the same package as
   adjacent columns (`ojph/` for cgo-dependent variants, root `mic` package
   otherwise).
2. **Add the invocation** to `run-paper-benchmarks.sh` with a new numbered
   output file, e.g. `10-newcodec.txt`.
3. **Add the parser branch** to `paper-tables.py` (the `BENCH_RE` already
   matches `BenchmarkXxx/...` lines; extend `parse_results` and one of the
   `table_*` functions to surface the new variant).
4. **Re-run on both reference platforms** before quoting numbers in the
   paper. A single-platform column is acceptable only if Section V.E
   explicitly says "AMD64 only" or "ARM64 only."

When deleting a column, reverse the same three changes plus remove any
LaTeX still referencing it.

---

## 9. Things NOT to Do

- ❌ Quote numbers from `BenchmarkDeltaRLEFSECompress` in the paper. It's a
  parallel aggregate microbench in `fseu16_test.go` that predates the
  paper-table refactor. The historical hardware tables in `docs/benchmarks.md`
  use it; the paper must not.
- ❌ Quote numbers from `BenchmarkFSE2StateSummary` in the paper. Same
  reason — parallel, summary-only, not in the source-of-truth pipeline.
- ❌ Reintroduce `var wg sync.WaitGroup` / `go func()` patterns into the
  wavelet decompression benchmarks, or into any new benchmark that feeds
  Tables 4/5.
- ❌ Run `go test -bench` with `-cpu=1` to "force serial" — that changes
  `GOMAXPROCS` for the whole process and skews other measurements. If you
  need single-thread numbers, restructure the benchmark to a serial loop.
- ❌ Edit `paper-tables.txt` by hand. It is generated. Hand edits are lost on
  the next run.
- ❌ Commit `results/` directories. They are reproducible from source and
  bloat the repo. Reference them by date in commit messages instead.
- ❌ Mix M-series chip generations within a single table.
- ❌ Treat the first iteration of a benchmark subtest as authoritative. Use
  `-benchtime=10x` (the default in the driver script).
- ❌ Quote a wavelet number from `BenchmarkWaveletV2RLEFSECompress` (the
  scalar V2 bench). The paper's Wav+SIMD column uses
  `BenchmarkWaveletV2SIMDRLEFSECompress`, which dispatches to AVX2 on AMD64
  and to the blocked-column scalar layout on ARM64. The streams are
  bit-identical; only the kernel differs.

---

## 10. Wavelet+SIMD Specifics

The Wav+SIMD column in Tables 1, 4, 5 must be sourced from
`BenchmarkWaveletV2SIMDRLEFSECompress`, which is a serial loop after the
recent fix.

- On AMD64 the SIMD bench dispatches to AVX2 `wt53PredictAVX2` /
  `wt53UpdateAVX2` kernels (Haswell+). The compressed stream is
  bit-identical to the scalar V2 stream.
- On ARM64 the SIMD bench currently falls back to the blocked-column scalar
  layout. **Do not** describe the ARM64 wavelet result as "scalar" or "no
  SIMD" — it still benefits from the 8-column blocked layout that reduces
  L2 misses. Describe it as "blocked column layout, no vector kernels yet."
- A NEON kernel for ARM64 is on the roadmap; when added, it must keep stream
  compatibility and be wired into the same `BenchmarkWaveletV2SIMDRLEFSECompress`
  dispatch.

---

## 11. PICS Specifics

- PICS-C-N (C pthreads + per-strip SIMD) is the canonical PICS column in
  Tables 4/5. The Go-only PICS-N variant is reported alongside but is not
  the headline number.
- PICS-N for small images (MR 256×256) shows speedup ≤ 1.0× — thread
  scheduling overhead exceeds the work. Footnote this in the paper rather
  than hiding the data point; the prose pattern is:
  > "MR is too small for PICS-C-8; PICS-C-4 is best."
- When the strip count exceeds image rows / 8, the encoder silently caps
  it. Don't report PICS-16 numbers — the codec doesn't actually use 16
  strips on the standard test corpus.

---

## 12. Quoting Numbers in the Paper

- Always cite the platform when stating a throughput: "On Apple M2 Max,
  MIC-4state-C achieves ..." Never "MIC-4state-C achieves X MB/s" without
  the platform.
- Round MB/s to whole integers (the driver script does this; don't add
  decimals back).
- Round ratios to two decimals with the `×` suffix: `2.35×`, not `2.350`
  or `2.35x`. Use proper Unicode `×` (U+00D7), not ASCII `x`, in the LaTeX
  source where `$\times$` is appropriate.
- Geomeans use the `geomean` row from `paper-tables.txt` directly; do not
  hand-compute on a subset.
- When stating a percentage gain, always anchor to a denominator: "+27% over
  MIC-4state-C on ARM64," not just "+27% faster."
