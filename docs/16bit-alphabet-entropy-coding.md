# 16-bit Alphabet Entropy Coding for Medical Images

This document discusses two foundational design motivations for MIC:

1. Why classical entropy coders designed for 8-bit alphabets are a poor fit for 16-bit medical images — and how MIC's 16-bit-native pipeline addresses this gap.
2. Why signal-processing-based filter-bank transforms (wavelets, DCT) work well for lossy compression but are suboptimal for lossless compression of medical images, making a dedicated 16-bit entropy coding scheme the right approach.

---

## The 16-bit Alphabet Challenge in Entropy Coding

### The 8-bit Assumption in Classical Entropy Coders

Classical entropy coders — Huffman coding, arithmetic coding, and their derivatives — were developed primarily in the context of byte-oriented data streams, where the symbol alphabet has at most 256 entries.

The dominant general-purpose compressors (Deflate, Zstandard, LZ4) all operate on 8-bit byte streams, as do the entropy coding stages of most image codecs:

| Codec | Entropy stage | Alphabet |
|-------|--------------|---------|
| JPEG | Huffman / arithmetic | ≤ 256 (byte-level DC/AC categories) |
| JPEG-LS | Golomb-Rice | Variable, mapped to small range |
| JPEG 2000 | MQ coder (binary arithmetic) | Binary (1 bit at a time) |
| Zstandard | FSE (tANS) | ≤ 4 096 symbols |

FSE, as used in Zstandard, is likewise capped at 4,096 symbols in its standard formulation. This is not an oversight — 8-bit alphabets are the natural coding unit for general-purpose data, and the engineering effort in these systems is optimized for that regime.

### Medical Images Break the 8-bit Assumption

DICOM stores pixels at 10, 12, or 16 bits per sample, yielding alphabet sizes of 1,024, 4,096, or 65,536 symbols respectively. Even after spatial delta prediction, the residual alphabet for a 16-bit CT image can span tens of thousands of distinct values.

An entropy coder that assumes an 8-bit alphabet faces a dilemma:

- **Re-encode 16-bit symbols as byte pairs** — This loses inter-byte correlation and doubles the number of coding steps. The high byte and low byte of a 16-bit residual carry correlated information (if the high byte is 0, the full residual is small) that a byte-by-byte model cannot represent.
- **Truncate the alphabet at 256 and use raw storage for rare symbols** — This forces frequent escapes for modalities with wide dynamic range (CT), inflating the compressed stream.

### Practical Consequences: Why Delta+zstd Underperforms

Applying zstd to delta-encoded 16-bit medical images treats each residual as a two-byte sequence, forcing the LZ77 back-reference engine to discover word-level patterns at the byte level. The match-length and offset statistics are fundamentally less favorable for 16-bit word streams than for natural-language or binary-protocol data, where 8-bit bytes are the natural coding unit.

This is confirmed empirically — see the [Delta+Zstandard comparison](../README.md#comparison-with-deltazstandard):

| Modality | MIC (Delta+RLE+FSE) | Delta+zstd-19 (ultra) | Gap |
|----------|:-------------------:|:---------------------:|:---:|
| MR  | **2.35×** | 1.95× | +21% |
| CT  | **2.24×** | 2.03× | +10% |
| MG1 | **8.57×** | 7.07× | +21% |
| MG4 | **3.47×** | 2.99× | +16% |

MIC outperforms even zstd's highest compression level on every modality.

### The Opportunity: Sparse but Wide Distributions

The 16-bit alphabet also presents an **opportunity** specific to medical images. After delta prediction, residuals are sharply concentrated:

- Over 90% of CT residuals fall within a ±64 range
- Mammography residuals cluster even more tightly near zero
- The **effective active alphabet** — distinct residual values with non-zero counts — is typically far smaller than 65,536 (often < 500 for medical images)

A 16-bit-aware entropy coder can exploit this concentration directly, building a precise probability model over the full uint16 range without byte-splitting distortions. Dynamic table sizing keeps the working set small (MIC's FSE tables are sized to the actual symbol range, not 65,536 — for 8-bit images this reduces working set from 512 KB to ~2 KB).

### How MIC Addresses This

MIC addresses the 8-bit alphabet gap with two 16-bit-native stages:

1. **16-bit RLE stage**: Encodes same-value runs and diff-run sequences operating on uint16 symbols. A run of 1,000 identical 16-bit zero residuals is a single same-run record in MIC's RLE, but 2,000 bytes with no obvious structure to a byte-level LZ77 matcher.

2. **Extended FSE**: MIC extends the standard FSE implementation to support up to 65,535 distinct symbols (versus the typical limit of 4,095), which is necessary for 16-bit medical images where delta residuals can span a wide range. The FSE table log is also adaptively increased from 11 to 12 when symbol density is high (> 128 distinct symbols with > 32 data points each), yielding 4–7% better compression ratios on CR and mammography images.

Together, these 16-bit-native stages outperform Delta+zstd by 10–22% across all tested modalities.

---

## Signal Processing Approaches and Their Limitations for Lossless Compression

### The Filter-Bank Tradition in Image Compression

Image compression research has long drawn on classical signal processing, applying the intuition that natural images can be efficiently represented in a transform domain where energy is concentrated in a small number of large coefficients.

The discrete cosine transform (DCT) underpins JPEG, while the discrete wavelet transform (DWT) forms the core of JPEG 2000. Both transforms decompose the image into components:

```
Low-pass band:   Coarse structure, bulk of signal energy, smooth regions
High-pass bands: Edges, fine texture, noise
```

This mirrors the classical filter-bank model of subband coding. The key insight is that different subbands carry energy at different scales and spatial frequencies, and can be compressed independently.

### Why Filter-Banks Excel at Lossy Compression

For **lossy** compression, filter-bank decompositions are highly effective:

- **High-frequency subbands can be quantized aggressively** with little perceptual impact. The human visual system is relatively insensitive to high-spatial-frequency contrast.
- **Progressive bit-plane coding** of wavelet coefficients (as in JPEG 2000's EBCOT) allows a smooth rate-distortion tradeoff at any target bit rate. At a given bit budget, quantizing small high-frequency coefficients to zero is nearly lossless perceptually.
- **Subband energy compaction**: For natural images, the LL (low-low) subband typically captures > 95% of the signal energy, while HL/LH/HH subbands are sparse and compress well even with coarse quantization.

For medical imaging standards that permit lossy modes — such as visually lossless compression of diagnostic images at moderate bit rates — wavelet-based codecs provide an excellent combination of compression ratio, perceptual quality, and standards compliance.

### Why Filter-Banks Are Suboptimal for Lossless Compression

For **lossless** compression, the picture is more nuanced. Lossless reconstruction requires perfect recovery of every transform coefficient, which imposes several constraints that erode the advantages of the filter-bank approach:

#### 1. Coefficient Overflow

Even with integer lifting schemes (such as the 5/3 Le Gall wavelet used in JPEG 2000 lossless mode), the predict step can produce detail coefficients that exceed the input dynamic range.

```
Predict step: d[i] = x[2i+1] − ⌊(x[2i] + x[2i+2]) / 2⌋
```

For a 16-bit input pair with values near the extremes of the range (e.g., x[2i] = 0, x[2i+1] = 65535), `d[i]` can reach ±32767. After the update step, low-pass coefficients can also overflow uint16. This forces either promotion to 32-bit arithmetic (doubling memory traffic) or escape coding of out-of-range values.

**Measured impact on CT images** (which use the full 16-bit dynamic range):

| Pipeline | CT compression ratio |
|----------|:--------------------:|
| Delta+RLE+FSE | **2.24×** |
| Wavelet+FSE | 1.48× |
| Wavelet+RLE+FSE | 1.48× |

A **52% drop** in compression ratio when the wavelet replaces delta encoding for CT.

#### 2. The Update Step Reintroduces Correlation

In the lifting scheme, the update step adjusts low-pass (scaling) coefficients to preserve the signal mean and orthogonality between subbands:

```
Update step: s[i] = x[2i] + ⌊(d[i−1] + d[i] + 2) / 4⌋
```

While this is desirable for lossy coding (it prevents low-pass coefficients from drifting away from the original signal), for lossless coding of smooth images it adds residual energy to the low-pass band without a compensating reduction elsewhere.

The smooth homogeneous regions that dominate medical images — large uniform tissue areas in mammography, smooth Hounsfield-unit gradients in CT — are exactly the cases where delta encoding already produces near-zero residuals, and where the wavelet update step provides no benefit.

#### 3. Higher Memory Bandwidth

The 2D wavelet inverse transform requires two sequential passes — horizontal then vertical — each visiting every element of the coefficient array. It also operates on int32 values (4 bytes/sample) for intermediate computations versus uint16 (2 bytes/sample) for delta.

For large images at high parallelism, this doubles or quadruples the memory traffic relative to a single-pass delta decoder. Since decompression throughput is memory-bandwidth-limited at high core counts, the wavelet's additional passes impose a proportional throughput penalty.

**Measured decompression speed** (single-threaded, Apple M2 Max):

| Modality | Delta+RLE+FSE | Wavelet+FSE |
|----------|:---:|:---:|
| MG1 | **1,530 MB/s** | 592 MB/s |
| MG2 | **1,493 MB/s** | 618 MB/s |
| MG3 | **606 MB/s** | 387 MB/s |
| CR  | **543 MB/s** | 418 MB/s |

#### 4. Entropy Coding Mismatch

Wavelet subbands each have distinct statistical characteristics (different variances, different spatial correlations), which ideally would be entropy-coded separately with subband-specific models. JPEG 2000 addresses this through context-adaptive bit-plane coding (EBCOT), but EBCOT's complexity is precisely what HTJ2K set out to reduce.

Simpler entropy coders applied to concatenated wavelet coefficients — as in MIC's wavelet+FSE experiments — fail to exploit this structure, resulting in worse compression than the simple flat residual distribution produced by delta encoding.

### The Case for Delta Prediction + 16-bit Entropy Coding

By contrast, simple spatial prediction (delta encoding) leaves a single residual image whose statistics are **homogeneous across the frame**: residuals are tightly clustered around zero everywhere, regardless of spatial frequency. There are no subbands to model separately — a single flat probability distribution applies everywhere.

This homogeneity makes the residual ideal for a uniform 16-bit entropy coding scheme: one RLE+FSE pipeline applied once, without subband partitioning or context modeling.

```
Filter-bank approach:
  Image → multiple subbands with different statistics
        → need per-subband entropy models (or lose compression ratio)
        → complex, slow, worse results for lossless

Delta approach:
  Image → single flat near-zero residual distribution
        → one 16-bit entropy coder models everything
        → simple, fast, better results for lossless
```

The result is a codec that is simultaneously simpler, faster, and better-compressing than the wavelet alternative on every tested medical modality. See the full [Wavelet+FSE analysis](./wavelet-fse-analysis.md) for detailed results.

### When the Wavelet Transform Does Make Sense

The wavelet transform remains valuable for two use cases outside MIC's current lossless scope:

- **Lossy compression**: Wavelet coefficients in high-frequency subbands can be quantized by magnitude, enabling smooth rate-distortion tradeoffs that are impossible with pure delta coding.
- **Progressive/multi-resolution delivery**: The LL subband at each decomposition level is a valid downscaled image, enabling zoom-level-aware delivery without decoding the full image. (MIC3 handles this for WSI via a separate tiled pyramid, but at the cost of storing multiple encoded versions.)

The wavelet transform is already implemented in MIC's codebase and benchmarked; it remains a natural foundation for a future lossy mode if ever needed.

### Conclusion

For lossless compression of clinical medical images — which are characterized by spatial smoothness, moderate noise, and 16-bit pixel depth — filter-bank decompositions add complexity without benefit. The right approach is:

1. A fast, accurate spatial predictor (delta encoding with average-of-neighbors) to produce a homogeneous, near-zero residual distribution
2. A 16-bit-native entropy coder (RLE + extended FSE) to model that distribution precisely

This combination outperforms both wavelet-based pipelines and general-purpose byte-oriented compressors across all tested DICOM modalities, in both compression ratio and decompression speed.
