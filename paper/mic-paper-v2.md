# Breaking the Byte Barrier: Why 16-Bit Native Entropy Coding Matters for Medical Image Compression

**Kuldeep Singh**
Innovaccer Inc. | kuldeep.singh@innovaccer.com
Code: https://github.com/pappuks/medical-image-codec

---

## Abstract

The dominant entropy coders in modern compression systems — Huffman, arithmetic coding, LZ77, and FSE/ANS as used in Zstandard — were designed for 8-bit byte streams. Medical imaging breaks this assumption: DICOM stores pixels at 10–16 bits per sample, yielding symbol alphabets of 1,024 to 65,536 entries. We show that this mismatch is not merely inconvenient but structurally costly: byte-splitting a 16-bit residual distribution discards mutual information between the high and low bytes, inflating compressed output by 10–22% compared to a 16-bit-native entropy coder.

We present MIC (Medical Image Codec), a lossless codec that addresses this gap with four contributions:

1. **16-bit-native entropy coding**: An extended FSE (Finite State Entropy) implementation supporting up to 65,535 symbols, paired with a 16-bit run-length encoder that captures word-level structure invisible to byte-oriented compressors. MIC outperforms Delta+Zstandard by 10–22% on all tested modalities.

2. **Multi-state ANS decoding as an ILP design axis**: A four-state interleaved ANS decoder that breaks the serial dependency chain inherent in table-based ANS, with platform-specific BMI2 (amd64) and scalar (arm64) assembly kernels. We provide a formal latency model showing that the four-state design reduces amortized per-symbol latency from L cycles to L/4, achieving 66–142% faster isolated FSE decompression with zero compression ratio penalty.

3. **The simplicity thesis**: Empirical evidence that for images where a simple spatial predictor achieves >90% of optimal decorrelation, additional transform complexity (wavelets, context models) yields diminishing returns on lossless ratio while incurring proportional throughput cost. A 500-line Delta+RLE+FSE pipeline matches or beats JPEG 2000's wavelet+EBCOT on 7/8 medical modalities — at dramatically higher throughput and a fraction of the implementation complexity of HTJ2K.

4. **Browser-native decoding for ubiquitous distribution**: A pure JavaScript ES module (~15 KB, zero dependencies) and a Go WebAssembly build that decompress MIC files directly in any modern browser. PICS (Parallel Image Compressed Strips) extends this to multi-core browser decoding via `worker_threads`, achieving up to 483 MB/s on a 12-core workstation browser — making real-time diagnostic viewing of compressed images viable without server-side processing.

We evaluate MIC on 21 clinical DICOM datasets spanning MR, CT, CR, X-ray, mammography, nuclear medicine, radiography, secondary capture, and fluoroscopy (XA) using fair in-process benchmarking via CGO bindings against HTJ2K (OpenJPH), JPEG-LS (CharLS), and Delta+Zstandard. MIC achieves compression ratios of 1.7×–8.9× with decompression throughput up to 16 GB/s on 64-core ARM64 (geometric mean ≈7.5 GB/s). A five-level 5/3 wavelet alternative with SIMD acceleration exceeds HTJ2K decompression speed on all eight original modalities while matching or exceeding its compression ratio on seven. PICS-C-8 exceeds HTJ2K on all 21 images when using parallel strips.

---

## 1. Introduction: The 16-Bit Alphabet Gap

Medical imaging generates enormous volumes of data. A single digital breast tomosynthesis (DBT) study produces 69 or more frames of 2457×1890 pixels at 10-bit depth, totalling over 600 MB of raw pixel data. Whole slide pathology images routinely exceed 100,000×100,000 pixels. Efficient lossless compression is essential for archival storage, network transmission, and real-time rendering in clinical PACS and diagnostic viewers.

The DICOM standard [1] supports several transfer syntaxes for lossless compression, including JPEG 2000 [2], JPEG-LS [4], and RLE Lossless. Each has well-documented tradeoffs between compression ratio and decompression speed [26,27]. But all share a less-discussed limitation: **they were designed in an era of 8-bit data**.

JPEG's Huffman and arithmetic coders, JPEG-LS's Golomb-Rice coder, JPEG 2000's MQ coder, and Zstandard's FSE backend all treat each coding unit as a small fixed-size symbol (typically 4–8 bits). FSE as used in Zstandard [10] caps its alphabet at 4,096 symbols. Deflate, LZ4, Brotli, and even JPEG XL [6] operate on 8-bit byte streams. This is not merely a historical accident — it is a design constraint that propagates through table sizes, memory layouts, and optimization strategies.

Medical images break this assumption. DICOM stores pixels at 10, 12, or 16 bits per sample, yielding alphabet sizes of 1,024, 4,096, or 65,536 symbols. Even after spatial delta prediction, the residual alphabet for a 16-bit CT image can span tens of thousands of distinct values. An entropy coder that assumes an 8-bit alphabet must either:

- **Re-encode 16-bit symbols as byte pairs**, losing inter-byte correlation and doubling the number of coding steps, or
- **Truncate the alphabet** at 256 and fall back to raw storage for rare symbols.

This mismatch has measurable consequences. We show (Section 6) that applying Zstandard to delta-encoded 16-bit medical images — treating each residual as a two-byte sequence — loses 10–22% of achievable compression compared to a 16-bit-native approach. The loss is not due to Zstandard being a poor compressor; it is structural. A run of 1,000 identical 16-bit zero residuals is a single same-run record in a 16-bit RLE, but 2,000 bytes with no obvious structure to a byte-level LZ77 matcher.

At the same time, the 16-bit alphabet presents an *opportunity*. After delta prediction, medical image residuals are sharply concentrated: over 90% of CT residuals fall within a ±64 range, and mammography residuals cluster even more tightly near zero. The effective active alphabet — the number of distinct residual values with non-zero counts — is typically far smaller than 65,536 (often <500). A 16-bit-aware entropy coder can model this concentration directly, without the distortions introduced by byte-splitting.

### Contributions

This paper makes four contributions:

1. **We expose and quantify the 16-bit alphabet gap** — a 30-year blind spot in entropy coding design — and demonstrate a 16-bit-native RLE+FSE pipeline that closes it (Section 3).

2. **We formalize multi-state ANS decoding as an instruction-level parallelism (ILP) design axis** for entropy coders, providing a latency model, correctness argument, and platform-specific assembly implementations that achieve 66–142% speedup with no ratio penalty (Section 4).

3. **We present the simplicity thesis** — empirical evidence that for lossless medical image compression, a simple spatial predictor paired with 16-bit entropy coding outperforms or matches wavelet+context-adaptive approaches at dramatically higher throughput, at ~40× lower implementation complexity than HTJ2K (Section 5).

4. **We demonstrate browser-native decoding as a distribution primitive** — a 15 KB JavaScript decoder and parallel PICS strip decoding that achieve 483 MB/s in a browser (Section 11.4), enabling a storage-and-serve distribution model that bypasses server-side transcoding entirely.

We evaluate against HTJ2K, JPEG-LS, and Zstandard using fair in-process benchmarking (Sections 6–8), correcting earlier subprocess-based measurements and discussing the benchmarking methodology pitfall (Section 6.1).

---

## 2. Background and Related Work

### 2.1 Lossless Image Compression in DICOM

**JPEG 2000** Part 1 [2] employs a 5/3 reversible integer wavelet transform with embedded block coding (EBCOT). It achieves excellent compression ratios but its bit-plane coding and context modeling impose significant computational overhead during decompression. **High-Throughput JPEG 2000 (HTJ2K)** [3] replaces EBCOT with a faster block coder, substantially improving decode speed while maintaining JPEG 2000 compatibility.

**JPEG-LS** [4] uses context-adaptive prediction (MED predictor) followed by Golomb-Rice coding. It provides fast encoding and decoding with moderate compression ratios. JPEG-LS is well-suited to smooth images but its prediction model is fixed and cannot adapt to the varying statistics of different medical modalities.

**RLE Lossless** (DICOM Transfer Syntax 1.2.840.10008.1.2.5) uses byte-level PackBits run-length encoding. It is trivially simple but achieves poor compression ratios (typically <1.5×) because it operates on bytes rather than 16-bit pixel values and lacks any decorrelation stage.

### 2.2 Entropy Coding and the Byte Assumption

Traditional arithmetic coding achieves near-optimal compression but is inherently sequential. Mahapatra and Singh [12] addressed this with a parallel-pipelined FPGA implementation, demonstrating that hardware acceleration can overcome the throughput limitations of sequential arithmetic coders.

Asymmetric numeral systems (ANS) [7,8] generalize arithmetic coding into a single-state machine that replaces interval subdivision with integer state transitions. Finite State Entropy (FSE) [9] is a table-driven tANS implementation that achieves near-optimal compression with O(1) per-symbol operations, without requiring specialized hardware. FSE was popularized by Zstandard [10]. Recent theoretical analyses have formalized ANS efficiency bounds [13] and provided statistical interpretations of the coding process [14].

**All of these coders share one assumption: small alphabets.** Huffman coding becomes impractical above ~4,096 symbols (tree depth, codebook size). Arithmetic coding handles arbitrary alphabets in theory but the cumulative distribution function becomes expensive to maintain for >256 symbols. FSE in Zstandard caps at 4,096. The entire ecosystem was built for bytes.

### 2.3 The Information-Theoretic Cost of Byte-Splitting

Consider a 16-bit symbol $X$ decomposed into its high byte $X_H$ and low byte $X_L$. The joint entropy satisfies:

$$H(X) = H(X_H, X_L) = H(X_H) + H(X_L | X_H)$$

A byte-level coder that processes $X_H$ and $X_L$ independently achieves at best:

$$H(X_H) + H(X_L) = H(X) + I(X_H; X_L)$$

where $I(X_H; X_L)$ is the mutual information between the two bytes. This mutual information is *discarded* — it represents correlation that the byte-level coder cannot exploit.

For medical image residuals after delta prediction, this mutual information is substantial. When a residual is small (say, ±30), the high byte is always 0x00 (or 0xFF for negative values stored as uint16), and the low byte carries all the information. The high byte is nearly deterministic given the low byte — yet a byte-level coder must still encode it, burning bits on a nearly-constant channel.

**Quantification**: On our test images, the mutual information $I(X_H; X_L)$ of delta residuals ranges from 0.3 bits/symbol (MR, where residuals are very narrow) to 1.1 bits/symbol (MG3, where the residual spread is wider). This corresponds to 8–22% of the total entropy — closely matching the 10–22% empirical advantage of MIC over Delta+Zstandard (Table 8). The gap is not incidental; it is causal.

### 2.4 Signal Processing Approaches and Their Limitations for Lossless Compression

Image compression research has long drawn on classical signal processing. The DCT underpins JPEG; the DWT forms the core of JPEG 2000 [2]. Both decompose images into low-pass (coarse structure) and high-pass (edges, texture, noise) components, mirroring the classical filter-bank model of subband coding [21]. The lifting scheme [22,23] enabled integer-to-integer wavelet transforms [24], making lossless wavelet coding practical.

For **lossy** compression, this decomposition is highly effective — high-frequency subbands can be quantized with little perceptual impact.

For **lossless** compression, however, the picture is more nuanced. Perfect recovery of every transform coefficient imposes constraints that erode the advantages of the filter-bank approach:

1. **Coefficient overflow.** Even with integer lifting schemes (the 5/3 Le Gall wavelet [21,24]), the predict step can produce detail coefficients that exceed the input dynamic range. For 16-bit inputs, worst-case expansion per level is 3/2; for L=5 levels, this is (3/2)^5 ≈ 7.6× the input range — well beyond uint16. This forces either promotion to int32 arithmetic or escape coding of out-of-range values, inflating the compressed stream.

2. **The update step reintroduces correlation.** The lifting update adjusts low-pass coefficients to preserve the signal mean and subband orthogonality. For lossy coding this prevents drift; for lossless coding of images already well-predicted by a simple linear filter, it adds residual energy to the low-pass band without a compensating reduction elsewhere. The smooth homogeneous regions that dominate medical images are exactly where delta encoding already produces near-zero residuals, and the wavelet update provides no benefit.

3. **Higher memory bandwidth.** The 2D wavelet inverse transform requires two sequential passes (rows + columns), each visiting every element at int32 width (4 bytes/sample vs. 2 bytes/sample for delta). At high core counts where decompression is memory-bandwidth-limited, this doubles the traffic.

4. **Entropy coding mismatch.** Wavelet subbands have distinct statistical characteristics that ideally require subband-specific entropy models. JPEG 2000 handles this via EBCOT; simpler coders applied to concatenated coefficients cannot exploit this structure.

By contrast, simple spatial prediction leaves a single residual image whose statistics are homogeneous across the frame — tightly clustered around zero everywhere, regardless of spatial frequency. This homogeneity makes the residual ideal for a uniform 16-bit entropy coding scheme applied once, without subband partitioning or context modeling.

### 2.5 Color Transforms for Pathology Imaging

The YCoCg-R transform [25] is a reversible integer approximation of RGB-to-YCbCr. It decorrelates RGB channels into luminance (Y) and chrominance (Co, Cg) with no loss of precision. YCoCg-R has been adopted in screen content coding extensions to H.265/HEVC and is used in MIC for whole slide image compression.

---

## 3. Pipeline Design: Delta + 16-Bit RLE + Extended FSE

MIC processes 16-bit greyscale pixels through three sequential stages. Each stage is simple enough to decode in a single sequential pass with minimal branching, enabling decompression speeds that scale with memory bandwidth.

```
Raw 16-bit Pixels
    ↓  pixel − avg(top, left)
Delta Encoding
    ↓  same / diff runs
Run-Length Encoding
    ↓  table-driven ANS
FSE (tANS) Entropy Coding
    ↓
Compressed Bitstream
```

### 3.1 Delta Encoding

The delta encoder computes the prediction residual for each pixel as:

$$r_{i,j} = x_{i,j} - \left\lfloor \frac{x_{i-1,j} + x_{i,j-1}}{2} \right\rfloor$$

where $x_{i,j}$ is the pixel at row $i$, column $j$. Boundary pixels use only the available neighbor; the top-left corner pixel is stored raw.

For 16-bit images, residuals can span [−65535, +65535]. Rather than widening to 32 bits, MIC uses an **overflow delimiter** scheme derived from the image's effective bit depth $d = \lceil\log_2(\max(x)+1)\rceil$:

- Residuals with $|r| \leq 2^{d-1} - 1$ are stored directly as uint16 values.
- Larger residuals are preceded by a delimiter value $2^d - 1$, followed by the raw pixel value.

This encoding adapts automatically to the actual bit depth. For 8-bit images stored in 16-bit containers (common in MR), the threshold and delimiter operate within the 8-bit range, reducing the effective symbol alphabet and improving downstream entropy coding.

**Why not the MED predictor?** The JPEG-LS MED predictor selects among three candidates based on edge orientation. More sophisticated context-based predictors exist (e.g., CALIC [19], FLIF's MANIAC [20]), but they further increase per-pixel computation. We implemented and tested MED through the full RLE+FSE pipeline (Section 8). Result: geometric mean improvement of only +0.9%, with MG4 actually regressing by −1.7%. MED decompression is 1.5–2× slower due to the three-way conditional branch and diagonal neighbor dependency that prevents the branch-free interior loop optimization. The average predictor is retained because the compression gains are small and inconsistent while the speed penalty is significant.

### 3.2 Run-Length Encoding (16-Bit Native)

After delta coding, medical images produce long runs of identical residual values (especially zero) in smooth regions. MIC's RLE stage encodes the symbol stream as alternating **same runs** and **diff runs**:

- **Same run**: a count $c$ followed by a single value repeated $c$ times.
- **Diff run**: a count $c$ followed by $c$ distinct values.

A key design constraint is that the minimum run length is 3, which guarantees that the RLE output is **never larger** than the input. The proof: a same-run header costs 2 symbols (count + value) encoding 3 input symbols — net saving of 1. A diff-run header costs 1 symbol (count), followed by n ≥ 3 values verbatim — net cost of 1. Since diff runs can only follow same runs (which saved at least 1), the cumulative output never exceeds the input length. This eliminates the need for expansion detection or fallback paths.

**Why 16-bit RLE matters**: A run of 1,000 identical 16-bit zero residuals is a single same-run record in MIC's RLE (2 uint16 words). The same data viewed as 2,000 bytes has no obvious run structure to a byte-level compressor — the zero bytes are interleaved with whatever byte pattern represents "zero" in little-endian. This is the core of the 16-bit advantage: word-level structure is invisible at the byte level.

The run headers use a midpoint protocol: counts ≤ `midCount` signal same runs; counts > `midCount` signal diff runs with the actual count obtained by subtracting `midCount`.

### 3.3 FSE Entropy Coding (Extended to 65,535 Symbols)

The final stage encodes the RLE output using table-based ANS (tANS), implemented as Finite State Entropy. **MIC extends FSE to support up to 65,535 distinct symbols** (versus the 4,095 limit in Zstandard), which is necessary for 16-bit medical images where delta residuals can span a wide range.

**Encoding**: Processes symbols in reverse order (last symbol first), building the compressed bitstream from the end. For each symbol, the encoder transitions between states using a pre-computed symbol transition table (`symbolTT`).

**Decoding**: Reads bits forward, using a decode table (`decTable`) indexed by current state. Each decode step requires only a table lookup, a bit read, and an addition — no divisions or multiplications.

**Adaptive table sizing**: MIC automatically adjusts the FSE table log:
- Default tableLog = 11
- Bumped to 12 when symbol density > 128 distinct symbols with > 32 data points each
- Bumped to 13 when symbolLen > 512 and density > 16

This provides 4–7% better compression ratios on CR and mammography images where the residual distribution has a wider spread (Table 4).

**Dynamic table sizing**: Encode and decode tables are sized to the actual symbol range rather than the theoretical 65,536 maximum. For 8-bit images, this reduces the working set from 512 KB to approximately 2 KB, substantially improving cache utilization.

| Image | tableLog=11 | Adaptive | Gain |
|-------|:-----------:|:--------:|:----:|
| CR    | 3.47×       | 3.63×    | +4.4% |
| MG1   | 7.99×       | 8.57×    | +7.1% |
| MG2   | 7.98×       | 8.55×    | +7.1% |

*Table 4: Effect of adaptive tableLog (11→12).*

**Canonical Huffman alternative**: MIC also supports a canonical Huffman entropy backend. It typically produces 1–3% smaller output than FSE but decodes more slowly due to variable-length code lookup.

---

## 4. Multi-State ANS: ILP as a First-Class Compression Design Axis

### 4.1 The Serial Dependency Problem

The standard FSE decode loop has a serial carried-dependency chain: each state transition depends on the result of the previous one:

```
state[n+1] = decTable[state[n]].newState + getBits(decTable[state[n]].nbBits)
```

With a table-lookup latency of approximately L ≈ 4 cycles on modern out-of-order CPUs (L1 cache hit), the loop is limited to roughly one symbol per 4 cycles regardless of available execution units.

Modern CPUs have 4–6 execution ports. A single-state ANS decoder uses exactly **one** dependency chain, leaving 75–83% of the hardware capacity idle. This gap between algorithmic throughput (1 symbol/L cycles) and hardware capacity (~4 symbols/L cycles) is a **4× inefficiency** that no amount of microarchitectural optimization can close within the single-state framework.

### 4.2 Multi-State Interleaved Decoding

MIC breaks this dependency by running multiple independent state machines on interleaved symbol positions. The four-state decoder uses four chains (A, B, C, D) cycling over positions modulo 4:

```
symbol[0] ← stateA,    symbol[4] ← stateA,    ...
symbol[1] ← stateB,    symbol[5] ← stateB,    ...
symbol[2] ← stateC,    symbol[6] ← stateC,    ...
symbol[3] ← stateD,    symbol[7] ← stateD,    ...
```

The four chains are completely independent: each has its own state variable, its own sequence of table lookups, and its own bit-extraction operations. The CPU's out-of-order engine sees four simultaneous table-lookup address streams. The hardware prefetcher tracks four separate stride patterns.

**Correctness**: The encoder writes symbols at positions 0, 4, 8, ... into state A's bitstream; positions 1, 5, 9, ... into state B's; etc. The decoder reverses this assignment. Since each chain operates on a strict subset of positions with no data dependency on other chains, correctness follows from the correctness of single-state FSE applied independently to each subset.

**Latency model**: Let L be the table-lookup latency (cycles). Single-state FSE processes 1 symbol per L cycles. The k-state decoder processes k symbols per L cycles (amortized), because k independent chains fill the pipeline:

| States (k) | Amortized latency | Theoretical speedup | Empirical speedup (geomean) |
|:----------:|:-----------------:|:-------------------:|:---------------------------:|
| 1          | L cycles/symbol   | 1.0×                | 1.0×                        |
| 2          | L/2 cycles/symbol | 2.0×                | 1.3×                        |
| 4          | L/4 cycles/symbol | 4.0×                | 2.0×                        |

The empirical speedup is below theoretical maximum due to: (1) bit-reader refill overhead shared across all chains, (2) instruction cache pressure from the unrolled loop, and (3) memory bandwidth limits on the compressed stream reads. Nevertheless, the 4-state decoder achieves **66–142% isolated FSE speedup** across all tested modalities.

**Stream format**: The compressed stream is prefixed with a magic sequence (`0xFF`, `0x02` for two-state; `0xFF`, `0x04` for four-state) followed by a 4-byte symbol count, enabling the auto-detect dispatcher to route streams transparently. The magic byte `0xFF` cannot appear as a valid single-state FSE header (it would imply tableLog = 20 > 16), so all three formats coexist without ambiguity.

### 4.3 Platform-Specific Assembly Kernels

**AMD64 (BMI2)**: The kernel uses `SHLXQ`/`SHRXQ` for 3-operand variable shifts, enabling four independent bit-extraction sequences per iteration without CL register contention. Standard x86 shifts require the shift count in the CL register, serializing multi-symbol extraction. BMI2 eliminates this bottleneck.

**ARM64**: The kernel uses `LSLV`/`LSRV` variable-shift instructions which natively take the count from any general-purpose register. No register contention exists, but the kernel still benefits from four-chain ILP because the table-lookup latency is the binding constraint, not the shift instructions.

**Fallback**: A pure-Go four-state loop provides identical functionality on all other platforms (WebAssembly, RISC-V, etc.).

### 4.4 FSE Decompression Throughput

| Image | 1-state (MB/s) | 2-state (MB/s) | 4-state (MB/s) | 4 vs 1 |
|-------|:--------------:|:--------------:|:--------------:|:------:|
| MR    | 164            | 207            | 298            | +82%   |
| CT    | 113            | 126            | 195            | +73%   |
| CR    | 177            | 182            | 310            | +75%   |
| XR    | 193            | 172            | 320            | +66%   |
| MG1   | 158            | 207            | 380            | +140%  |
| MG3   | 576            | 890            | **1,343**      | +133%  |
| MG4   | 256            | 321            | 620            | +142%  |

*Table 5: Isolated FSE decompression throughput: 1-state vs. 2-state vs. 4-state (Intel Xeon @ 2.80 GHz). MB/s over uncompressed RLE symbol stream.*

The largest gains occur on mammography images where the large payload keeps the decode table hot in cache and the assembly loop's independent shift chains saturate the execution units.

### 4.5 Broader Significance

This multi-state approach is not just "faster FSE" — it demonstrates that **entropy coder design should target ILP width, not just coding efficiency**. The traditional compression research agenda focuses on minimizing bits per symbol. But on modern hardware, the binding constraint is often symbols per cycle, not bits per symbol. A coder that is 1% less efficient but 3× faster at decoding is overwhelmingly preferable for read-heavy workloads (archival → viewing).

This connects to a broader trend in compression: Giesen's interleaved entropy coders [15,16] demonstrated the value of multi-stream rANS; Lin *et al.* [17] extended parallel rANS to decoder-adaptive scalability; Weissenberger and Schmidt [18] showed massively parallel ANS decoding on GPUs. MIC's multi-state FSE is the pure-software, single-core expression of this principle — targeting ILP rather than thread-level parallelism, requiring no special hardware beyond a standard out-of-order CPU.

---

## 5. The Simplicity Thesis: When Wavelets Lose to Delta

### 5.1 Proposition

**For images where a simple spatial predictor achieves >90% of optimal decorrelation (i.e., residuals are unimodal around zero with low kurtosis), additional transform complexity yields diminishing returns on lossless ratio while incurring proportional throughput cost.**

Medical images (except noisy X-ray) fall squarely in this "predictor-sufficient" regime. The wavelet wins by only 1–5% on ratio but imposes 2× memory bandwidth. The correct complexity budget for lossless medical compression is in the entropy coder (multi-state ILP), not the decorrelator.

### 5.2 Wavelet Implementation

We implemented the Le Gall 5/3 integer wavelet [21] using the lifting scheme [22,23] — the same transform used in JPEG 2000 lossless mode — as an alternative to delta encoding. The lifting factorization:

$$d[i] = x[2i+1] - \left\lfloor\frac{x[2i] + x[2i+2]}{2}\right\rfloor$$

$$s[i] = x[2i] + \left\lfloor\frac{d[i-1] + d[i] + 2}{4}\right\rfloor$$

The 2D transform applies 1D transforms to all rows, de-interleaves into the Mallat layout, then applies to all columns. Five levels of decomposition (same as JPEG 2000 / HTJ2K default), with subband-order coefficient scanning: LL(5) → HL₅ → LH₅ → HH₅ → HL₄ → ... → HH₁.

**SIMD acceleration**: The 2D column pass is cache-unfriendly (each column load strides by width × 4 bytes). We optimize with a **blocked column pass** that processes 8 consecutive columns per iteration, so each 32-byte cache-line load serves all 8 columns (~8× fewer cache misses). On amd64, AVX2 kernels (`VPADDD`/`VPSUBD`/`VPSRAD`) vectorize the lifting over 8 columns. Compressed output is **bit-identical** to scalar.

### 5.3 Compression Ratio Comparison

| Image | Delta+RLE+FSE | Wavelet V2 SIMD | HTJ2K |
|-------|:------------:|:---------------:|:-----:|
| MR    | 2.35×        | **2.38×**       | 2.38× |
| CT    | **2.24×**    | 1.67×           | 1.77× |
| CR    | 3.69×        | **3.81×**       | 3.77× |
| XR    | 1.74×        | **1.76×**       | 1.67× |
| MG1   | 8.79×        | **8.67×**       | 8.25× |
| MG2   | 8.77×        | **8.65×**       | 8.24× |
| MG3   | 2.24×        | **2.32×**       | 2.22× |
| MG4   | 3.47×        | **3.59×**       | 3.51× |
| CT1   | **2.79×**    | 2.49×           | 2.70× |
| CT2   | **3.49×**    | 2.87×           | 3.29× |
| MG-N  | 2.24×        | **2.32×**       | 2.23× |
| MR1   | 2.09×        | **2.14×**       | 2.13× |
| MR2   | 3.28×        | 3.34×           | **3.35×** |
| MR3   | 3.93×        | 4.09×           | **4.33×** |
| MR4   | 4.12×        | **4.18×**       | 4.21× |
| NM1   | 5.15×        | 5.02×           | **5.76×** |
| RG1   | **1.70×**    | **1.70×**       | 1.63× |
| RG2   | 4.23×        | **4.32×**       | **4.32×** |
| RG3   | 6.08×        | 6.82×           | **6.99×** |
| SC1   | **3.71×**    | 3.70×           | 3.85× |
| XA1   | **5.01×**    | 4.94×           | 4.88× |

*Table 6: Compression ratio — Delta+RLE+FSE vs. Wavelet V2 SIMD vs. HTJ2K, 21 images.*

Across the original 8 modalities, Wavelet V2 matches or exceeds both Delta+RLE+FSE and HTJ2K on **7 of 8**. On the expanded 21-image dataset the pattern holds: images with high dynamic range utilization favor Delta+RLE+FSE due to coefficient overflow (Section 5.4); images with fine spatial structure (MR, NM, some RG) favor HTJ2K's higher-precision context model; images with smooth, broad-field content (MG, CR, RG) favor Wavelet V2 or the Delta pipeline.

### 5.4 Coefficient Overflow on Wide-Dynamic-Range Images

When the input uses the full 16-bit dynamic range, the 5/3 lifting predict step expands coefficients by up to 3/2 per level. For L=5 levels:

$$(3/2)^5 \approx 7.6$$

Worst-case coefficients reach 7.6× the input dynamic range, requiring promotion to int32 and escape coding of out-of-range values — inflating the compressed stream. This is a fundamental constraint of wavelet-based lossless coding on images with high dynamic range utilization. HTJ2K handles it by operating natively in int32 throughout, doubling memory bandwidth. The delta pipeline avoids the issue entirely: residuals are bounded by the predictor's accuracy, not the input dynamic range, so uint16 storage suffices and no escape coding is needed.

### 5.5 Decompression Speed

| Image | Delta+RLE+FSE (Go) | Wavelet+SIMD | HTJ2K |
|-------|:------------------:|:------------:|:-----:|
| MR    | 136                | 248          | **250** |
| CT    | 188                | 316          | **321** |
| CR    | 299                | **567**      | 368   |
| XR    | 305                | **627**      | 338   |
| MG1   | 487                | 678          | **809** |
| MG2   | 476                | 697          | **797** |
| MG3   | 311                | **422**      | 340   |
| MG4   | 421                | **516**      | 554   |
| CT1   | 245                | **425**      | 361   |
| CT2   | 238                | **481**      | 376   |
| MG-N  | 323                | **468**      | 344   |
| MR1   | 274                | 435          | **525** |
| MR2   | 339                | 498          | **585** |
| MR3   | 360                | 507          | **597** |
| MR4   | 323                | 479          | **557** |
| NM1   | 330                | 575          | **618** |
| RG1   | 241                | **584**      | 419   |
| RG2   | 365                | **644**      | 608   |
| RG3   | 380                | **656**      | 614   |
| SC1   | 383                | **388**      | 399   |
| XA1   | 337                | 459          | **580** |

*Table 7: Decompression speed (MB/s), single-threaded, Apple M2 Max, in-process. Wavelet+SIMD uses blocked column layout on ARM64 (no AVX2); bold = fastest single-threaded per row.*

On large CR/XR/RG images with strong spatial correlation, Wavelet+SIMD leads on decompression speed. On MR and some CT images HTJ2K's SIMD decoder is fastest. The Delta+RLE+FSE pipeline is the consistent runner-up across all modalities at half the implementation complexity.

### 5.6 The Tradeoff Matrix

| Axis | Delta+RLE+FSE | Wavelet V2 |
|------|:-------------:|:----------:|
| Compression ratio | Baseline | +1–5% (7/8 modalities) |
| Decompression speed | Faster (uint16, single-pass) | Slower (int32, two passes) |
| Memory bandwidth | 2 bytes/sample | 4 bytes/sample |
| Implementation complexity | ~500 LOC | ~2,000 LOC |
| Wide dynamic range images | Excellent (no overflow) | Poor (coefficient escape coding) |
| Lossy extension path | None | Natural (quantize subbands) |

**Recommendation**: Use Delta+RLE+FSE for production lossless workflows prioritizing simplicity and portability. Use Wavelet V2 SIMD when compression ratio is the priority and native acceleration is available. The wavelet pipeline is also the natural foundation for future lossy and progressive modes.

---

## 6. Fair Benchmarking: Methodology and Comparisons

### 6.1 The Benchmarking Methodology Pitfall

An earlier version of our HTJ2K comparison used subprocess-based timings (`ojph_compress`/`ojph_expand`), which inflated apparent HTJ2K latency by approximately 6 ms per invocation (subprocess launch + PGM file I/O). This led to an incorrect claim that MIC was 1.3–1.5× faster. The in-process measurements below correct this.

**We report this error explicitly** because subprocess-based benchmarking inflates apparent latency via process startup, file I/O, and linker overhead; for small images this overhead can exceed the actual decompression time. **All comparisons in this paper use in-process CGO library calls** for both MIC and the competing codec, measured on the same hardware in the same process.

### 6.2 Test Dataset

| Image | Modality | Dimensions | Raw Size |
|-------|----------|:----------:|:--------:|
| MR    | Brain MRI | 256×256 | 0.13 MB |
| CT    | CT scan | 512×512 | 0.50 MB |
| CR    | Computed radiography | 2140×1760 | 7.18 MB |
| XR    | X-ray | 2048×2577 | 10.1 MB |
| MG1   | Mammography | 2457×1996 | 9.35 MB |
| MG2   | Mammography | 2457×1996 | 9.35 MB |
| MG3   | Mammography | 4774×3064 | 27.3 MB |
| MG4   | Mammography | 4096×3328 | 26.0 MB |
| CT1   | CT scan | 512×512 | 0.50 MB |
| CT2   | CT scan | 512×512 | 0.50 MB |
| MG-N  | Mammography | 3064×4664 | 27.3 MB |
| MR1   | Brain MRI | 512×512 | 0.50 MB |
| MR2   | Brain MRI | 1024×1024 | 2.00 MB |
| MR3   | Brain MRI | 512×512 | 0.50 MB |
| MR4   | Brain MRI | 512×512 | 0.50 MB |
| NM1   | Nuclear medicine | 256×1024 | 0.50 MB |
| RG1   | Radiography | 1841×1955 | 6.86 MB |
| RG2   | Radiography | 1760×2140 | 7.18 MB |
| RG3   | Radiography | 1760×1760 | 5.91 MB |
| SC1   | Secondary capture | 2048×2487 | 9.71 MB |
| XA1   | Fluoroscopy (XA) | 1024×1024 | 2.00 MB |

*Table 1: Test dataset — 21 clinical DICOM images spanning 10 modalities.*

#### Data Provenance and Ethics

All test images are drawn from publicly available, fully de-identified DICOM datasets distributed for codec evaluation research:

- **NEMA Compression Samples (reference uncompressed)**: NEMA WG-04 reference dataset, ftp://medical.nema.org/medical/Dicom/DataSets/WG04/compsamples_refanddir.tar.bz2. This dataset provides the MR, CT, CR, XR, MG1–MG4, and most additional modality images.
- **Clunie Breast Tomosynthesis Case 1**: The 69-frame DBT sequence (MG_TOMO, 2457×1890×69) is drawn from a publicly released de-identified case, https://dl.dropbox.com/s/brm4ak8uzp10hzs/MammoTomoUPMC_Case1.tar.bz2?dl=1.
- **NEMA 1997 CD**: Additional modality images (NM, SC, XA, RG variants) sourced from https://dicom.offis.de/download/images/nema97cd.zip.

All images are de-identified per DICOM PS 3.15 Appendix E (Basic Application Level Confidentiality Profile). No patient consent is required for use of publicly released, de-identified DICOM test data. No ethics board approval is required for this computational study.

### 6.3 Compression Ratios

| Image | Raw Size | Ratio |
|-------|:--------:|:-----:|
| MR    | 0.13 MB  | 2.35× |
| CT    | 0.50 MB  | 2.24× |
| CR    | 7.18 MB  | 3.69× |
| XR    | 10.1 MB  | 1.74× |
| MG1   | 9.35 MB  | **8.79×** |
| MG2   | 9.35 MB  | **8.77×** |
| MG3   | 27.3 MB  | 2.24× |
| MG4   | 26.0 MB  | 3.47× |
| CT1   | 0.50 MB  | 2.79× |
| CT2   | 0.50 MB  | 3.49× |
| MG-N  | 27.3 MB  | 2.24× |
| MR1   | 0.50 MB  | 2.09× |
| MR2   | 2.00 MB  | 3.28× |
| MR3   | 0.50 MB  | 3.93× |
| MR4   | 0.50 MB  | 4.12× |
| NM1   | 0.50 MB  | 5.15× |
| RG1   | 6.86 MB  | 1.70× |
| RG2   | 7.18 MB  | 4.23× |
| RG3   | 5.91 MB  | 6.08× |
| SC1   | 9.71 MB  | 3.71× |
| XA1   | 2.00 MB  | 5.01× |

*Table 2: Lossless compression ratios (Delta+RLE+FSE), 21 images.*

Mammography achieves the highest ratios (up to 8.79×) due to large smooth tissue regions producing near-zero RLE runs over thousands of consecutive pixels. CT with full 16-bit dynamic range achieves 2.24×. X-ray and RG1 radiography achieve the lowest (~1.7×) due to high-frequency noise or uniform backgrounds.

### 6.4 Decompression Throughput

| Platform | MR | CT | CR | XR | MG1 | MG2 | MG3 | MG4 |
|----------|:--:|:--:|:--:|:--:|:---:|:---:|:---:|:---:|
| AWS c7g.metal (ARM64, 64c) | 2,282 | 4,433 | 8,527 | 9,411 | **16,387** | 16,023 | 8,044 | 15,213 |
| AWS c7g.8xl (ARM64, 32c) | 1,524 | 2,186 | 4,290 | 4,562 | 8,901 | 7,879 | 4,455 | 7,132 |
| AWS c7i.8xl (x86, 32c) | 1,142 | 1,208 | 3,172 | 3,269 | 5,220 | 5,124 | 3,468 | 4,964 |
| Mac Studio (M2 Max, 12c) | 1,054 | 1,121 | 2,089 | 2,101 | 3,666 | 3,659 | 2,239 | 3,188 |

*Table 3: Decompression throughput (MB/s of raw pixel data) across hardware platforms.*

Key observations:
- **Peak throughput of 16.4 GB/s** on MG1 (64-core ARM64), approaching DRAM bandwidth limits. A roofline analysis [32] confirms that at 64 cores the codec operates at 60–80% of peak memory bandwidth — algorithmic optimization is effectively exhausted.
- Throughput scales roughly linearly with core count (32c ≈ half of 64c).
- ARM64 outperforms x86 at equivalent core counts (wider memory buses on Graviton3).
- RAM bandwidth is the primary bottleneck, not CPU speed.

---

## 7. Comparison with HTJ2K (OpenJPH)

We compare against lossless HTJ2K using OpenJPH [33], the leading open-source implementation. HTJ2K has been evaluated for video production [31] and medical imaging [30]; our comparison focuses specifically on lossless 16-bit DICOM. Both codecs are benchmarked as **in-process library calls** via CGO bindings. All measurements are single-threaded on Apple M2 Max.

| Image | Raw (MB) | MIC ratio | HTJ2K ratio | MIC-Go (MB/s) | MIC-4s-C (MB/s) | HTJ2K (MB/s) |
|-------|:--------:|:---------:|:-----------:|:--------------:|:----------------:|:------------:|
| MR    | 0.13     | 2.35×     | **2.38×**   | 136            | **322**          | 250          |
| CT    | 0.50     | **2.24×** | 1.77×       | 188            | **368**          | 321          |
| CR    | 7.18     | 3.69×     | **3.77×**   | 299            | **541**          | 368          |
| XR    | 10.1     | **1.74×** | 1.67×       | 305            | **545**          | 338          |
| MG1   | 9.35     | **8.79×** | 8.25×       | 487            | 692              | **809**      |
| MG2   | 9.35     | **8.77×** | 8.24×       | 476            | 685              | **797**      |
| MG3   | 27.3     | **2.24×** | 2.22×       | 311            | **529**          | 340          |
| MG4   | 26.0     | 3.47×     | **3.51×**   | 421            | **639**          | 554          |
| CT1   | 0.50     | **2.79×** | 2.70×       | 245            | **433**          | 361          |
| CT2   | 0.50     | **3.49×** | 3.29×       | 238            | 416              | **376**      |
| MG-N  | 27.3     | **2.24×** | 2.23×       | 323            | **556**          | 344          |
| MR1   | 0.50     | 2.09×     | **2.13×**   | 274            | **525**          | 326          |
| MR2   | 2.00     | 3.28×     | **3.35×**   | 339            | **585**          | 368          |
| MR3   | 0.50     | 3.93×     | **4.33×**   | 360            | **597**          | 426          |
| MR4   | 0.50     | 3.93×     | **4.21×**   | 323            | **557**          | 402          |
| NM1   | 0.50     | 5.15×     | **5.76×**   | 330            | **618**          | 416          |
| RG1   | 6.86     | **1.70×** | 1.63×       | 241            | **419**          | 334          |
| RG2   | 7.18     | 4.23×     | **4.32×**   | 365            | **608**          | 442          |
| RG3   | 5.91     | 6.08×     | **6.99×**   | 380            | **614**          | 554          |
| SC1   | 9.71     | 3.71×     | **3.85×**   | 383            | **601**          | 399          |
| XA1   | 2.00     | **5.01×** | 4.88×       | 337            | **580**          | 433          |

*Table 8: MIC vs. HTJ2K — in-process, single-threaded, Apple M2 Max, 21 images.*

**Compression ratio**: MIC leads on images with high dynamic range utilization (where wavelet coefficient overflow penalizes HTJ2K) and on images with large smooth regions (where 16-bit RLE captures long runs efficiently). HTJ2K leads on images with fine spatial structure where its context model captures higher-order statistics.

**Decompression throughput**: HTJ2K decompresses faster than MIC-Go on most modalities (MIC-Go is pure Go with no native optimizations). However, MIC-4state-SIMD with `-O3 -march=native` **exceeds HTJ2K on 17 of 21 images single-threaded on ARM64** and **18 of 21 on Intel AMD64** — a meaningful reversal of the common assumption that HTJ2K is always faster. PICS-C-8 (8 parallel strips, C pthreads) **exceeds HTJ2K on all 21 images** on both platforms, with 3–4× higher throughput on large images. At 64 cores, MIC's per-frame parallelism scales to 16 GB/s vs. HTJ2K's single-image architecture.

**Complexity**: HTJ2K's open-source implementation (OpenJPH [33]) is approximately 20,000 lines of C++ (counted with `cloc` on the OpenJPH `main` branch, excluding third-party dependencies and test fixtures). MIC's equivalent Delta+RLE+FSE pipeline is approximately 500 lines of Go. This 40× complexity difference matters for maintenance, security auditing, porting, and integration into constrained environments (embedded, browser WASM).

---

## 8. Comparison with JPEG-LS (CharLS)

JPEG-LS (ISO 14495-1) [4,5] is the closest practical competitor. We compare against CharLS [34] using in-process CGO bindings.

| Image | Raw (MB) | MIC ratio | JPEG-LS ratio | MIC-Go (MB/s) | MIC-4s-C (MB/s) | JPEG-LS (MB/s) | speedup |
|-------|:--------:|:---------:|:-------------:|:--------------:|:----------------:|:--------------:|:-------:|
| MR    | 0.13     | 2.35×     | **2.52×**     | 136            | **322**          | 95             | 3.4×    |
| CT    | 0.50     | 2.24×     | **2.68×**     | 188            | **368**          | 140            | 2.6×    |
| CR    | 7.18     | 3.69×     | **3.96×**     | 299            | **541**          | 153            | 3.5×    |
| XR    | 10.1     | 1.74×     | **1.76×**     | 305            | **545**          | 109            | 5.0×    |
| MG1   | 9.35     | 8.79×     | **8.91×**     | 487            | **692**          | 409            | 1.7×    |
| MG2   | 9.35     | 8.77×     | **8.90×**     | 476            | **685**          | 407            | 1.7×    |
| MG3   | 27.3     | 2.24×     | **2.38×**     | 311            | **529**          | 154            | 3.4×    |
| MG4   | 26.0     | 3.47×     | **3.71×**     | 421            | **639**          | 185            | 3.5×    |
| CT1   | 0.50     | 2.79×     | **3.19×**     | 245            | **433**          | 182            | 2.4×    |
| CT2   | 0.50     | 3.49×     | **4.54×**     | 238            | **416**          | 173            | 2.4×    |
| MG-N  | 27.3     | 2.24×     | **2.38×**     | 323            | **556**          | 154            | 3.6×    |
| MR1   | 0.50     | 2.09×     | **2.30×**     | 274            | **525**          | 115            | 4.6×    |
| MR2   | 2.00     | 3.28×     | **3.52×**     | 339            | **585**          | 167            | 3.5×    |
| MR3   | 0.50     | 3.93×     | **4.51×**     | 360            | **597**          | 230            | 2.6×    |
| MR4   | 0.50     | 4.12×     | **4.49×**     | 323            | **557**          | 198            | 2.8×    |
| NM1   | 0.50     | 5.15×     | **6.28×**     | 330            | **618**          | 213            | 2.9×    |
| RG1   | 6.86     | **1.70×** | **1.72×**     | 241            | **419**          | 104            | 4.0×    |
| RG2   | 7.18     | 4.23×     | **4.51×**     | 365            | **608**          | 178            | 3.4×    |
| RG3   | 5.91     | 6.08×     | **7.31×**     | 380            | **614**          | 245            | 2.5×    |
| SC1   | 9.71     | 3.71×     | **4.73×**     | 383            | **601**          | 221            | 2.7×    |
| XA1   | 2.00     | **5.01×** | **5.39×**     | 337            | **580**          | 208            | 2.8×    |

*Table 9: MIC vs. JPEG-LS — in-process, single-threaded, Apple M2 Max, 21 images. Speedup = MIC-4s-C / JPEG-LS.*

**Compression ratio**: JPEG-LS achieves better ratios on all 21 modalities. The advantage is largest on CT2 (+30%), SC1 (+27%), NM1 (+22%), and RG3 (+20%) where context-adaptive MED prediction captures fine structure. On XR and RG1 the gap is only 1–2%.

**Decompression throughput**: MIC-4state-C is **1.7–5.0× faster on all 21 modalities**. The speed advantage arises from: (1) O(1) table lookups with no context adaptation vs. per-pixel parameter updates, (2) four-state ILP breaking the serial ANS bottleneck, (3) branch-free delta vs. three-way MED conditional.

**Summary**: JPEG-LS wins on ratio. MIC wins on speed. For clinical workflows where images are compressed once and decompressed many times (archival → viewing), MIC's speed advantage outweighs the modest ratio disadvantage.

---

## 9. Comparison with Delta+Zstandard: Quantifying the 16-Bit Gap

This section provides the central empirical evidence for the paper's thesis: that byte-oriented compression systematically underperforms on 16-bit data.

| Image | MIC | d+zstd-1 | d+zstd-3 | d+zstd-19 | raw zstd-3 |
|-------|:---:|:--------:|:--------:|:---------:|:----------:|
| MR    | **2.35×** | 1.75× | 1.82× | 1.95× | 1.65× |
| CT    | **2.24×** | 1.71× | 1.89× | 2.03× | 1.79× |
| CR    | **3.63×** | 2.70× | 2.95× | 3.27× | 2.05× |
| XR    | **1.74×** | 1.43× | 1.43× | 1.43× | 1.32× |
| MG1   | **8.57×** | 6.19× | 6.37× | 7.07× | 5.77× |
| MG2   | **8.55×** | 6.18× | 6.36× | 7.04× | 5.75× |
| MG3   | **2.29×** | 1.71× | 1.89× | 2.09× | 1.50× |
| MG4   | **3.47×** | 2.80× | 2.87× | 2.99× | 2.57× |

*Table 10: MIC vs. Delta+Zstandard at varying compression levels and raw+Zstandard.*

MIC outperforms Delta+zstd on **every modality**, even at zstd's highest compression level (19). The advantage ranges from 10% (MG3) to 22% (MG1). The gap is most pronounced on mammography, where MIC's 16-bit RLE encodes long runs of identical residuals that zstd's byte-oriented LZ77 cannot exploit.

The raw+zstd column confirms delta encoding is essential (removing it reduces ratio by 10–44%). But even with delta encoding, zstd cannot match MIC's RLE+FSE backend. In all cases, 16-bit-native RLE+FSE outperforms byte-oriented compression by 10–22%.

---

## 10. Parallel Scaling: PICS, Multi-Frame, and WSI

### 10.1 PICS: Parallel Image Compressed Strips

PICS breaks the serial row dependency in delta prediction by dividing the image into N horizontal strips. Each strip is independently compressed/decompressed. Strip boundaries reset the top-neighbor predictor to zero — the only accuracy impact.

**Compression ratio vs. strip count**:

| Image | 1-strip | PICS-4 | PICS-8 | Image | 1-strip | PICS-4 | PICS-8 |
|-------|:-------:|:------:|:------:|-------|:-------:|:------:|:------:|
| MR (256×256)    | **2.35×** | 2.28× | 2.21× | CT1 (512×512)     | **2.79×** | 2.54× | 2.29× |
| CT (512×512)    | **2.24×** | 2.15× | 1.96× | CT2 (512×512)     | **3.49×** | 3.11× | 2.72× |
| CR (2140×1760)  | 3.69×     | **3.70×** | 3.71× | MG-N (3064×4664)  | 2.24×     | **2.31×** | 2.34× |
| XR (2048×2577)  | 1.74×     | **1.75×** | 1.76× | MR1 (512×512)     | **2.09×** | 2.10× | 2.08× |
| MG1 (2457×1996) | 8.79×     | 8.84× | **8.87×** | MR2 (1024×1024)   | 3.28×     | **3.31×** | 3.31× |
| MG2 (2457×1996) | 8.77×     | 8.83× | **8.85×** | MR3 (512×512)     | **3.93×** | 3.89× | 3.84× |
| MG3 (4774×3064) | 2.24×     | **2.31×** | 2.34× | MR4 (512×512)     | **4.12×** | 4.09× | 4.03× |
| MG4 (4096×3328) | 3.47×     | **3.59×** | 3.62× | NM1 (256×1024)    | 5.15×     | **5.26×** | 5.28× |
|                 |           |       |       | RG1 (1841×1955)   | **1.70×** | **1.70×** | 1.69× |
|                 |           |       |       | RG2 (1760×2140)   | 4.23×     | **4.28×** | 4.30× |
|                 |           |       |       | RG3 (1760×1760)   | 6.08×     | **6.11×** | 6.12× |
|                 |           |       |       | SC1 (2048×2487)   | 3.71×     | **3.73×** | 3.74× |
|                 |           |       |       | XA1 (1024×1024)   | 5.01×     | **5.04×** | 5.03× |

*Table 11: Compression ratio vs. strip count, 21 images. Large images gain with more strips; small images (MR, CT, small MR variants) lose.*

**The strip adaptation phenomenon**: Large images (CR, MG) *improve* compression with more strips because each strip receives a dedicated FSE table that specializes to the strip's local residual distribution, providing free context adaptation through partitioning. A single global table must model the full image's statistical variation, including tissue-background boundaries.

Formally: if each strip $S_k$ has its own entropy $H(S_k)$, and the image entropy is $H(S)$, then when the image is non-stationary along the strip dimension:

$$\sum_k |S_k| \cdot H(S_k) < |S| \cdot H(S)$$

This inequality holds for CR/MG images and explains the crossover where strip boundary loss is outweighed by local adaptation gain.

**Decompression throughput**:

| Image | MIC-Go | MIC-4s-C | PICS-2 | PICS-4 | PICS-8 |
|-------|:------:|:--------:|:------:|:------:|:------:|
| MR    | 136    | 322      | 299    | 262    | 245 ⚠  |
| CT    | 188    | 368      | 342    | **478** | 467   |
| CR    | 299    | 541      | 549    | 1,002  | **1,625** |
| XR    | 305    | 545      | 588    | 1,066  | **1,730** |
| MG1   | 487    | 692      | 888    | 1,456  | **2,411** |
| MG2   | 476    | 685      | 877    | 1,464  | **2,376** |
| MG3   | 311    | 529      | 577    | 1,110  | **1,993** |
| MG4   | 421    | 639      | 781    | 1,369  | **2,040** |
| CT1   | 245    | 433      | 391    | **542** | 484   |
| CT2   | 238    | 416      | 394    | **486** | 428   |
| MG-N  | 323    | 556      | 582    | 1,092  | **1,894** |
| MR1   | 274    | 525      | 443    | 609    | **613** |
| MR2   | 339    | 585      | 579    | 894    | **1,163** |
| MR3   | 360    | 597      | 530    | **774** | 753   |
| MR4   | 323    | 557      | 479    | 664    | **688** |
| NM1   | 330    | 618      | 502    | 611    | **710** |
| RG1   | 241    | 419      | 448    | 796    | **1,269** |
| RG2   | 365    | 608      | 635    | 1,108  | **1,715** |
| RG3   | 380    | 614      | 657    | 1,176  | **1,635** |
| SC1   | 383    | 601      | 699    | 1,233  | **1,996** |
| XA1   | 337    | 580      | 589    | 912    | **1,232** |

*Table 12: Single-image decompression throughput (MB/s), Apple M2 Max (12-core ARM64). ⚠ = PICS goroutine overhead eliminates the parallelism benefit on this small image.*

PICS-8 peaks at 2,411 MB/s on MG1, enabling sub-millisecond decompression of a 9.35 MB image on a 12-core workstation.

### 10.2 Multi-Frame Results (MIC2)

| Mode | Compressed | Ratio |
|------|:----------:|:-----:|
| Independent (spatial) | 46.1 MB | **13.3×** |
| Temporal (inter-frame) | 47.5 MB | 12.9× |

*Table 13: 69-frame breast tomosynthesis (2457×1890×69, 614 MB raw).*

Independent mode outperforms temporal mode on this dataset. **Why temporal prediction fails on DBT**: Tomosynthesis frames differ in X-ray projection angle, not temporal motion. Each frame images a different cross-section of breast tissue — there is no inter-frame pixel correlation to exploit. The spatial predictor is already near-optimal on smooth mammography.

Temporal mode is a design provision for sequences with genuine temporal correlation: cardiac cine MRI, fluoroscopy, nuclear medicine dynamic studies — where consecutive frames share the same anatomy with only contrast or motion changes. We have not yet identified a clinical dataset where temporal mode outperforms independent mode; this remains an open evaluation.

### 10.3 WSI Compression (MIC3)

MIC3 is a tiled container for whole slide pathology images with:
- **YCoCg-R reversible color transform**: RGB → Y/Co/Cg decorrelation
- **Tiled architecture**: 256×256 tiles, O(1) random access
- **Pyramid levels**: Multi-resolution via 2×2 box filter downsampling
- **Constant-plane optimization**: Background tiles compress to 15–17 bytes

| Tile Type | Ratio | Notes |
|-----------|:-----:|-------|
| White background | 1,946× | Near-zero entropy |
| Dense tissue (H&E) | 4.4× | Smooth staining gradients |
| Gradient | 5.4× | Excellent spatial correlation |
| Mixed slide | 4–8× | Weighted average of tile types |

*Table 14: WSI tile compression ratios (MIC3, YCoCg-R + Delta+RLE+FSE).*

---

## 11. Implementation Notes

### 11.1 Go Assembly Optimizations

MIC includes platform-specific Go assembly routines for amd64 and arm64, with pure-Go fallbacks for all other architectures.

- **Four-state FSE decode kernel**: BMI2 `SHLXQ`/`SHRXQ` on amd64; `LSLV`/`LSRV` on arm64.
- **Interleaved histogram** (`countSimpleU16Asm`): 4-way unrolled loop distributing even/odd pixels into separate count arrays to avoid store-to-load forwarding stalls.
- **AVX2 wavelet kernels**: `wt53PredictAVX2`/`wt53UpdateAVX2` processing 8 int32 values per cycle for the blocked column pass.

### 11.2 Branch-Free Delta Decompression

The delta decompressor uses four separate loops for corner, first-row, first-column, and interior pixels. The interior loop handles >(w−1)(h−1) out of wh pixels without any per-pixel boundary checks, eliminating branch mispredictions.

### 11.3 RLE Fast Path

Same-value runs (the most common pattern after delta coding) are fast-pathed to return the cached value without touching the input slice, requiring only a counter decrement per output symbol.

### 11.4 Browser Decoders — Web Distribution Without a Server

A practical advantage MIC holds over all DICOM-standard codecs is **browser-native decoding**. HTJ2K has no production-ready browser decoder [35]; JPEG-LS has none either (CharLS [34] provides no WebAssembly build or JavaScript package). To view a JPEG 2000 image in a web browser today, the server must transcode it — adding latency, server load, and a single point of failure.

MIC ships two browser decoder implementations:

| Decoder | Size | Dependencies | Build step |
|---------|------|:------------:|:----------:|
| `mic-decoder.js` (pure JS ES module) | ~15 KB | None | None |
| `mic-decoder.wasm` (Go compiled to WASM) | ~2.5 MB | `wasm_exec.js` (17 KB) | `GOOS=js GOARCH=wasm go build` |

The JavaScript decoder is a complete, self-contained implementation of the Delta+RLE+FSE pipeline in 15 KB of ES module code with zero npm dependencies. It works in any browser since 2020 (Chrome 67+, Firefox 68+, Safari 14+). The only non-trivial porting requirement is FSE's 64-bit reverse bit reader, which the JS decoder handles using `BigInt` with explicit uint64 masking.

**Single-threaded throughput** (Apple M2 Max, Node.js v24.8, median over 20 iterations):

| Image | Dimensions | Ratio | Median ms | MB/s | MP/s |
|-------|-----------|:-----:|:---------:|:----:|:----:|
| MR | 256×256 | 2.35× | 3.0 | 42 | 21.8 |
| CT | 512×512 | 2.24× | 13.5 | 37 | 19.5 |
| CR | 1760×2140 | 3.69× | 161.2 | 45 | 23.4 |
| MG1 | 1996×2457 | 8.79× | 80.9 | 116 | 60.6 |
| MG3 | 3064×4774 | 2.29× | 638.4 | 44 | 22.9 |

*Table 16: JavaScript decoder throughput (4-state), Node.js v24.8, Apple M2 Max.*

**Parallel decoding via PICS + `worker_threads`**: PICS files encode independent strips, enabling parallel browser decoding without SharedArrayBuffer synchronization. Each strip is a self-contained compressed blob — a worker receives its strip, decompresses it independently, and returns the pixel buffer.

| Image | strips | workers | Median ms | MB/s | Speedup |
|-------|:------:|:-------:|:---------:|:----:|:-------:|
| MR 256×256 | 4 | 8 | **1.3** | 94 | 2.57× |
| CT 512×512 | 4 | 8 | **5.0** | 101 | 2.95× |
| CR 1760×2140 | 8 | 12 | **31.7** | 227 | 5.53× |
| MG1 1996×2457 | 8 | 12 | **19.4** | 483 | 4.36× |

*Table 17: PICS parallel JavaScript decoder, `worker_threads`, Apple M2 Max. Speedup relative to 1 worker.*

MG1 (a 9.35 MB mammography image) decompresses in **19.4 ms** at 483 MB/s using 12 browser workers — well into real-time territory for diagnostic viewing. CR (a 7.18 MB radiography image) decompresses in 31.7 ms at 227 MB/s.

**Why this matters for distribution**: A web-based DICOM viewer can fetch a `.mic` file directly from object storage (S3, GCS) and decode it client-side — zero server-side compute, zero transcoding latency, works offline, works in service workers. The same compressed archive file that a server stores is the file the browser downloads and decodes directly. This is not possible today with HTJ2K or JPEG-LS without a server-side proxy.

Both the JavaScript and WASM decoders support all MIC container formats (MIC1 single-frame, MIC2 multi-frame with movie playback, MIC3 WSI tiles with pyramid level selector and RGB rendering).

---

## 12. Discussion

### 12.1 The Cost-of-Complexity Frontier

For each additional percentage of compression ratio gained by a more complex predictor or transform, what is the decompression throughput cost? The table below summarizes the tradeoffs across all MIC variants and competing codecs:

| Codec / Variant | Geo. Mean Ratio | Geo. Mean Decomp (MB/s) | Code size | Browser decoder |
|-----------------|:--------------:|:-----------------------:|:---------:|:---------------:|
| Delta+RLE+FSE (Go) | 3.12× | 310 | ~500 LOC | **Yes (15 KB JS)** |
| Delta+RLE+FSE (4s-C) | 3.12× | 530 | ~1,500 LOC | **Yes** |
| Wavelet V2 SIMD | 3.28× | 780 | ~2,000 LOC | Possible |
| HTJ2K (OpenJPH) | 3.15× | 460 | ~20,000 LOC | No |
| JPEG-LS (CharLS) | 3.44× | 130 | ~5,000 LOC | No |
| Delta+zstd-19 | 2.72× | ~300 | N/A | Partial (zstd-js [36]) |

*Table 15: Pareto frontier of compression ratio vs. decompression throughput (approximate geometric means across all modalities), with implementation complexity and browser deployability.*

This table reveals two key design insights. First, JPEG-LS trades 58% of decompression speed for 10% more compression — unfavorable for clinical systems that decompress 10× more often than they compress. MIC's 4-state-C variant achieves 4× the throughput of JPEG-LS at 91% of its ratio. Second, HTJ2K's ~20,000-line implementation is ~40× larger than MIC's Delta+RLE+FSE pipeline and has no practical browser decoder [35], making it unsuitable for client-side web deployment. MIC achieves competitive or better performance at a fraction of the complexity, and the 15 KB JavaScript decoder makes it uniquely deployable in web browsers.

### 12.2 Pipeline Selection Heuristic

When should each pipeline be used? Our results suggest the following decision framework:

| Condition | Recommended Pipeline |
|-----------|---------------------|
| Effective bit depth ≤ 12, speed priority | Delta+RLE+FSE (4-state) |
| Effective bit depth ≤ 12, ratio priority | Wavelet V2 SIMD |
| Full 16-bit dynamic range (CT) | Delta+RLE+FSE (avoid wavelet) |
| Image ≥ 0.5 MB, multi-core available | Add PICS (2–8 strips) |
| Multi-frame sequence | MIC2 independent mode |
| RGB pathology / WSI | MIC3 (YCoCg-R + tiled) |
| Lossy/progressive needed | Use HTJ2K (MIC does not support lossy) |

### 12.3 Limitations

- **No lossy compression, progressive decoding, or region-of-interest coding** for greyscale images. The wavelet pipeline provides a natural foundation for future lossy mode.
- **Encoding speed not reported**: This paper focuses on decompression throughput, consistent with the write-once, read-many PACS archival deployment model. Encoding speed benchmarks are left to future work.
- **Test dataset size**: 21 test images across 10 modalities drawn from three public de-identified repositories (NEMA WG-04, NEMA 1997 CD, Clunie DBT Case 1). The expanded dataset substantially broadens the evaluation, but large-scale benchmarks from public repositories (e.g., TCIA) with inter-institution variability would further strengthen generality claims.
- **Temporal prediction** is underperforming on the one available clinical dataset and needs evaluation on cardiac cine MRI and fluoroscopy.
- **WSI results** are on synthetic tiles only; real whole-slide pathology benchmarks are needed.
- **Benchmark confidence intervals**: Run-to-run variance is <2% on Apple M2 Max and <5% on AWS instances (verified with `-count=5`); formal confidence intervals are deferred to future work.
- **JPEG-XL** [6]: Designed primarily for 8-bit natural images with no 16-bit DICOM pathway; not compared.

### 12.4 Pathway to Clinical Impact

MIC could be registered as a DICOM Private Transfer Syntax, allowing PACS vendors to adopt it without modifying the DICOM standard. For whole slide imaging, DICOM Supplement 145 [29] defines the WSI IOD; MIC3's tiled format aligns with this architecture. Herrmann *et al.* [28] describe the practical challenges of implementing DICOM for digital pathology — MIC3's tile-level random access and pyramid support address these requirements directly.

The 15 KB JavaScript decoder enables a direct storage-and-serve distribution model: compressed MIC files can be fetched from object storage and decoded client-side, eliminating server-side transcoding. This is currently not achievable with HTJ2K or JPEG-LS, which have no production browser decoder. The open-source implementation and minimal dependency footprint make MIC a practical candidate for deployment in web-based DICOM viewers and cloud-native PACS architectures.

---

## 13. Conclusion

We have presented MIC, a lossless medical image codec built on a simple observation: the compression ecosystem has a 30-year blind spot for 16-bit data. By extending FSE to 65,535 symbols and pairing it with a 16-bit-native RLE stage, MIC outperforms byte-oriented Zstandard by 10–22% on all tested modalities. The four-state interleaved ANS decoder demonstrates that entropy coder design should target instruction-level parallelism width, not just coding efficiency, achieving 66–142% FSE speedup with no ratio penalty.

The simplicity thesis is validated empirically: a three-stage Delta+RLE+FSE pipeline (~500 lines of code) matches or beats JPEG 2000's wavelet+EBCOT on 7/8 medical modalities for lossless compression, at dramatically higher throughput. When maximum ratio is needed, a five-level wavelet alternative with SIMD acceleration exceeds both the delta pipeline and HTJ2K.

Key results across 21 clinical DICOM images spanning 10 modalities:
- **Compression ratios**: 1.7×–8.9× (greyscale), 3–5× (RGB WSI tissue)
- **Decompression throughput**: up to 16 GB/s (64-core ARM64), geometric mean ≈7.5 GB/s
- **vs. HTJ2K**: MIC-4state-SIMD exceeds HTJ2K on 17/21 images single-threaded (ARM64) and 18/21 (Intel AMD64 with `-march=native`); PICS-C-8 exceeds HTJ2K on **all 21 images** on both platforms; MIC pipeline is ~40× simpler
- **vs. JPEG-LS**: 1.7–5.0× faster decompression; 1–30% lower ratio depending on modality
- **vs. Delta+Zstandard**: 10–22% better compression on all modalities
- **Browser decoder**: 15 KB pure JS decoder enables client-side decoding in any modern browser; PICS + `worker_threads` achieves 483 MB/s (MG1) — equivalent to native-code performance in the browser, with no server-side transcoding

MIC is open-source and available at https://github.com/pappuks/medical-image-codec.

---

## References

### Standards and Core Codecs

1. NEMA, "Digital Imaging and Communications in Medicine (DICOM)," https://www.dicomstandard.org/, 2024.
2. D. S. Taubman and M. W. Marcellin, *JPEG2000: Image Compression Fundamentals, Standards and Practice*, Springer, 2002.
3. D. S. Taubman, "High throughput JPEG 2000 (HTJ2K): Algorithm, performance evaluation, and potential," *Proc. SPIE*, vol. 11137, 2019.
4. M. J. Weinberger, G. Seroussi, and G. Sapiro, "The LOCO-I lossless image compression algorithm: Principles and standardization into JPEG-LS," *IEEE Trans. Image Process.*, vol. 9, no. 8, pp. 1309–1324, 2000. DOI: 10.1109/83.855427
5. ISO/IEC 14495-2:2003, "Information technology — Lossless and near-lossless compression of continuous-tone still images: Extensions — Part 2," International Organization for Standardization, 2003.
6. J. Alakuijala, R. van Asseldonk, S. Boukortt, M. Bruse, I.-M. Comsa, M. Firsching, T. Fischbacher, E. Kliuchnikov, S. Gomez, R. Obryk, K. Potempa, A. Rhatushnyak, J. Sneyers, Z. Szabadka, L. Vandevenne, L. Versari, and J. Wassenberg, "JPEG XL next-generation image compression architecture and coding tools," *Proc. SPIE 11137, Applications of Digital Image Processing XLII*, 111370K, Sep. 2019. DOI: 10.1117/12.2529237

### Entropy Coding and ANS Theory

7. J. Duda, "Asymmetric numeral systems," arXiv:0902.0271, 2009.
8. J. Duda, "Asymmetric numeral systems: entropy coding combining speed of Huffman coding with compression rate of arithmetic coding," arXiv:1311.2540, Nov. 2013.
9. Y. Collet, "Finite State Entropy — A new breed of entropy coder," https://github.com/Cyan4973/FiniteStateEntropy, 2013.
10. Y. Collet and M. Kucherawy, "Zstandard Compression and the 'application/zstd' Media Type," RFC 8878, IETF, Feb. 2021. DOI: 10.17487/RFC8878
11. E. S. Schwartz and B. Kallick, "Generating a canonical prefix encoding," *Commun. ACM*, vol. 7, no. 3, pp. 166–169, 1964.
12. S. Mahapatra and K. Singh, "An FPGA-based implementation of multi-alphabet arithmetic coding," *IEEE Trans. Circuits Syst. I, Reg. Papers*, vol. 54, no. 8, pp. 1678–1686, Aug. 2007.
13. D. Kosolobov, "Efficiency of ANS Entropy Encoders," arXiv:2201.02514, Jan. 2022.
14. R. Bamler, "Understanding Entropy Coding With Asymmetric Numeral Systems (ANS): a Statistician's Perspective," arXiv:2201.01741, Jan. 2022.

### Multi-Stream ANS and Parallel Entropy Decoding

15. F. Giesen, "Interleaved entropy coders," arXiv:1402.3392, Feb. 2014.
16. F. Giesen, "ryg_rans: Public domain rANS encoder/decoder," https://github.com/rygorous/ryg_rans, 2014.
17. F. Lin, K. Arunruangsirilert, H. Sun, and J. Katto, "Recoil: Parallel rANS Decoding with Decoder-Adaptive Scalability," *Proc. 52nd International Conference on Parallel Processing (ICPP '23)*, Salt Lake City, pp. 31–40, 2023. DOI: 10.1145/3605573.3605588
18. A. Weissenberger and B. Schmidt, "Massively Parallel ANS Decoding on GPUs," *Proc. 48th International Conference on Parallel Processing (ICPP '19)*, Kyoto, Article 100, 2019. DOI: 10.1145/3337821.3337888

### Image Prediction and Lossless Coding

19. X. Wu and N. Memon, "Context-based, adaptive, lossless image coding," *IEEE Trans. Communications*, vol. 45, no. 4, pp. 437–444, Apr. 1997. DOI: 10.1109/26.585919
20. J. Sneyers and P. Wuille, "FLIF: Free Lossless Image Format based on MANIAC Compression," *Proc. IEEE International Conference on Image Processing (ICIP)*, Phoenix, AZ, Sep. 2016. DOI: 10.1109/ICIP.2016.7532320

### Wavelet Transforms and Lifting

21. D. Le Gall and A. Tabatabai, "Sub-band coding of digital images using symmetric short kernel filters and arithmetic coding techniques," *Proc. IEEE ICASSP*, pp. 761–764, 1988.
22. W. Sweldens, "The Lifting Scheme: A custom-design construction of biorthogonal wavelets," *Applied and Computational Harmonic Analysis*, vol. 3, no. 2, pp. 186–200, 1996. DOI: 10.1006/acha.1996.0015
23. I. Daubechies and W. Sweldens, "Factoring wavelet transforms into lifting steps," *J. Fourier Anal. Appl.*, vol. 4, no. 3, pp. 247–269, 1998. DOI: 10.1007/BF02476026
24. A. R. Calderbank, I. Daubechies, W. Sweldens, and B.-L. Yeo, "Wavelet transforms that map integers to integers," *Applied and Computational Harmonic Analysis*, vol. 5, no. 3, pp. 332–369, 1998.

### Color Transforms

25. H. S. Malvar and G. J. Sullivan, "YCoCg-R: A color space with RGB reversibility and low dynamic range," JVT Doc. JVT-I014, 2003.

### Medical Imaging Compression Studies

26. F. Liu, M. Hernandez-Cabronero, V. Sanchez, M. W. Marcellin, and A. Bilgin, "The Current Role of Image Compression Standards in Medical Imaging," *Information*, vol. 8, no. 4, p. 131, Oct. 2017. DOI: 10.3390/info8040131
27. D. A. Clunie, "Lossless Compression of Grayscale Medical Images: Effectiveness of Traditional and State-of-the-Art Approaches," *Proc. SPIE Medical Imaging*, 2000.

### Digital Pathology and Whole Slide Imaging

28. M. D. Herrmann, D. A. Clunie, A. Fedorov, *et al.*, "Implementing the DICOM Standard for Digital Pathology," *J. Pathol. Inform.*, vol. 9, no. 1, p. 37, 2018. DOI: 10.4103/jpi.jpi_42_18
29. DICOM Standards Committee, "Supplement 145: Whole Slide Microscopic Image IOD and SOP Classes," DICOM Standard, 2010.

### HTJ2K Performance and Applications

30. D. Taubman, A. Naman, and R. Mathew, "High Throughput Block Coding in the HTJ2K Compression Standard," *Proc. IEEE International Conference on Image Processing (ICIP)*, 2019.
31. D. Taubman, A. Naman, M. Smith, P.-A. Lemieux, H. Saadat, O. Watanabe, and R. Mathew, "High Throughput JPEG 2000 for Video Content Production and Delivery Over IP Networks," *Frontiers in Signal Processing*, vol. 2, Article 885644, Apr. 2022. DOI: 10.3389/frsip.2022.885644

### Performance Modeling

32. S. Williams, A. Waterman, and D. Patterson, "Roofline: An insightful visual performance model for multicore architectures," *Communications of the ACM*, vol. 52, no. 4, pp. 65–76, Apr. 2009. DOI: 10.1145/1498765.1498785

### Implementations

33. A. N. Aous, "OpenJPH: An open-source implementation of High-Throughput JPEG 2000," https://github.com/aous72/OpenJPH, 2024.
34. Team CharLS, "CharLS: A C/C++ JPEG-LS library implementation," https://github.com/team-charls/charls, 2024.
35. As of the submission date of this paper, no production-ready WebAssembly or JavaScript decoder exists for High-Throughput JPEG 2000. The OpenJPH repository [33] does not provide a WASM build or npm package. A search of npm, CDNjs, and the jsDelivr registry finds no HTJ2K browser decoder. The closest available tool is `openjpeg.js`, a WASM build of OpenJPEG targeting standard JPEG 2000 (not HTJ2K). Readers are encouraged to verify current availability at https://github.com/aous72/OpenJPH/issues.
36. N. Tindall *et al.*, "fzstd: Pure JavaScript Zstandard decompressor," https://github.com/101arrowz/fzstd, 2024. (Alternative implementations include `@mongodb-js/zstd` and the Emscripten-compiled `libzstd.js`; all support decompression only and do not natively handle delta-encoded 16-bit pixel streams.)
