# Paper Review & TODO

## Overall Assessment

The paper makes a clear, well-scoped contribution: a practical, open-source lossless codec for DICOM images that prioritizes decompression throughput. The writing is generally clear, the results are reproducible (code is public), and the benchmarking methodology is mostly sound. The paper is suitable for a systems/engineering venue (e.g., DCC, MICCAI workshop, or a medical imaging informatics journal) but would benefit from several clarifications before submission to a peer-reviewed journal.

**Strengths:**
- The three-stage pipeline (delta → RLE → FSE) is well-motivated and design tradeoffs are explained clearly.
- The two-state FSE decoder is a genuine, novel throughput optimization with clean measured results.
- The wavelet comparison is a useful negative result that strengthens the case for the chosen pipeline.
- The MIC2/MIC3 container formats are practical and well-specified.

**Blocking issues before submission:** the HTJ2K comparison methodology is not apples-to-apples (subprocess overhead vs. in-process library), and the wavelet comparison needs more implementation detail to support the "strictly dominates" claim. A JPEG-LS comparison is also expected by reviewers given JPEG-LS is a primary DICOM lossless standard.

---

## Must Fix (Methodology)

- [ ] **HTJ2K comparison fairness** *(Section 5.4)* — MIC timings are in-process library calls; HTJ2K timings include subprocess launch, PGM I/O, and codestream I/O overhead (~6 ms per call). Even for a 27 MB image, 6 ms at 225 MB/s represents ~27% inflation of apparent HTJ2K latency. Re-benchmark using OpenJPH as a library (it has a C API) or a harness that eliminates subprocess/I/O overhead. The 1.3–1.5× speedup claim in the abstract depends on a fair comparison.

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
