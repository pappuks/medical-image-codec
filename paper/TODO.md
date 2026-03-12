# Paper Review & TODO

## Overall Assessment

The paper makes a clear, well-scoped contribution: a practical, open-source lossless codec for DICOM images that prioritizes decompression throughput. The writing is generally clear, the results are reproducible (code is public), and the benchmarking methodology is mostly sound. The paper is suitable for a systems/engineering venue (e.g., DCC, MICCAI workshop, or a medical imaging informatics journal) but would benefit from several clarifications before submission to a peer-reviewed journal.

**Strengths:**
- The three-stage pipeline (delta → RLE → FSE) is well-motivated and design tradeoffs are explained clearly.
- The two-state FSE decoder is a genuine, novel throughput optimization with clean measured results.
- The wavelet comparison is a useful negative result that strengthens the case for the chosen pipeline.
- The MIC2/MIC3 container formats are practical and well-specified.

**Blocking issues before submission:** ~~the HTJ2K comparison methodology is not apples-to-apples (subprocess overhead vs. in-process library)~~ *(resolved: re-benchmarked in-process via CGO; HTJ2K is actually ~1.8× faster than MIC single-threaded; paper claims must be corrected)*, and the wavelet comparison needs more implementation detail to support the "strictly dominates" claim. A JPEG-LS comparison is also expected by reviewers given JPEG-LS is a primary DICOM lossless standard.

---

## Must Fix (Methodology)

- [x] **HTJ2K comparison fairness** *(Section 5.4)* — MIC timings are in-process library calls; HTJ2K timings include subprocess launch, PGM I/O, and codestream I/O overhead (~6 ms per call). Even for a 27 MB image, 6 ms at 225 MB/s represents ~27% inflation of apparent HTJ2K latency. Re-benchmark using OpenJPH as a library (it has a C API) or a harness that eliminates subprocess/I/O overhead. The 1.3–1.5× speedup claim in the abstract depends on a fair comparison.

  *(Done: Re-benchmarked using OpenJPH as an in-process library via CGO with mem_infile/mem_outfile — no subprocess launch, no disk I/O. Results show HTJ2K (OpenJPH) is actually ~1.8× faster than MIC at single-threaded decompression when measured fairly. The original 1.3–1.5× MIC advantage was entirely due to subprocess overhead inflating HTJ2K timings. MIC still achieves comparable or slightly better compression ratios on most modalities (notably CT: 2.24× vs 1.77×), but the decompression speed advantage claim must be retracted. MIC's value proposition is now: (a) competitive compression ratios, (b) very simple pipeline amenable to parallel scaling (16 GB/s on 64 cores for mammography), (c) purpose-built for 16-bit DICOM with no external dependencies. See `ojph/htj2k_fair_comparison_test.go` and `BenchmarkHTJ2KFairDecomp`.)*

  **Fair comparison results (single-threaded, in-process):**

  | Image | MIC ratio | HTJ2K ratio | MIC decomp (ms) | HTJ2K decomp (ms) | HTJ2K speedup |
  |-------|-----------|-------------|------------------|--------------------|---------------|
  | MR    | 2.35      | 2.38        | 0.61             | 0.38               | 1.6×          |
  | CT    | 2.24      | 1.77        | 3.69             | 1.93               | 1.9×          |
  | CR    | 3.63      | 3.77        | 38.82            | 22.09              | 1.8×          |
  | XR    | 1.74      | 1.67        | 54.63            | 33.66              | 1.6×          |
  | MG1   | 8.57      | 8.25        | 30.47            | 15.10              | 2.0×          |
  | MG2   | 8.55      | 8.24        | 30.30            | 15.54              | 2.0×          |
  | MG3   | 2.24      | 2.22        | 154.45           | 88.39              | 1.7×          |
  | MG4   | 3.47      | 3.51        | 98.25            | 55.65              | 1.8×          |
  | **Geo mean** | | | | | **1.8×** |

  **Follow-up: MIC ported to C (same algorithm, C implementation via CGO):**

  Porting the MIC decompression pipeline (FSE two-state + RLE + Delta) to C closes
  ~60% of the gap with HTJ2K. The C version is 1.56× faster than Go, and within
  0.87× of HTJ2K (i.e., HTJ2K is only ~15% faster than MIC-C, vs ~80% faster than
  MIC-Go). On CR and MG3, MIC-C matches HTJ2K. The remaining gap is likely due to
  HTJ2K's SIMD-optimized wavelet/block decoder vs MIC's scalar RLE+delta.
  See `ojph/mic_decompress_c.{h,c}`, `ojph/mic_c.go`, and `TestThreeWayComparison`.

  | Image | Go (MB/s) | C (MB/s) | HTJ2K (MB/s) | C/Go | C/HTJ2K |
  |-------|-----------|----------|--------------|------|---------|
  | MR    | 137       | 252      | 266          | 1.6× | 0.94×   |
  | CT    | 103       | 197      | 239          | 1.6× | 0.84×   |
  | CR    | 151       | 319      | 329          | 1.8× | 0.99×   |
  | XR    | 158       | 292      | 303          | 1.5× | 0.97×   |
  | MG1   | 314       | 466      | 636          | 1.5× | 0.71×   |
  | MG2   | 300       | 452      | 595          | 1.5× | 0.75×   |
  | MG3   | 169       | 306      | 318          | 1.6× | 1.01×   |
  | MG4   | 273       | 403      | 462          | 1.5× | 0.83×   |
  | **Geo mean** | | | | **1.56×** | **0.87×** |

- [ ] **Wavelet implementation details** *(Section 6)* — The wavelet pipeline is described in ~one paragraph. State: (a) number of decomposition levels applied, (b) subband scan order for RLE input (standard LL, HL, LH, HH?), (c) whether it received comparable optimization effort to the delta pipeline. The result that wavelet+RLE+FSE underperforms delta+RLE+FSE on compression ratio is surprising — JPEG 2000 routinely beats simple delta coders. If only one decomposition level is used, this would explain the gap and must be stated explicitly.

---

## Should Fix (Claims and Coverage)

- [x] **Qualify the "16 GB/s" headline** — 16 GB/s is achieved on MG1/MG2 specifically (mammography, 8.5× ratio). CT achieves 4.4 GB/s; MR achieves 2.3 GB/s. The abstract and conclusion should either report a cross-modality geometric mean or label 16 GB/s explicitly as "best-case on mammography."

- [ ] **Add JPEG-LS comparison** — JPEG-LS is a first-class DICOM transfer syntax and the closest practical competitor to MIC. Its absence is a gap reviewers will notice. Use CharLS (optimized JPEG-LS library) for both compression ratio and decompression speed benchmarks.

- [x] **Temporal mode: show a win case** *(Section 5.3)* — Independent mode (13.3×) beats temporal mode (12.9×) on the DBT dataset. Add at least one dataset where temporal mode wins (e.g., cardiac cine MRI, fluoroscopy, or a synthetic high-inter-frame-correlation sequence), or explicitly state temporal mode is a design provision for those use cases and has not been benchmarked favorably here.

- [x] **Separate single-threaded vs. multi-threaded throughput** — The abstract conflates two-state FSE gain (1.3–1.5× per thread) with 64-core scaling (16 GB/s). These are completely different operating conditions and should be clearly distinguished in the abstract and introduction.

- [x] **CT compression ratio anomaly** — MIC achieves 2.24× on CT vs. HTJ2K's 1.77× (a 27% gap favoring MIC). This is the strongest per-modality result in the paper. Provide a more detailed explanation: show the residual histogram for CT to confirm the overflow delimiter scheme keeps the symbol alphabet small compared to HTJ2K's fixed 16-bit block coding. If true, this is one of the stronger contributions.

---

## Consider Adding

- [x] **Delta+zstd baseline** — Compare against running `zstd` directly on the delta-encoded uint16 stream. Since MIC's entropy coder is FSE (the same family as Zstandard), this establishes whether the custom 16-bit RLE+FSE stages add value beyond a general-purpose compressor. *(Done: MIC outperforms Delta+zstd-19 by 10–22% on all modalities. See `TestDeltaZstdComparison` and paper Section 8.)*

- [x] **MED predictor comparison** — The JPEG-LS MED predictor (`median(left, top, left+top-diag)`) is a well-known improvement over avg-of-neighbors. Either benchmark it or cite a reason why the simpler predictor was chosen (simplicity, decompression speed). If avg-of-neighbors produces similar ratios, that is worth noting. *(Done: MED yields ~0.9% mean improvement at 1.5–2× decompression speed penalty. Avg predictor retained. See `TestMEDPredictorComparison` and paper Section 9.)*

- [ ] **Browser decoder methodology** — "10–30 M pixels/s in V8" is unverifiable as stated. Add: browser version, platform, which image(s), and how it was measured.

---

## Minor Fixes

- [x] **RLE minimum-run guarantee** *(Section 3.2)* — "Minimum run length 3 guarantees the RLE output is never larger than the input" is asserted without justification. Add a brief proof: the run header costs 1 symbol, a same-run of 3 encodes 3 symbols as 2 (header + value), a net saving of 1. This needs to be made explicit.

- [x] **YCoCg-R assembly caveat** *(Section 4.6)* — The paper presents the assembly routines as a performance optimization but Section 4.6 itself acknowledges they are "dispatch scaffolding for future SIMD-vectorized paths." Scalar code in assembly is typically slower than the compiler's scalar output. Either remove it from the contributions list or clearly state it is infrastructure for a planned optimization, not a current speedup.

- [x] **WSI "3–5×" range in abstract** — The 1946× white background result is a degenerate constant-plane case, not a compression algorithm result. The "3–5×" claim should be explicitly scoped to tissue tiles only.

- [x] **Replace pipeline figure (Fig. 1)** — The current `\fbox{\parbox{...}}` text box will render poorly in two-column layout. Replace with a TikZ diagram or an imported vector figure.

- [x] **"Strictly dominates" language** *(Abstract and Conclusion)* — Soften to "outperforms on all tested modalities" until the wavelet implementation quality is established (see Must Fix #2 above).

- [x] **Table placement consistency** — Tables 3 and 5 use `table*` (full-width) but Tables 1, 2, 4 use `table` (single-column). In two-column layout this causes awkward float placement. Make all large comparison tables use `table*` consistently.
