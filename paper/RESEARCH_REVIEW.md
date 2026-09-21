# Research Review: Publishability Assessment for MIC (Medical Image Codec)

## Executive Summary

This repository contains **publication-worthy research** with several distinct contributions suitable for peer-reviewed venues. The work is strongest as a **systems/engineering paper** for a compression or medical imaging informatics venue. Below I identify the publishable contributions, rank them by novelty and impact, recommend target venues, and flag gaps that must be addressed before submission.

---

## Publishable Contributions (Ranked by Novelty)

### 1. Multi-State ANS Decoding for Medical Image Throughput (HIGH NOVELTY)

**What it is:** Breaking the serial dependency chain in tANS/FSE decoding by interleaving 2 or 4 independent state machines, with platform-specific assembly kernels (BMI2 on x86-64, scalar ARM64).

**Why it matters:** ANS (Asymmetric Numeral Systems) decoding has a fundamental throughput bottleneck: each state transition depends on the previous one (4-cycle table-lookup latency). The 4-state interleaving is a clean ILP optimization that achieves:
- +82% to +142% isolated FSE throughput (4-state vs 1-state, Intel Xeon)
- +63% to +304% on Apple M2 Max (ARM64)
- +20% to +75% full pipeline (Delta+RLE+FSE) end-to-end improvement

**Novelty assessment:** While interleaved ANS streams exist in the literature (e.g., Zstandard uses interleaved Huffman streams), **applying multi-state ANS specifically to 16-bit medical image entropy coding with measured ILP analysis and platform-specific assembly kernels** is novel. The format design (magic-byte auto-detection, backward compatibility with single-state streams) is practical and well-engineered. The paper `TODO.md` already identifies this as "a genuine, novel throughput optimization."

**Publication angle:** "Multi-State ANS Decoding for High-Throughput Lossless Medical Image Decompression" — could stand alone as a short paper at DCC (Data Compression Conference) or as a core contribution in a longer systems paper.

---

### 2. 16-Bit Native Entropy Coding Pipeline (MEDIUM-HIGH NOVELTY)

**What it is:** Extending FSE/tANS from the standard 4,096-symbol alphabet to 65,535 symbols, combined with a 16-bit-native RLE stage, to directly model the residual distribution of DICOM images without byte-splitting.

**Why it matters:** All major general-purpose compressors (zstd, LZ4, Deflate) and most image codecs (JPEG, JPEG-LS, JPEG 2000) operate on 8-bit alphabets or binary arithmetic coding. Medical images with 10-16 bit samples are forced through byte-splitting or truncation, losing inter-byte correlation. MIC demonstrates that a native 16-bit entropy pipeline outperforms Delta+zstd by 10-22% across all tested modalities.

**Novelty assessment:** The argument in `docs/16bit-alphabet-entropy-coding.md` is compelling and well-supported by data. The observation that delta-coded medical image residuals produce sparse-but-wide distributions ideally suited to wide-alphabet ANS is a genuine insight. The adaptive tableLog (11→12 based on symbol density) yielding 4-7% ratio improvement is a practical refinement.

**Publication angle:** This is best framed as a design rationale paper: "Why 16-Bit Entropy Coding Outperforms Byte-Oriented Compression for DICOM Lossless" — suitable for Journal of Medical Imaging (JMI) or Medical Image Analysis.

---

### 3. Wavelet vs. Delta for Lossless Medical Compression: A Controlled Comparison (MEDIUM NOVELTY)

**What it is:** A head-to-head comparison of the Le Gall 5/3 integer wavelet (same as JPEG 2000 lossless) against simple delta prediction, both feeding into the same downstream RLE+FSE pipeline, across 8 medical imaging modalities.

**Why it matters:** The conventional wisdom (from JPEG 2000's success) is that wavelets are superior decorrelators. MIC's data shows this is nuanced for lossless medical imaging:
- Wavelet V2 (5-level) achieves slightly better **compression ratio** on 7/8 modalities (+1-5% vs delta)
- But delta achieves 1.1-3.8x better **decompression speed** due to single-pass uint16 vs two-pass int32
- CT is the one modality where delta wins on ratio too (2.24x vs 1.67x) due to int32 coefficient overflow

**Novelty assessment:** The analysis in `docs/wavelet-fse-analysis.md` and `docs/16bit-alphabet-entropy-coding.md` provides a well-reasoned explanation of **why** filter-bank approaches lose their advantage in lossless mode: coefficient overflow, update-step correlation reintroduction, doubled memory bandwidth, and entropy coding mismatch. This is a useful **negative result** that challenges assumptions in the JPEG 2000 lossless community.

**Publication angle:** "Filter-Bank vs. Prediction-Based Decorrelation for Lossless Medical Image Compression: When Simpler is Better" — DCC or IEEE Transactions on Medical Imaging (TMI).

---

### 4. SIMD-Accelerated 5/3 Wavelet Transform with Cache-Optimized Column Pass (MEDIUM NOVELTY)

**What it is:** A blocked column-pass strategy (8 columns per cache-line load) combined with AVX2 predict/update kernels for the Le Gall 5/3 integer wavelet, achieving 14-47% decompression speedup while producing bit-identical output.

**Why it matters:** The column pass of a 2D separated wavelet transform is notoriously cache-unfriendly (stride = image width × 4 bytes). The blocked approach reduces cache misses ~8x. Combined with AVX2 vectorization (VPADDD/VPSUBD/VPSRAD), the result exceeds HTJ2K (OpenJPH) decompression speed on all 8 tested modalities.

**Novelty assessment:** Cache-blocking for wavelet transforms is known in HPC literature, but the specific combination with AVX2 lifting kernels, 4-state FSE, and the demonstration that a simpler codec can outperform HTJ2K's heavily-optimized implementation is noteworthy.

---

### 5. MIC3: Tiled Container Format for Whole Slide Imaging (LOWER NOVELTY, HIGH PRACTICAL VALUE)

**What it is:** A tiled, pyramidal container format for RGB pathology images using YCoCg-R color transform + per-plane Delta+RLE+FSE, with O(1) random tile access and parallel compression.

**Why it matters:** Digital pathology images are 2-4 GB and existing solutions (JPEG 2000 in DICOM WSI, TIFF-based formats) are either slow or complex. MIC3 achieves 3-5x compression on tissue tiles with a simple, fast pipeline.

**Novelty assessment:** The individual components (YCoCg-R, tiling, pyramids) are well-established. The contribution is integration and engineering. Best suited as part of a systems paper rather than standalone.

---

### 6. Fair HTJ2K Comparison via In-Process CGO Benchmarking (METHODOLOGICAL CONTRIBUTION)

**What it is:** The paper initially claimed MIC was 1.3-1.5x faster than HTJ2K, but this was due to subprocess overhead in the HTJ2K benchmark. After re-benchmarking with in-process CGO bindings to OpenJPH:
- HTJ2K is actually ~1.8x faster than MIC-Go single-threaded
- MIC-C (same algorithm, C implementation) closes to within 0.87x of HTJ2K
- MIC-4state-C beats HTJ2K on 6/8 modalities

**Why it matters:** This honest self-correction strengthens the paper significantly. The transparent methodology (subprocess vs in-process benchmarking) is a cautionary tale for the compression community. The finding that a conceptually simpler codec (delta+RLE+FSE) can approach or match HTJ2K's throughput when implemented in C with ILP optimizations is itself interesting.

---

## Recommended Publication Strategy

### Option A: Single Comprehensive Paper (Recommended)

**Title:** "MIC: A 16-Bit Native Lossless Codec for High-Throughput Medical Image Decompression"

**Target Venue (Tier 1):**
- **IEEE Transactions on Medical Imaging (TMI)** — Impact Factor ~10, covers medical image compression
- **Medical Image Analysis (MedIA)** — Impact Factor ~13, if framed around clinical workflow impact

**Target Venue (Tier 2):**
- **Journal of Medical Imaging (JMI, SPIE)** — Impact Factor ~1.9, very appropriate scope
- **Data Compression Conference (DCC)** — Top venue for compression, accepts systems papers
- **Computerized Medical Imaging and Graphics** — Impact Factor ~5.7

**Structure:**
1. Introduction: The 16-bit alphabet gap in medical image compression
2. Pipeline Design: Delta → RLE → Extended FSE (16-bit native)
3. Multi-State ANS: ILP optimization with 2/4 independent state machines
4. Wavelet Alternative: 5/3 integer wavelet with SIMD and controlled comparison
5. Container Formats: MIC2 (multi-frame) and MIC3 (WSI tiling)
6. Evaluation: Compression ratio and throughput across 8 modalities, fair HTJ2K comparison
7. Discussion: When delta beats wavelet, CT overflow analysis, parallel scaling

### Option B: Two Focused Papers

**Paper 1 (Algorithm):** "Multi-State ANS Decoding with 16-Bit Native Entropy Coding for Lossless Medical Image Compression" → DCC or IEEE Signal Processing Letters

**Paper 2 (Systems):** "MIC: A Practical Lossless Codec for DICOM with Multi-Frame and Whole Slide Imaging Support" → JMI or Computerized Medical Imaging and Graphics

---

## Gaps to Address Before Submission

### Must Fix (Blocking)

1. **JPEG-LS Comparison** — JPEG-LS (ISO 14495-1) is a first-class DICOM transfer syntax and the primary lossless competitor. Its absence will be the first reviewer comment. Benchmark against CharLS (optimized implementation) for both ratio and throughput. This is already flagged in `paper/TODO.md`.

2. **Wavelet Implementation Details** — The wavelet comparison needs to state explicitly: 5 decomposition levels, LL→HL→LH→HH subband ordering (coarsest to finest), ZigZag coefficient encoding with escape for int32 overflow. Without this, reviewers cannot assess whether the wavelet pipeline received comparable optimization effort. Flagged in `TODO.md`.

3. **Statistical Significance** — Current benchmarks report single-point measurements. For a journal paper, provide confidence intervals or standard deviations over multiple runs. The ±10% run-to-run variance noted in `docs/native-optimizations.md` must be characterized.

4. **Larger Test Corpus** — 8 images from ~5 modalities is thin for a journal paper. TMI/MedIA reviewers will want 20+ images across more modalities (US, PET, fluoroscopy, dental). Public datasets like TCIA (The Cancer Imaging Archive) would strengthen credibility.

### Should Fix

5. **Formal Complexity Analysis** — The paper should include O() analysis of encoding/decoding complexity. Delta is O(n) single-pass; wavelet is O(n·L) for L levels; FSE is O(n) but with different constants per state count. This frames the speed results theoretically.

6. **Parallel Scaling Curve** — The 16 GB/s headline is multi-core mammography. Show a scaling curve (1, 2, 4, 8, 16, 32, 64 cores) to demonstrate linear scaling and identify the bandwidth saturation point.

7. **Energy/Power Analysis** — For clinical deployment (embedded devices, edge computing), power consumption per decompressed GB is relevant. Even a rough estimate (throughput × TDP) would differentiate from pure-throughput papers.

8. **Comparison with FLIF, JPEG-XL, WebP Lossless** — While these are not DICOM standards, reviewers familiar with modern compression will ask about them. A brief comparison (even a table showing they're designed for 8-bit natural images) preempts this.

9. **Perceptual Quality Metrics for Future Lossy Mode** — The wavelet pipeline is positioned as a future lossy foundation. Stating PSNR/SSIM targets for visually lossless medical compression would strengthen the motivation.

---

## Strongest Individual Claims for Publication

Ranked by what would most impress reviewers:

1. **MIC-4state-C beats HTJ2K on 6/8 modalities** in fair in-process benchmarking — a conceptually simpler codec matching the state-of-the-art after ILP optimization
2. **16-bit native FSE outperforms Delta+zstd by 10-22%** — demonstrates that byte-oriented compression is fundamentally suboptimal for DICOM
3. **4-state ANS achieves +82% to +304% FSE throughput** — a clean ILP result with assembly validation on two architectures
4. **5/3 wavelet loses to simple delta on CT by 52% in ratio** — a surprising negative result explained by int32 overflow
5. **Wavelet V2 SIMD exceeds HTJ2K decompression speed on all 8 modalities** — despite using a simpler entropy coder

---

## Assessment of Current Paper Draft

Based on `paper/TODO.md`, the existing PDF paper has already addressed most minor issues (replaced figures, softened language, added Delta+zstd and MED comparisons). The remaining blockers are:
- JPEG-LS comparison (not started)
- Wavelet implementation details (partially addressed in docs but not in paper)
- HTJ2K speed claims need correction (data exists but paper text may not be updated)

The self-review in `TODO.md` is exceptionally thorough and honest — a strong sign of research maturity.

---

## Verdict

**Yes, this repository contains publishable research.** The strongest path is a single comprehensive paper targeting JMI or TMI, with the multi-state ANS optimization and 16-bit native entropy coding as the primary novel contributions, the wavelet comparison as a supporting negative result, and the fair HTJ2K benchmarking as methodological rigor. Addressing JPEG-LS comparison and expanding the test corpus are the two most important steps before submission.

The codebase quality (comprehensive tests, cross-platform assembly, browser decoder, honest benchmarking methodology) significantly strengthens the reproducibility argument, which is increasingly valued by reviewers.
