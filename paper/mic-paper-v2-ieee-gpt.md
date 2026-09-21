# A 16-Bit-Native Entropy Coding Pipeline for Lossless Medical Image Compression

**Kuldeep Singh**  
Innovaccer Inc.  
kuldeep.singh@innovaccer.com

---

## Abstract

Lossless compression remains important in medical imaging because archival storage, transmission, and diagnostic workflows often require exact reconstruction of high-bit-depth pixel data. Many medical images are stored at 10–16 bits per sample, yet a large fraction of practical compression software remains optimized for byte-oriented processing or relatively small symbol alphabets. This paper studies a lossless compression pipeline designed to operate natively on predicted 16-bit residuals.

We present MIC, a codec consisting of three stages: spatial prediction, 16-bit run-length encoding, and large-alphabet table-based asymmetric numeral system coding. The implementation extends FSE-style entropy coding to support large active symbol sets arising in medical image residual streams and includes a multi-state interleaved decoder that increases instruction-level parallelism during decompression. The design is motivated by the observation that, after simple spatial prediction, many medical images produce residual distributions that are sharply concentrated near zero and exhibit repeated 16-bit residual values.

We evaluate the proposed codec on 21 de-identified DICOM images spanning MR, CT, CR, X-ray, mammography, nuclear medicine, radiography, secondary capture, and fluoroscopy. On the evaluated dataset, the 16-bit-native pipeline improves compression ratio by 10–22% relative to a delta + Zstandard baseline. Across grayscale images, MIC achieves lossless compression ratios ranging from 1.7× to 8.9×. A four-state interleaved entropy decoder provides substantial decompression speedup over a single-state implementation without affecting compressed size. On the reported ARM64 and AMD64 platforms, the fastest MIC variants are competitive with HTJ2K in single-threaded settings and exceed it in strip-parallel configurations on the tested images.

In addition, compact JavaScript and WebAssembly implementations indicate that the core decoding pipeline is lightweight enough for client-side deployment. These results suggest that 16-bit-native entropy coding is a useful design point for lossless medical image compression when decompression throughput, implementation simplicity, and deployment portability are important. **[Needs work: add shuffle/bitshuffle-based general-purpose baselines, true browser benchmarks using Web Workers, and confidence intervals for all timing results.]**

**Index Terms**—medical image compression, lossless compression, entropy coding, ANS, FSE, DICOM, high-bit-depth imaging.

---

## I. Introduction

Medical imaging systems routinely generate large volumes of high-bit-depth data. Modalities such as computed tomography (CT), magnetic resonance imaging (MR), digital radiography (CR/XR), mammography, and tomosynthesis commonly store pixel values at 10, 12, or 16 bits per sample. These images must often be compressed losslessly for archival storage, network transmission, and interactive viewing, especially in picture archiving and communication systems (PACS) and cloud-based imaging workflows.

DICOM supports several established lossless transfer syntaxes, including JPEG 2000, HTJ2K, JPEG-LS, and RLE Lossless. These methods offer different tradeoffs among compression ratio, decoding speed, and implementation complexity. JPEG 2000 and HTJ2K provide strong compression performance but rely on transform and block-coding pipelines that are comparatively complex. JPEG-LS uses predictive coding and often yields strong lossless ratios, but its throughput and deployment characteristics remain application dependent. RLE Lossless is simple but typically provides limited compression.

This work studies a different design point for lossless medical image compression: direct coding of **predicted 16-bit residual streams**. The central observation is that, after simple spatial prediction, many medical images produce residual values that are strongly concentrated near zero and often contain repeated 16-bit symbols. Such data can be represented efficiently without first decomposing the residual stream into bytes.

Based on this observation, we present MIC, a lossless codec that combines:
1. a simple spatial predictor,
2. a 16-bit-native run-length representation of the residual stream, and
3. an extended FSE/tANS entropy coder that supports large active symbol alphabets.

The codec also includes multi-state interleaved entropy decoding to improve decompression throughput by increasing instruction-level parallelism.

The paper is motivated by three practical questions:

1. Can a 16-bit-native entropy coding pipeline improve compression efficiency over a byte-oriented general-purpose baseline on medical residual streams?
2. Can entropy-decoder organization be modified to improve throughput without changing compressed size?
3. Can such a codec remain compact enough for lightweight deployment, including client-side decoding?

### A. Contributions

The main contributions of this paper are as follows.

1. **A 16-bit-native lossless coding pipeline for medical images.**  
   We present a Delta + 16-bit RLE + extended FSE pipeline for grayscale medical image compression.

2. **An extended table-based entropy coder for large residual alphabets.**  
   The proposed implementation supports large active symbol ranges while dynamically sizing working tables to the observed symbol range.

3. **A multi-state interleaved entropy decoder.**  
   We implement and evaluate two-state and four-state FSE decoding to exploit instruction-level parallelism without changing compressed size.

4. **An empirical evaluation against standard codecs and a general-purpose baseline.**  
   We compare MIC with HTJ2K, JPEG-LS, and delta + Zstandard on a dataset of public de-identified DICOM images.

5. **A compact JavaScript/WASM decoder implementation.**  
   We provide a lightweight decoder to explore deployment feasibility for client-side viewing.  
   **[Needs work: current parallel JavaScript results are primarily Node.js measurements; add real browser benchmarks.]**

### B. Scope

The primary focus of this paper is **lossless grayscale medical image compression**. Multi-frame grayscale and single-frame RGB support are included as secondary studies. The present work does not address lossy coding, progressive decoding, or region-of-interest coding.

### C. Organization

Section II reviews related work. Section III describes the proposed coding pipeline. Section IV presents the multi-state entropy decoder. Section V describes the experimental methodology. Section VI reports compression and speed results. Section VII discusses limitations and implications. Section VIII concludes.

---

## II. Related Work

### A. Lossless Compression in Medical Imaging

JPEG 2000 remains one of the best known transform-based standards for lossless medical image compression. In lossless mode, it employs the reversible 5/3 integer wavelet transform and bit-plane-based block coding. HTJ2K reduces some of the throughput limitations of the original JPEG 2000 block coder while preserving compatibility with the broader ecosystem.

JPEG-LS uses predictive coding followed by Golomb-Rice-style entropy coding and is often a strong baseline for medical image compression. DICOM RLE Lossless, by contrast, is computationally simple but typically offers limited compression because it operates on byte-oriented runs and does not include an effective decorrelation stage.

These standards are important baselines for the present work. The goal of this paper is not to argue that they are obsolete, but to investigate a simpler residual-domain design targeted specifically at high-bit-depth images.

### B. Entropy Coding and High-Bit-Depth Symbol Models

Entropy coding methods such as Huffman coding, arithmetic coding, and ANS can in principle support many alphabet sizes. In practice, however, software implementations often optimize for compact tables, moderate alphabets, and byte-oriented processing. This is especially common in general-purpose compression systems and image codecs originally designed around 8-bit inputs.

Medical images differ in that they naturally contain 10–16 bit sample values. After prediction, the residual stream may still be more naturally modeled as a sequence of 16-bit symbols rather than bytes. This creates a modeling choice: preserve the residual stream as word-level symbols, or transform it into bytes or planes and rely on later processing to recover cross-byte structure.

This paper studies the first option.  
**[Needs work: add direct comparisons to byte-shuffle and bitshuffle preprocessing, since those are strong baselines for multibyte numerical data.]**

### C. ANS and Interleaved Decoding

ANS and its table-based variants provide efficient entropy coding with fast decoding and near-arithmetic-coding efficiency. Prior work has also shown that interleaving or multi-stream organization can improve decoder throughput. The present work therefore does not claim novelty in ANS itself or in interleaving as a general concept. Rather, the contribution lies in applying these ideas in a 16-bit-native medical image codec, together with a specific large-alphabet implementation and throughput-oriented software design.

### D. Positioning of This Work

The present paper is positioned at the intersection of:
- lossless medical image compression,
- large-alphabet residual-domain entropy coding,
- throughput-oriented decoder design,
- and lightweight deployment.

A concise “novelty relative to prior work” table should be added in the final version.  
**[Needs work: add a table explicitly distinguishing this work from JPEG-LS, HTJ2K, interleaved rANS/tANS literature, and scientific-data shuffle pipelines.]**

---

## III. Proposed Method

MIC compresses grayscale images using three stages:
1. spatial prediction,
2. 16-bit run-length encoding, and
3. large-alphabet table-based entropy coding.

This section describes the algorithmic design. Implementation-specific optimizations are deferred to Section IV.

### A. Spatial Prediction

Let \(x_{i,j}\) denote the pixel at row \(i\), column \(j\). For interior pixels, MIC predicts the current pixel from the average of the top and left neighbors:

\[
\hat{x}_{i,j} = \left\lfloor \frac{x_{i-1,j} + x_{i,j-1}}{2} \right\rfloor
\]

and forms the residual

\[
r_{i,j} = x_{i,j} - \hat{x}_{i,j}.
\]

Boundary pixels use only the available neighbor, and the first pixel is stored directly.

This predictor was selected because it is computationally simple, branch-light in decoding, and effective on the current dataset. Preliminary experiments with the MED predictor showed smaller and inconsistent compression gains relative to the additional decoding complexity.

**[Needs work: provide a systematic predictor ablation across the full dataset, including average predictor, MED, and at least one additional predictive baseline.]**

### B. Effective Bit Depth and Overflow Coding

To avoid widening the main residual stream to 32 bits, MIC uses an overflow delimiter scheme derived from the effective image bit depth \(d\):

\[
d = \left\lceil \log_2(\max(x)+1) \right\rceil .
\]

Residuals within an in-range interval are mapped directly to 16-bit codes. Residuals outside that interval are encoded using a delimiter followed by the raw pixel value. The delimiter is chosen as

\[
D = 2^d - 1,
\]

and the direct residual mapping is defined so that the direct-code range and delimiter are disjoint. The decoder can therefore distinguish direct residuals from overflow cases by a single comparison.

This mechanism preserves a 16-bit main stream while handling rare large residuals exactly.

**[Needs work: add pseudocode and a compact proof of non-collision between direct residual codes and the delimiter.]**

### C. 16-Bit Run-Length Encoding

The predicted residual stream is converted into an alternating sequence of:

- **same runs**: a count and a repeated 16-bit value;
- **diff runs**: a count followed by a sequence of distinct 16-bit values.

A same run is emitted only when at least three identical consecutive values are observed. This reduces expansion on short repetitions. Counts are signaled using a midpoint convention: counts on one side of the midpoint indicate same runs, and counts on the other side indicate diff runs.

The design choice of 16-bit-native RLE is motivated by representation. Instead of decomposing repeated residual words into bytes and asking a downstream compressor to infer structure indirectly, MIC preserves repeated 16-bit symbols directly in the coding domain used by the entropy backend.

This should not be interpreted as a claim that byte-oriented compressors cannot compress repeated values. Rather, it is a statement that the proposed pipeline retains word-level structure explicitly and consistently.

### D. Large-Alphabet FSE Coding

The RLE output is entropy-coded using a table-based ANS/FSE backend. The implementation differs from conventional small-alphabet use in two respects:

1. it supports large active symbol sets arising from predicted 16-bit residual streams; and
2. it sizes tables dynamically to the actual observed symbol range rather than the theoretical 16-bit maximum.

This reduces unnecessary working-set size and improves cache behavior when the active residual alphabet is narrow.

### E. Adaptive Table Size Selection

The implementation adjusts `tableLog` based on the number of active symbols and the density of observations per symbol. A default `tableLog = 11` is increased on broader and denser symbol distributions. These heuristics improved compression on several wider-distribution images, especially CR and mammography cases.

At present, these thresholds should be viewed as empirical design choices rather than theoretically optimized settings.

**[Needs work: add an ablation over a larger corpus showing compression gain and decode cost as a function of `tableLog`.]**

### F. Optional Canonical Huffman Backend

An optional canonical Huffman backend was implemented for comparison. In the current experiments it occasionally produced slightly smaller outputs than FSE but decoded more slowly. Since this paper focuses on the ratio-throughput tradeoff, FSE is used as the primary backend.

### G. RGB and Multi-Frame Extensions

MIC also includes:
- **MIC2** for multi-frame grayscale data, and
- **MICR** for single-frame RGB images using a reversible YCoCg-R transform followed by the same core residual pipeline.

These modes are included to demonstrate extensibility, but the main contribution of the paper remains the grayscale 16-bit lossless pipeline.

---

## IV. Multi-State Entropy Decoding

### A. Motivation

A conventional single-state table-based ANS decoder contains a serial dependency chain:

\[
\text{state}_{n+1} = f(\text{state}_n, \text{bits}_n),
\]

which limits the amount of instruction overlap available to the processor. On modern out-of-order CPUs, this may constrain entropy-decoder throughput even when additional execution resources are available.

### B. Interleaved Decoding

To reduce this bottleneck, MIC supports multiple independent decoding states operating on interleaved output positions. In the four-state design, four independent state machines reconstruct positions

- \(0,4,8,\dots\),
- \(1,5,9,\dots\),
- \(2,6,10,\dots\),
- \(3,7,11,\dots\).

Because the chains are independent, the processor can overlap more of the table lookup and bit extraction work.

This is an implementation-level throughput optimization; it does not alter the underlying compressed representation in terms of entropy efficiency. Compression ratio is unchanged.

### C. Correctness

Correctness follows from partitioning the output sequence into independent subsequences during encoding and reversing that assignment during decoding. Each subsequence is coded and decoded using the same FSE machinery as the single-state case.

### D. Stream Signaling

The bitstream includes a small mode signal to identify whether the payload uses single-state, two-state, or four-state decoding. This allows automatic dispatch in the decoder without ambiguity.

### E. Platform-Specific Kernels

The implementation includes hand-optimized paths for AMD64 and ARM64, with pure-Go fallbacks for portability. The optimized paths improve performance but are not required for correctness.

The important distinction for the paper is between:
- the **algorithmic effect** of multi-state decoding, and
- the **additional implementation effect** of assembly-level optimization.

**[Needs work: add a breakdown table isolating the gain from multi-state decoding itself versus the extra gain from architecture-specific kernels.]**

---

## V. Experimental Methodology

### A. Dataset

The current evaluation uses 21 de-identified DICOM images spanning multiple modalities, including MR, CT, CR, X-ray, mammography, nuclear medicine, radiography, secondary capture, and fluoroscopy. The images are drawn from public datasets including NEMA sample collections and the Clunie breast tomosynthesis case.

This dataset spans multiple modalities and image sizes, but remains modest for strong generalization claims.

**[Needs work: expand to a larger multi-institution dataset, e.g., TCIA or a similar public archive, and report Bits Stored, Bits Allocated, Photometric Interpretation, and frame count for each image.]**

### B. Baselines

The current study compares MIC with:
- HTJ2K,
- JPEG-LS,
- and delta + Zstandard.

These baselines were selected because they represent standard medical-image codecs and a strong practical general-purpose compressor.

However, the current baseline set is incomplete for the paper’s central thesis.

**[Needs work: add the following baselines if feasible:]**
- delta + byte-shuffle + Zstandard,
- delta + bitshuffle + Zstandard,
- JPEG XL lossless, if a stable and fair implementation is available,
- a throughput-oriented general-purpose baseline such as LZ4 after shuffle preprocessing.

### C. Metrics

The paper reports:
- compression ratio,
- decompression throughput in MB/s,
- encoding throughput in MB/s,
- and, where useful, MPixels/s.

For the final submission, throughput should be defined uniformly as:

\[
\text{MB/s} = \frac{\text{uncompressed pixel bytes reconstructed}}{\text{measured runtime}}.
\]

**[Needs work: standardize this definition across all tables; the previous draft mixed throughput bases in at least one location.]**

### D. Benchmark Procedure

All codecs were measured using in-process library calls on the same machine to avoid file-I/O and subprocess overhead. The reported platforms are:
- Apple M2 Max (ARM64),
- Intel Core Ultra 9 285K (AMD64).

Compiler flags and implementation variants should be stated explicitly in each table caption or in a benchmark appendix.

**[Needs work: add full benchmark protocol, including warm-up, run count, median/mean policy, variance, and 95% confidence intervals.]**

### E. JavaScript and Browser Evaluation

A pure JavaScript decoder and a Go WebAssembly build were implemented. Current performance measurements were obtained primarily in Node.js, which demonstrates runtime portability and provides an upper-bound indication of client-side feasibility.

However, Node.js `worker_threads` are not equivalent to browser execution.

**[Needs work: add measurements in actual browser environments using Web Workers, and separate Node.js and browser claims explicitly.]**

---

## VI. Results

### A. Compression Ratio

Across the grayscale test images, MIC achieves compression ratios ranging from approximately 1.7× to 8.9×. The strongest ratios occur on images with smooth regions and strongly concentrated residual distributions, such as mammography.

Relative to the current delta + Zstandard baseline, MIC improves compression ratio by 10–22% on the evaluated images. This supports the claim that a 16-bit-native residual pipeline can be beneficial on high-bit-depth medical data.

At the same time, JPEG-LS remains a strong compression-ratio baseline and often achieves the best lossless ratio among the compared codecs. Accordingly, MIC should not be presented as universally best in ratio; its main strength is the ratio-throughput-simplicity tradeoff.

**[Needs work: add a concise main-text summary table showing geometric means and per-codec win counts, and move the full per-image ratio table to an appendix.]**

### B. Effect of 16-Bit-Native Residual Coding

The current experiments support a narrower and more defensible conclusion than the original draft: on the tested dataset, a 16-bit-native pipeline outperformed the specific delta + Zstandard baseline used in this study.

This should not be generalized into a blanket claim that byte-oriented coding is fundamentally inadequate. The observed gain likely arises from the interaction of:
- concentrated residual distributions,
- explicit word-level run-length representation,
- and a large-alphabet entropy backend.

**[Needs work: strengthen this section with the added byte-shuffle and bitshuffle baselines.]**

### C. Effect of Adaptive Table Size

On several broader residual distributions, especially CR and mammography cases, increasing `tableLog` improved compression ratio modestly. This indicates that large-alphabet residual coding benefits from flexible probability quantization.

**[Needs work: add a broader tableLog sensitivity analysis, including decode-time impact.]**

### D. Multi-State Decoding

The two-state and four-state decoders provide clear entropy-decoder throughput improvements over the single-state baseline. The four-state design consistently provides the largest speedup in isolated FSE decoding experiments.

These results support the idea that entropy-decoder organization is an important throughput design axis in lossless codecs, particularly for read-heavy workloads.

**[Needs work: add a figure summarizing 1-state, 2-state, and 4-state speedup across the dataset.]**

### E. Decompression Throughput

On the reported ARM64 platform, the fastest MIC single-threaded variant exceeds HTJ2K on most tested images, while strip-parallel PICS exceeds HTJ2K on all tested images in the current dataset. On AMD64, the optimized MIC variants also perform strongly, although HTJ2K remains faster on selected images.

These results indicate that MIC is competitive from a decode-throughput standpoint, especially when multi-state decoding and strip-level parallelism are enabled.

For the final paper, it would be preferable to emphasize summary trends rather than many large per-image tables in the main text.

**[Needs work: move detailed ARM64 and AMD64 per-image throughput tables to an appendix and retain only summary tables in the main paper.]**

### F. Encoding Throughput

The C/CGO and strip-parallel MIC variants also achieve high encoding throughput. This is useful for ingestion-heavy workflows, although the main motivation of the paper is fast decompression in read-dominant systems.

Because encoding performance depends more strongly on implementation maturity, the paper should present encoding-speed results as implementation-specific rather than as purely algorithmic conclusions.

### G. JavaScript Runtime Results and Deployment Feasibility

A pure JavaScript decoder and a WebAssembly implementation were developed. In Node.js, the JavaScript decoder reconstructs images at useful throughput, and parallel execution improves performance on large images. These results suggest that the codec is lightweight enough for client-side implementation.

However, the revised paper should make a narrower claim:
- the codec is **portable to JavaScript environments**,
- and appears suitable for client-side deployment,
- but full browser performance remains to be validated directly.

**[Needs work: add real browser measurements using Chrome, Firefox, and Safari, preferably with Web Workers.]**

### H. Multi-Frame and RGB Results

The MIC2 multi-frame mode and MICR RGB mode produced encouraging results on the evaluated examples, including breast tomosynthesis and selected US/VL images. These results demonstrate extensibility of the coding framework, but they remain secondary to the main grayscale contribution.

**[Needs work: either move these studies to a short appendix, or strengthen them with broader datasets and stronger baselines.]**

---

## VII. Discussion

### A. Why a Simple Residual Pipeline Can Be Effective

A practical conclusion from the current study is that simple prediction can already remove much of the redundancy in many medical images. When the residual stream becomes concentrated near zero and repeated values are common, a direct residual-domain representation can be highly effective.

This should be framed as an empirical result rather than a universal principle. More complex predictors, transforms, or context models may still be preferable in other settings, especially when ratio is the dominant objective.

### B. Comparison with Wavelet-Based Coding

A wavelet front end was also implemented and performed well on several images. It should not be dismissed; indeed, it may be a more suitable basis for future lossy or progressive extensions.

The current evidence suggests the following practical interpretation:

- **Delta + 16-bit RLE + FSE** is an attractive default when simplicity and fast decoding are prioritized.
- **Wavelet + FSE** may provide higher ratio on some images with strong spatial correlation.
- **HTJ2K** remains a strong and important standard benchmark.
- **JPEG-LS** remains highly competitive on compression ratio.

### C. Complexity and Deployability

One practical advantage of the MIC pipeline is compact implementation size and a relatively straightforward decode path. This may matter for maintenance, auditability, and deployment in constrained environments.

That said, line-count comparisons should be treated as secondary engineering observations rather than scientific evidence.

### D. Limitations

This work has several limitations.

1. The dataset is multi-modality but still modest in size.
2. Byte-shuffle and bitshuffle baselines are not yet included.
3. Browser evaluation is incomplete; Node.js is not a substitute for browser benchmarks.
4. Predictor selection is supported only by preliminary ablation.
5. Some wavelet-related explanations remain empirically suggestive rather than conclusively established.
6. No PACS integration or clinical workflow study is included.

---

## VIII. Conclusion

This paper presented MIC, a lossless medical image compression pipeline based on spatial prediction, 16-bit-native run-length representation, and large-alphabet table-based entropy coding. The results show that, on the evaluated dataset, this design improves over a delta + Zstandard baseline while remaining competitive with established medical image codecs in decompression throughput.

A multi-state interleaved entropy decoder substantially improves decode speed without changing compressed size, indicating that decoder organization is an important practical design dimension for lossless codecs. The implementation is also compact enough to support JavaScript and WebAssembly deployment, suggesting feasibility for lightweight client-side decoding.

Overall, the study indicates that 16-bit-native entropy coding is a useful design point for lossless medical image compression, particularly in applications where decompression throughput, implementation simplicity, and deployment portability are important. Future work should strengthen the evidence base with larger datasets, shuffle-based general-purpose baselines, formal statistical reporting, and direct browser benchmarking.

---

# APPENDIX / MATERIAL TO ADD BEFORE SUBMISSION

## A. Tables to Keep, But Move Out of the Main Narrative

You already have a lot of useful data. For a submission-ready IEEE version, I would recommend:

### Keep in main paper
- one dataset summary table,
- one compression-ratio summary table,
- one decompression-speed summary table,
- one isolated 1-state/2-state/4-state table,
- one small deployment/runtime table.

### Move to appendix or supplementary
- full per-image grayscale compression table,
- full ARM64 decompression table,
- full AMD64 decompression table,
- full ARM64 encoding table,
- full AMD64 encoding table,
- full RGB and multi-frame tables.

**[Needs work: renumber all tables sequentially and verify every textual claim against the final table values.]**

---

## B. Figures You Should Add

1. **Pipeline block diagram**  
   Raw pixels → prediction → 16-bit RLE → FSE

2. **Residual histogram figure**  
   One or two modalities showing strong concentration near zero

3. **Speedup figure**  
   1-state vs 2-state vs 4-state

4. **Pareto plot**  
   Compression ratio vs decompression throughput for MIC, Wavelet, HTJ2K, JPEG-LS, delta+zstd

5. **Optional deployment figure**  
   Decoder size / environment / runtime stack

**[Needs work: actual figures are needed; tables alone will make the paper feel overly implementation-heavy.]**

---

## C. Specific [Needs work] Items Before Submission

### Experiments
- [Needs work] Add **delta + byte-shuffle + zstd**
- [Needs work] Add **delta + bitshuffle + zstd**
- [Needs work] Add broader dataset, ideally multi-institution
- [Needs work] Add confidence intervals / variance bars
- [Needs work] Add predictor ablation
- [Needs work] Add tableLog ablation
- [Needs work] Add browser benchmarks using Web Workers
- [Needs work] If deployment remains central, add an end-to-end viewer latency experiment

### Writing/structure
- [Needs work] Replace all remaining promotional phrasing
- [Needs work] Ensure all “best on X/Y images” claims match the final tables exactly
- [Needs work] Standardize variant names: MIC, MIC-4S, MIC-W, PICS, MICR, MIC2
- [Needs work] Define throughput consistently everywhere

### References
- [Needs work] Add references on shuffle/bitshuffle/scientific array compression
- [Needs work] Replace website-only evidence with archival citations where possible
- [Needs work] Keep GitHub repositories only as software citations, not scientific support
- [Needs work] Remove or reframe unsupported “no browser decoder exists” claims unless documented carefully

---

# Suggested Title Alternatives

If you want a more IEEE-journal-safe title, I recommend one of these:

1. **A 16-Bit-Native Entropy Coding Pipeline for Lossless Medical Image Compression**
2. **Lossless Compression of High-Bit-Depth Medical Images Using 16-Bit-Native Residual Coding**
3. **Large-Alphabet Residual Entropy Coding for Lossless Medical Image Compression**
4. **A High-Throughput 16-Bit-Native Codec for Lossless Medical Imaging**
