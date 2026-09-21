# A 16-Bit-Native Entropy Coding Pipeline for Lossless Medical Image Compression

**Kuldeep Singh**  
Innovaccer Inc. | kuldeep.singh@innovaccer.com  
Code: https://github.com/pappuks/medical-image-codec

---

## Abstract

Lossless compression remains important in medical imaging because clinical workflows require exact pixel reconstruction for archival, transmission, and diagnostic display. Many medical images are stored at 10–16 bits per sample, yet many practical compression back ends are optimized for byte-oriented or relatively small-alphabet symbol models. This paper investigates a 16-bit-native lossless coding pipeline for such data.

We present MIC, a lossless codec for medical images that combines (i) spatial prediction, (ii) a 16-bit run-length representation of residuals, and (iii) an extended table-based asymmetric numeral system implementation supporting large symbol alphabets. We also study a multi-state interleaved decoder that increases instruction-level parallelism without changing the compressed representation. The work is motivated by the observation that residual streams derived from medical images frequently exhibit strong concentration near zero and substantial word-level redundancy after prediction.

Across 21 de-identified DICOM images spanning multiple modalities, MIC achieves lossless compression ratios between 1.7× and 8.9× on grayscale images. Relative to a delta + Zstandard baseline, the proposed 16-bit-native pipeline improves compression ratio by 10–22% on the tested images. A four-state decoder provides substantial decompression speedup over a single-state implementation, and on the reported ARM64 and AMD64 platforms the fastest MIC variants are competitive with, and in many cases faster than, HTJ2K in single-threaded and strip-parallel settings. In addition, a JavaScript implementation demonstrates that the core pipeline is compact enough for client-side decoding.

The results suggest that 16-bit-native entropy coding is a useful design point for lossless medical image compression, especially when decode throughput and implementation simplicity are prioritized.  
**[Needs work: add additional baselines using byte-shuffle/bitshuffle-style preprocessing with byte-oriented compressors, and report confidence intervals for all throughput results.]**

---

## Index Terms

Medical image compression, lossless compression, entropy coding, asymmetric numeral systems, finite state entropy, DICOM, high-bit-depth imaging.

---

## 1. Introduction

Medical imaging systems generate large volumes of high-bit-depth data. Modalities such as CT, MR, radiography, mammography, and tomosynthesis commonly store grayscale pixel data at 10, 12, or 16 bits per sample. In picture archiving and communication systems (PACS), these images must often be compressed losslessly for archival storage, transmission, and interactive viewing.

DICOM supports several established lossless compression options, including JPEG 2000, HTJ2K, JPEG-LS, and RLE Lossless. These methods represent different tradeoffs among compression ratio, decoding complexity, and software availability. JPEG 2000 and HTJ2K provide strong compression performance but require comparatively complex transform and block-coding pipelines. JPEG-LS provides attractive ratios on many medical images with a simpler predictive framework, but throughput and deployment tradeoffs remain important in read-heavy workflows.

This paper examines a different design point: a codec tailored to **16-bit residual symbol streams** rather than primarily to byte streams or transform coefficients. The motivation is practical rather than doctrinal. After simple spatial prediction, many medical images produce residuals that are sharply concentrated around zero and contain long runs of repeated values. A coding pipeline that preserves the residual stream as 16-bit symbols can exploit this structure directly using word-level run-length encoding and large-alphabet entropy coding.

The proposed codec, MIC, uses three stages:

1. a simple spatial predictor,
2. a 16-bit-native run-length representation of the residual stream, and
3. an extended FSE/tANS entropy coder capable of handling large active alphabets.

In addition, MIC includes a multi-state interleaved decoder intended to expose instruction-level parallelism (ILP) during entropy decoding. The paper evaluates this design against JPEG-LS, HTJ2K, and a delta + Zstandard baseline, and briefly studies JavaScript decoding feasibility for client-side use.

### 1.1 Motivation and Scope

The focus of this work is **lossless grayscale medical image compression**. The main questions are:

- Can a 16-bit-native entropy coding pipeline improve over a byte-oriented baseline on predicted medical image residuals?
- Can a multi-state decoder improve throughput without affecting compressed size?
- How does this design compare with JPEG-LS and HTJ2K on compression ratio and decode speed?
- Is the implementation compact enough to support lightweight client-side decoding?

This is not a paper about lossy compression, perceptual quality, or progressive transmission. It also does not claim to replace DICOM-standard codecs in all settings. Rather, it studies one underexplored point in the design space for lossless coding of high-bit-depth medical images.

### 1.2 Main Contributions

The contributions of this paper are as follows.

1. **A 16-bit-native residual coding pipeline for medical images.**  
   We present a Delta + 16-bit RLE + extended FSE pipeline that codes predicted residuals as 16-bit symbols rather than splitting them into bytes.

2. **An extended table-based entropy coder for large active alphabets.**  
   The implementation supports large symbol ranges encountered in 10–16 bit medical residual streams while sizing working data structures to the effective symbol range.

3. **A multi-state interleaved decoder for improved ILP.**  
   We describe and evaluate a two-state and four-state interleaving strategy for FSE decoding and show substantial decode speedup with no compression-ratio penalty.

4. **An empirical comparison with established codecs and a general-purpose baseline.**  
   We compare against HTJ2K, JPEG-LS, and delta + Zstandard on a multi-modality dataset. We also provide an initial JavaScript implementation demonstrating deployment feasibility.

### 1.3 Paper Organization

Section 2 reviews related work and positions the contribution. Section 3 describes the coding pipeline. Section 4 presents the multi-state decoding design. Section 5 describes the experimental methodology. Section 6 reports results. Section 7 discusses implications and limitations. Section 8 concludes.

---

## 2. Related Work and Positioning

### 2.1 Lossless Compression in DICOM

Lossless medical image compression in DICOM is dominated by a small number of practical families.

**JPEG 2000** uses a reversible 5/3 integer wavelet transform followed by embedded block coding. It is a strong baseline for lossless ratio but involves a relatively complex transform and coding structure.

**HTJ2K** reduces some of the throughput limitations of classic JPEG 2000 while preserving JPEG 2000 compatibility.

**JPEG-LS** uses predictive coding and Golomb-Rice-style entropy coding. It often performs strongly on medical imagery, especially in smooth or moderately textured cases.

**DICOM RLE Lossless** is simple but generally provides lower compression ratios because it uses byte-oriented run-length coding without a decorrelation stage.

The present work does not seek to dispute the value of these standards. Instead, it investigates a different design point centered on large-alphabet residual coding and implementation simplicity.

### 2.2 Entropy Coding for High-Bit-Depth Data

Traditional entropy coding methods such as Huffman coding, arithmetic coding, and ANS can all be used with varying alphabet sizes, but practical implementations are often optimized around moderate symbol alphabets and compact tables. In widely used software systems, entropy coding back ends are frequently embedded within byte-oriented pipelines or are tuned for low- to medium-cardinality symbol models.

In high-bit-depth scientific and medical data, however, each sample may naturally be represented as a 10–16 bit word. This creates a modeling choice:

- treat each sample as a word-level symbol, or
- transform the stream into bytes or planes and rely on downstream compression to recover structure.

Both choices are legitimate. This paper studies the first option.

**[Needs work: add a dedicated comparison against byte-shuffle and bitshuffle preprocessing, since these are standard ways to improve byte-oriented compression on multibyte numerical data.]**

### 2.3 ANS, FSE, and Interleaving

Asymmetric numeral systems (ANS) and table-based ANS/FSE provide near-arithmetic-coding efficiency with efficient decoding. Prior work has also explored **interleaving** or **multi-stream** entropy decoding to increase throughput. The present work is therefore not claiming the invention of interleaved ANS as a concept. Rather, the contribution is the application and engineering of multi-state interleaving in a 16-bit medical image codec built around large-alphabet residual coding.

### 2.4 Positioning of This Work

To clarify novelty, Table 1 summarizes how this paper is positioned relative to prior work.

| Area | Prior Work | Limitation in Current Context | This Work |
|---|---|---|---|
| Lossless medical image compression | JPEG-LS, JPEG 2000, HTJ2K | Strong baselines, but different complexity/throughput tradeoffs | Studies a simple residual pipeline targeted at high-bit-depth images |
| ANS/FSE coding | Existing ANS/FSE literature and implementations | Practical table sizing and symbol limits may not target 16-bit residual alphabets | Extends FSE-style coding to large active alphabets in this application |
| Interleaved ANS decoding | Prior rANS/tANS interleaving work | Not specifically studied here for medical residual streams and this software stack | Evaluates 2-state/4-state decoding in this codec |
| Client-side deployment | Limited lightweight browser-ready support for medical lossless codecs | Integration burden for web viewing | Provides compact JavaScript implementation as feasibility demonstration |

**[Needs work: strengthen this section with more precise prior-art comparison and additional references on scientific-data compression, shuffle filters, and large-alphabet entropy coding.]**

---

## 3. Proposed Method

MIC compresses grayscale 16-bit images using a three-stage pipeline: prediction, 16-bit run-length representation, and entropy coding. Figure 1 illustrates the processing flow.

**[Needs work: replace the current ASCII diagram with an IEEE-quality figure.]**

### 3.1 Prediction Stage

Let \(x_{i,j}\) denote the pixel at row \(i\), column \(j\). For interior pixels, the predictor used in the main MIC pipeline is

\[
\hat{x}_{i,j} = \left\lfloor \frac{x_{i-1,j} + x_{i,j-1}}{2} \right\rfloor
\]

and the residual is

\[
r_{i,j} = x_{i,j} - \hat{x}_{i,j}.
\]

Boundary pixels use only available neighbors, and the first pixel is stored directly.

This predictor is intentionally simple. In the tested images, it produced residuals concentrated near zero and enabled a low-branch decode path. More complex predictors, such as MED, were explored in preliminary experiments, but the gains were small relative to the added control complexity.

**[Needs work: provide a systematic ablation table comparing the average predictor against MED and, if feasible, one stronger predictive baseline.]**

#### Overflow Representation

Residuals may exceed the range conveniently represented by the effective image bit depth. To avoid widening the main residual stream to 32 bits, the implementation uses an overflow delimiter driven by the effective bit depth \(d\):

- residuals within a bounded in-range interval are stored directly as encoded 16-bit values,
- out-of-range residuals are emitted as a delimiter followed by the raw pixel value.

This preserves a 16-bit main stream while handling exceptions exactly. The delimiter mapping is constructed so that in-range residual codes and the delimiter are disjoint.

**[Needs work: add concise pseudocode for encoding and decoding the overflow convention.]**

### 3.2 16-Bit Run-Length Representation

After prediction, many images produce repeated residual values, especially zero. MIC therefore converts the residual stream into an alternating sequence of:

- **same runs**: count + repeated value,
- **diff runs**: count + a sequence of distinct values.

The representation is word-native: counts and symbols are handled at 16-bit granularity. This differs from byte-oriented run-length treatment in that repeated 16-bit values remain explicit as repeated words.

A same run is emitted only when at least three consecutive equal symbols are observed. This reduces the chance that run headers expand short repetitions.

The midpoint-count convention is used to distinguish same runs from diff runs.

The main purpose of this stage is not to claim that byte-oriented compressors cannot compress repeated values, but rather that **a word-native representation preserves residual structure directly in the coding domain chosen for the entropy coder**.

### 3.3 Extended FSE for Large Active Alphabets

The final stage entropy-codes the run-length output using table-based ANS/FSE. The implementation differs from typical small-alphabet use in two ways.

1. It supports large active symbol ranges encountered in predicted 16-bit residual streams.
2. It sizes tables dynamically to the actual observed symbol range rather than always allocating for the theoretical maximum.

This matters because the active alphabet is often much smaller than the full 16-bit range, especially for lower effective bit depth images or strongly concentrated residual distributions.

#### Adaptive Table Size

The implementation adjusts `tableLog` using heuristics based on:

- the number of active symbols, and
- the average support density.

These thresholds improved compression on several wider-distribution images in the present dataset.

**[Needs work: add a broader ablation study showing the effect of `tableLog` across a larger corpus and justify the thresholds more systematically.]**

### 3.4 Optional Canonical Huffman Backend

A canonical Huffman mode was also implemented as an alternative entropy backend. In the current experiments it occasionally gave slightly smaller files, but decoded more slowly than FSE. For this reason, FSE is the main backend considered in the remainder of the paper.

### 3.5 Container Variants

The implementation includes several containers:

- **MIC1**: single-frame grayscale
- **MIC2**: multi-frame grayscale
- **MICR**: single-frame RGB

These containers package the same core coding stages with modest metadata. In this paper, the focus is the core grayscale coding method; RGB and multi-frame results are reported as secondary studies.

---

## 4. Multi-State Entropy Decoding

### 4.1 Motivation

A conventional single-state table-based decoder has a serial dependency chain: the state update for the next symbol depends on the current state. On modern out-of-order processors, this limits the extent to which independent instructions can be scheduled.

The MIC decoder therefore supports **interleaved decoding with multiple independent state machines**, each responsible for a subset of output positions. The compressed representation is organized so that symbols for different interleaved positions can be reconstructed from separate state streams.

### 4.2 Two-State and Four-State Decoding

In the four-state design, four independent state variables decode symbols in an interleaved pattern:

- stream A reconstructs positions 0, 4, 8, ...
- stream B reconstructs positions 1, 5, 9, ...
- stream C reconstructs positions 2, 6, 10, ...
- stream D reconstructs positions 3, 7, 11, ...

Because each chain has no data dependency on the others, the processor can overlap more of the work.

This should be interpreted as an **implementation-level throughput optimization** rather than a new coding theorem. The compression ratio is unchanged; only the decode organization differs.

### 4.3 Practical Stream Signaling

The bitstream carries a small mode signal identifying whether the stream is single-state, two-state, or four-state. The decoder dispatches accordingly. This allows backwards-compatible coexistence of multiple implementations within the same container family.

### 4.4 Platform-Specific Kernels

The implementation includes platform-specific kernels for AMD64 and ARM64, plus pure-Go fallbacks. On AMD64, BMI2 instructions are used in the optimized path; on ARM64, the scalar/vector instruction set already supports the needed variable shift operations without the same register constraint.

These optimizations improve throughput but are not required for correctness. For scientific clarity, the paper distinguishes:

- **algorithmic gain** from multi-state decoding,
- and **additional implementation gain** from platform-specific assembly.

**[Needs work: add a breakdown separating (i) single-state vs four-state in pure C/Go and (ii) four-state scalar vs four-state assembly/SIMD.]**

---

## 5. Experimental Methodology

### 5.1 Dataset

The current evaluation uses 21 de-identified DICOM images spanning MR, CT, CR, radiography/X-ray, mammography, nuclear medicine, secondary capture, and XA/fluoroscopy. The images are drawn from public datasets, including NEMA sample collections and the Clunie tomosynthesis case.

This dataset covers multiple modalities and image sizes, but it remains modest for a journal-level claim of broad generality.

**[Needs work: expand the study with a larger multi-institution dataset, e.g., TCIA or another public archive, and report image metadata including Bits Stored, Bits Allocated, Photometric Interpretation, and frame count.]**

### 5.2 Baselines

The reported baselines are:

- HTJ2K (OpenJPH),
- JPEG-LS (CharLS),
- delta + Zstandard.

The current paper keeps these baselines because they represent important points in the design space: standards-based lossless medical coding and a practical general-purpose compressor.

However, the baseline set is incomplete for the central thesis of this paper.

**[Needs work: add at least the following baselines if feasible:]**
- **delta + byte-shuffle + Zstandard**  
- **delta + bitshuffle + Zstandard**  
- **JPEG XL lossless** if a stable and fair implementation path is available  
- **[Optional]** LZ4 or Brotli after shuffling, as a throughput-oriented general-purpose baseline

### 5.3 Metrics

Compression performance is reported using:

- **compression ratio** \(=\frac{\text{raw size}}{\text{compressed size}}\),
- **bits per pixel**,
- **decompression throughput** in MB/s,
- and, where useful, MPixels/s.

In the revised version, MB/s should be defined uniformly as:

\[
\text{MB/s} = \frac{\text{uncompressed pixel bytes reconstructed}}{\text{decode time}}
\]

for all codec comparisons.

**[Needs work: ensure this exact throughput definition is applied consistently across all tables and figure captions; the previous draft mixed different throughput bases in at least one table.]**

### 5.4 Benchmarking Procedure

All codecs were benchmarked in-process on the same machine to reduce file-I/O and subprocess noise. MIC and competitor libraries were invoked through the same benchmark harness.

Platform summaries:
- **ARM64**: Apple M2 Max
- **AMD64**: Intel Core Ultra 9 285K

Compiler settings and implementation variants should be reported explicitly for each table.

**[Needs work: add full benchmark protocol, including run count, warm-up strategy, median vs mean reporting, CPU frequency policy, and 95% confidence intervals.]**

### 5.5 JavaScript and Browser Evaluation

A JavaScript implementation of the MIC decoder was evaluated in Node.js. This demonstrates portability of the codec logic and provides a useful proxy for client-side feasibility.

However, Node.js `worker_threads` are not the same as browser execution.

Accordingly, this paper now distinguishes:
- **JavaScript runtime performance (Node.js)**, and
- **browser deployment feasibility**.

**[Needs work: add actual browser measurements using Web Workers in at least one major browser, and separate them clearly from Node.js results.]**

---

## 6. Results

### 6.1 Compression Ratio

Across the tested grayscale images, MIC achieves compression ratios ranging from approximately 1.7× to 8.9×. The strongest ratios occur on mammography and other smooth images with long near-zero residual runs after prediction.

Relative to the current delta + Zstandard baseline, MIC improves compression ratio by 10–22% on the test set. This indicates that preserving and coding the residual stream at 16-bit granularity can be beneficial on these images.

At the same time, JPEG-LS remains a strong ratio baseline and often produces the best lossless ratio among the compared codecs. Therefore, the main case for MIC is not “best ratio in all scenarios,” but a balance of ratio, decode throughput, and implementation simplicity.

A wavelet-based front end was also implemented and, on some images, improved ratio relative to the delta predictor. On others—particularly some full-dynamic-range CT images—the delta pipeline performed better.

**[Needs work: include a concise summary table in the main text—e.g., wins/losses and geometric means—and move the full per-image table to an appendix.]**

#### Suggested Main-Text Summary Table

| Codec / Variant | Approx. Geo. Mean Ratio | Notes |
|---|---:|---|
| MIC (Delta + 16-bit RLE + FSE) | 3.12× | Main proposed pipeline |
| Wavelet + FSE | 3.28× | Better on several smooth images, weaker on some high-dynamic-range cases |
| HTJ2K | 3.15× | Strong standard baseline |
| JPEG-LS | 3.44× | Strongest ratio baseline in this study |
| Delta + Zstd | 2.72× | Practical general-purpose baseline |

**[Needs work: verify all geometric means after standardizing dataset membership and variant inclusion.]**

### 6.2 Effect of the 16-Bit-Native Pipeline

The present results support the following narrower conclusion:

- on the evaluated dataset, a 16-bit-native residual pipeline outperformed the tested delta + Zstandard baseline.

This should not be generalized into a blanket statement that byte-oriented compression is fundamentally unsuitable for medical imaging. Rather, the observed advantage appears to arise from the interaction of:

- residual concentration,
- word-level repetition,
- a run-length representation matched to that symbol domain,
- and a large-alphabet entropy backend.

**[Needs work: strengthen this section with the added shuffle-based baselines, since they directly test whether preprocessing can recover most of the current MIC advantage.]**

### 6.3 Multi-State Decoding

The two-state and four-state decoders improve isolated entropy-decoder throughput substantially relative to the single-state design. The observed speedups are consistent with the intuition that multiple independent state chains expose additional instruction-level parallelism.

A representative summary is shown below.

| States | Amortized Dependency Depth | Empirical Trend |
|---|---|---|
| 1 | baseline | reference |
| 2 | reduced | moderate gain |
| 4 | further reduced | largest gain |

In the current experiments, the four-state design achieved substantial isolated FSE speedup without affecting compressed size.

This is a useful result because the entropy backend is often on the critical path of decode throughput.

**[Needs work: add a compact figure showing single-state vs 2-state vs 4-state speedup across the dataset, and separate microarchitecture-specific gains from general algorithmic gains.]**

### 6.4 Decompression Throughput

On ARM64, the fastest single-threaded MIC variant exceeded HTJ2K on most tested images, and strip-parallel PICS exceeded HTJ2K on all tested images. On AMD64, the optimized MIC variant also performed strongly, though HTJ2K remained faster on selected images.

These results indicate that the proposed pipeline is competitive from a throughput standpoint, especially when the four-state decoder and strip parallelism are enabled.

At the same time, the results should be presented carefully:

- throughput depends on implementation language and optimization level,
- not all variants are directly comparable in engineering maturity,
- and platform-specific kernels contribute nontrivially to the best-case numbers.

For this reason, the revised paper recommends emphasizing:
1. **relative trends**, and
2. **best validated implementation pairs**,  
rather than using every per-image throughput number as a central claim in the main text.

**[Needs work: move detailed ARM64/AMD64 per-image tables to the appendix and add shorter summary tables in the main paper.]**

### 6.5 Encoding Throughput

MIC encoding is also fast, especially in the C/CGO and strip-parallel variants. For ingestion-heavy workflows this is useful, although the primary deployment motivation of this paper remains decode throughput in read-dominant systems.

Because some codec implementations are more mature than others, encoding-speed conclusions should be presented as implementation-specific rather than universal.

### 6.6 JavaScript Runtime Results and Browser Feasibility

A pure JavaScript decoder and a Go WebAssembly build were implemented. In Node.js, the JavaScript decoder reconstructed images at useful throughput, and parallel execution using `worker_threads` further improved throughput on large images.

These results support the statement that the codec is **portable to JavaScript environments** and is compact enough for lightweight deployment.

However, the revised manuscript no longer equates Node.js `worker_threads` with browser execution. The correct claim is narrower:

- the JavaScript implementation demonstrates **client-side implementation feasibility**,
- while true browser performance requires explicit measurement with Web Workers and real browser runtimes.

**[Needs work: add Chrome/Safari/Firefox browser benchmarks using Web Workers and report download, startup, and decode times separately.]**

### 6.7 Multi-Frame and RGB Studies

The MIC2 and MICR variants were evaluated on a smaller set of multi-frame and RGB examples. These results are encouraging, particularly for tomosynthesis and selected RGB ultrasound/visible-light images, but they remain secondary to the main grayscale study.

**[Needs work: move these results to a short subsection or appendix unless the paper is expanded to include stronger RGB and temporal baselines.]**

---

## 7. Discussion

### 7.1 Why the Simple Residual Pipeline Can Work Well

A practical takeaway of the experiments is that simple predictive residual coding can already capture much of the lossless redundancy in many medical images. When the residual distribution becomes strongly concentrated around zero and repeated values occur frequently, the downstream entropy problem becomes comparatively simple.

This does not imply that wavelets or more sophisticated predictors are unimportant. Rather, it suggests that under a **lossless, throughput-sensitive** objective, the marginal ratio gain from added transform or context complexity may be modest on some modalities.

The conclusion should therefore be stated as an empirical observation from the present study, not as a universal thesis.

### 7.2 Comparison with Wavelet-Based Coding

The wavelet front end performed well on several images and should not be dismissed. In fact, it may be a better foundation for future lossy or progressive extensions.

The current data suggest the following balanced view:

- **Delta + 16-bit RLE + FSE** is an attractive default when simplicity and fast decoding are primary goals.
- **Wavelet + FSE** may provide better ratio on some images, especially those with high spatial correlation and manageable coefficient ranges.
- **HTJ2K** remains a strong and important standard baseline.
- **JPEG-LS** remains highly competitive, especially on pure ratio.

### 7.3 Complexity and Deployability

One practical advantage of the proposed pipeline is compact implementation size and a relatively direct decode path. This can matter for maintenance, auditability, and portability.

However, implementation line count should be treated only as a secondary engineering observation, not as a scientific metric.

The JavaScript implementation suggests that the codec may be suitable for client-side viewing pipelines where lightweight deployment is valuable.

**[Needs work: if browser deployment remains a major selling point, add a short system-level experiment showing end-to-end latency in a realistic web-viewer scenario.]**

### 7.4 Limitations

This work has several limitations.

1. **Dataset size and diversity.**  
   The dataset is useful but still limited for strong generalization claims.

2. **Baseline completeness.**  
   Shuffle-based preprocessing with byte-oriented compressors was not yet evaluated.

3. **Browser evaluation.**  
   Current JavaScript performance data are from Node.js, not full browser measurements.

4. **Predictor ablation.**  
   More systematic study is needed to justify predictor choice across modalities.

5. **Wavelet analysis.**  
   The explanation for weaker wavelet performance on some CT images is plausible, but requires broader validation.

6. **Clinical workflow claims.**  
   The paper does not include viewer studies, PACS integration studies, or clinical-reader evaluations.

---

## 8. Conclusion

This paper presented MIC, a lossless medical image compression pipeline based on simple spatial prediction, 16-bit-native run-length representation, and large-alphabet table-based entropy coding. The central result is that, on the evaluated dataset, this design improves over a delta + Zstandard baseline while remaining competitive in decode throughput with established medical image codecs.

A four-state interleaved decoder substantially improves entropy-decoder throughput without changing compressed size, showing that decode organization is an important practical design axis for lossless codecs. The implementation also appears compact enough to support JavaScript deployment, although full browser benchmarking remains future work.

Overall, the study suggests that **16-bit-native entropy coding is a useful and practically relevant design point for lossless medical image compression**, particularly in scenarios where decompression speed and lightweight implementation are important. Future work should strengthen the evidence base through larger datasets, shuffle-based general-purpose baselines, standardized statistical reporting, and true browser-side benchmarks.

---

## Appendix A. Items to Reinsert from the Original Draft After Cleanup

**[Needs work]** The following materials from the previous draft should be retained, but moved to appendices or supplementary material after consistency checking:

1. Full per-image compression-ratio table  
2. Full ARM64 decompression table  
3. Full AMD64 decompression table  
4. Full ARM64 encoding table  
5. Full AMD64 encoding table  
6. Isolated FSE 1-state/2-state/4-state table  
7. Multi-frame and RGB detailed tables

Before reinsertion, please:
- renumber all tables sequentially,
- standardize the throughput definition in every caption,
- verify every textual claim against the final table values,
- and ensure each table clearly states raw size basis and implementation variant.

---

## Appendix B. Recommended Revision Checklist Before IEEE Submission

**[Needs work]**

### Technical
- Add byte-shuffle and bitshuffle baselines
- Add predictor ablation table
- Add broader `tableLog` ablation
- Add larger multi-institution dataset
- Add confidence intervals and exact benchmark protocol

### Writing
- Ensure title, abstract, and conclusions remain neutral
- Remove any remaining absolute claims such as “all coders assume bytes”
- Replace “browser-native” with precise wording unless browser benchmarks are added
- Shorten implementation advocacy and emphasize validated findings

### Editorial
- Fix table numbering and section cross-references
- Standardize codec variant names
- Add pseudocode for the core codec stages
- Replace the ASCII flowchart with a proper figure

### References
- Add references on:
  - byte shuffling / bitshuffle,
  - scientific-data compression,
  - additional lossless medical image compression surveys,
  - browser/web medical imaging deployment if kept
- Replace website-only assertions with citable papers/standards where possible
- Format all references in IEEE style consistently

---

## References

Below is a cleaned **working reference list** based on the original draft. It still requires final IEEE-format verification and a few additions.

1. NEMA, *Digital Imaging and Communications in Medicine (DICOM)*.  
   **[Needs work: cite specific DICOM standard parts relevant to transfer syntaxes and de-identification.]**

2. D. S. Taubman and M. W. Marcellin, *JPEG2000: Image Compression Fundamentals, Standards and Practice*. Springer, 2002.

3. D. S. Taubman, “High throughput JPEG 2000 (HTJ2K): Algorithm, performance evaluation, and potential,” in *Proc. SPIE*, vol. 11137, 2019.

4. M. J. Weinberger, G. Seroussi, and G. Sapiro, “The LOCO-I lossless image compression algorithm: Principles and standardization into JPEG-LS,” *IEEE Trans. Image Process.*, vol. 9, no. 8, pp. 1309–1324, 2000.

5. ISO/IEC 14495-2:2003, *Lossless and near-lossless compression of continuous-tone still images—Extensions*.

6. J. Alakuijala et al., “JPEG XL next-generation image compression architecture and coding tools,” in *Proc. SPIE 11137*, 2019.  
   **[Needs work: ensure this reference is used precisely and not as evidence for unsupported comparative claims.]**

7. J. Duda, “Asymmetric numeral systems,” arXiv:0902.0271, 2009.

8. J. Duda, “Asymmetric numeral systems: Entropy coding combining speed of Huffman coding with compression rate of arithmetic coding,” arXiv:1311.2540, 2013.

9. Y. Collet, “Finite State Entropy—A new breed of entropy coder,” 2013.  
   **[Needs work: replace or supplement with a more archival citation if available.]**

10. Y. Collet and M. Kucherawy, “Zstandard compression and the ‘application/zstd’ media type,” RFC 8878, 2021.

11. E. S. Schwartz and B. Kallick, “Generating a canonical prefix encoding,” *Commun. ACM*, vol. 7, no. 3, pp. 166–169, 1964.

12. S. Mahapatra and K. Singh, “An FPGA-based implementation of multi-alphabet arithmetic coding,” *IEEE Trans. Circuits Syst. I*, vol. 54, no. 8, pp. 1678–1686, 2007.

13. D. Kosolobov, “Efficiency of ANS entropy encoders,” arXiv:2201.02514, 2022.

14. R. Bamler, “Understanding entropy coding with asymmetric numeral systems (ANS): A statistician’s perspective,” arXiv:2201.01741, 2022.

15. F. Giesen, “Interleaved entropy coders,” arXiv:1402.3392, 2014.

16. F. Giesen, “ryg_rans: Public domain rANS encoder/decoder,” 2014.  
   **[Needs work: use only as implementation reference, not primary scientific evidence.]**

17. F. Lin, K. Arunruangsirilert, H. Sun, and J. Katto, “Recoil: Parallel rANS decoding with decoder-adaptive scalability,” in *Proc. ICPP*, 2023.

18. A. Weissenberger and B. Schmidt, “Massively parallel ANS decoding on GPUs,” in *Proc. ICPP*, 2019.

19. X. Wu and N. Memon, “Context-based, adaptive, lossless image coding,” *IEEE Trans. Commun.*, vol. 45, no. 4, pp. 437–444, 1997.

20. J. Sneyers and P. Wuille, “FLIF: Free lossless image format based on MANIAC compression,” in *Proc. ICIP*, 2016.

21. D. Le Gall and A. Tabatabai, “Sub-band coding of digital images using symmetric short kernel filters and arithmetic coding techniques,” in *Proc. ICASSP*, 1988.

22. W. Sweldens, “The lifting scheme: A custom-design construction of biorthogonal wavelets,” *Appl. Comput. Harmonic Anal.*, vol. 3, no. 2, pp. 186–200, 1996.

23. I. Daubechies and W. Sweldens, “Factoring wavelet transforms into lifting steps,” *J. Fourier Anal. Appl.*, vol. 4, no. 3, pp. 247–269, 1998.

24. A. R. Calderbank, I. Daubechies, W. Sweldens, and B.-L. Yeo, “Wavelet transforms that map integers to integers,” *Appl. Comput. Harmonic Anal.*, vol. 5, no. 3, pp. 332–369, 1998.

25. H. S. Malvar and G. J. Sullivan, “YCoCg-R: A color space with RGB reversibility and low dynamic range,” JVT Doc. JVT-I014, 2003.

26. F. Liu, M. Hernandez-Cabronero, V. Sanchez, M. W. Marcellin, and A. Bilgin, “The current role of image compression standards in medical imaging,” *Information*, vol. 8, no. 4, p. 131, 2017.

27. D. A. Clunie, “Lossless compression of grayscale medical images: Effectiveness of traditional and state-of-the-art approaches,” in *Proc. SPIE Medical Imaging*, 2000.

28. **[Needs work: add references on byte shuffling / bitshuffle / scientific array compression.]**

29. **[Needs work: add one or more references on web-based medical image viewing or browser-side imaging pipelines if that section remains prominent.]**

30. D. Taubman, A. Naman, and R. Mathew, “High throughput block coding in the HTJ2K compression standard,” in *Proc. ICIP*, 2019.

31. D. Taubman et al., “High throughput JPEG 2000 for video content production and delivery over IP networks,” *Frontiers in Signal Processing*, vol. 2, 2022.

32. S. Williams, A. Waterman, and D. Patterson, “Roofline: An insightful visual performance model for multicore architectures,” *Commun. ACM*, vol. 52, no. 4, pp. 65–76, 2009.

33. OpenJPH repository.  
   **[Needs work: keep as software citation only; not as primary evidence.]**

34. CharLS repository.  
   **[Needs work: keep as software citation only; not as primary evidence.]**

35. JavaScript/WASM decoder availability claim.  
   **[Needs work: remove as a numbered reference unless replaced with a stable, citable source or reframed as a limited implementation observation.]**

36. JavaScript Zstandard implementation reference.  
   **[Needs work: keep only if directly relevant to experimental setup.]**
