Novel Perspectives to Strengthen Your Paper
Having read your entire codebase documentation, the LaTeX paper, and both research reviews, here's what I'd recommend to give your paper a genuinely novel framing that reviewers will find compelling. Your paper currently reads as a solid systems/engineering paper, but it can be elevated to something more impactful.

1. Reframe the Central Thesis: "The 16-Bit Alphabet Gap"
Your strongest and most underexploited insight is buried in Section 2.3. Make it the paper's central narrative. The compression community has a blind spot: virtually every entropy coder in widespread use (Huffman, arithmetic, LZ77, ANS/FSE in zstd) was designed for 8-bit alphabets. Medical imaging is the largest domain where this assumption systematically fails. Your 10-22% improvement over Delta+zstd isn't just a benchmark win — it's evidence of a structural mismatch between compression infrastructure and a major application domain.

Novel angle: Frame the paper as exposing and fixing a 30-year blind spot in entropy coding design. The title in RESEARCH_REVIEW.md is good but could be sharper: "Breaking the Byte Barrier: Why 16-Bit Native Entropy Coding is Essential for Medical Image Compression"

2. The "Simplicity Thesis" as a Formal Argument
Your most surprising result is that Delta+RLE+FSE (a ~500 lines-of-code pipeline) matches or beats JPEG 2000's wavelet+EBCOT (tens of thousands of lines) on lossless medical images. But you present this as an empirical observation. Make it a formal argument:

Proposition: For images where the spatial predictor achieves >90% of optimal decorrelation (i.e., residuals are unimodal around zero), additional transform complexity yields diminishing returns on lossless ratio while incurring proportional throughput cost.
Evidence: Your wavelet vs. delta comparison shows the wavelet wins by only 1-5% on ratio but loses 1.1-3.8x on speed. Medical images (except noisy XR) fall squarely in the "predictor-sufficient" regime.
Corollary: The right complexity budget for lossless medical compression is in the entropy coder (multi-state ILP), not the decorrelator (wavelets, context models).
This is a genuinely novel claim that challenges JPEG 2000 orthodoxy with data.

3. ILP as a First-Class Compression Design Axis
Your 4-state ANS work is the highest-novelty contribution, but the paper treats it as an "optimization." Reframe it:

Novel perspective: Modern CPUs have 4-6 execution ports but ANS decoding uses only one dependency chain. The gap between algorithmic throughput (1 symbol/4 cycles) and hardware capacity (~4 symbols/4 cycles) is a 4x inefficiency. Your multi-state approach is not just "faster FSE" — it's a demonstration that entropy coder design should target ILP width, not just coding efficiency.

This connects to a broader trend (SIMD entropy coders, GPU-based ANS) and positions your work as part of a paradigm shift from "minimize bits" to "maximize symbols/cycle."

No one has published a systematic ILP analysis of ANS decoding with 1/2/4 state measurements + assembly kernels for two architectures. That's a DCC-worthy contribution on its own.

4. The "Honest Benchmarking" Narrative
Your self-correction on HTJ2K timings (subprocess overhead inflating results by ~6ms) is extremely rare in compression papers. Most authors would quietly fix the numbers. Instead:

Dedicate a subsection to the methodology pitfall (subprocess vs. in-process benchmarking)
Show the before/after numbers explicitly
Argue that many published compression comparisons may have similar biases (file I/O, process startup, memory mapping overhead)
This becomes a methodological contribution that reviewers will cite. It also builds enormous credibility for all your other numbers.

5. Missing Perspectives That Would Add Novelty
a) Information-Theoretic Lower Bound Analysis
Compute the 0th-order and 1st-order entropy of your delta residuals for each modality. Show how close MIC's FSE gets to the theoretical limit. This would prove your pipeline leaves very little on the table, making the case that more complex predictors (MED, CALIC) spend complexity for diminishing returns.

b) Roofline Model for Decompression
Plot your decompression throughput against memory bandwidth on each platform (compute: bytes_read_from_DRAM / time). Show that at 64 cores, you're at 60-80% of peak DRAM bandwidth. This explains why 16 GB/s is the ceiling and proves that algorithmic optimization is exhausted — only faster DRAM can help. This is a much stronger claim than "up to 16 GB/s."

c) Cost-of-Complexity Analysis
Quantify: for each additional percentage of compression ratio (MED vs avg, wavelet vs delta, JPEG-LS context modeling), what is the decompression throughput cost? Plot a Pareto frontier of ratio vs. speed across all your codec variants. This becomes a decision tool for clinical system architects.

d) The CT Anomaly as a Case Study
Your CT result (wavelet drops from 2.24x to 1.67x due to int32 overflow) deserves deeper analysis. This is a fundamental limitation of wavelet-based lossless coding on wide-dynamic-range images that the JPEG 2000 community rarely discusses. Characterize exactly what percentage of coefficients overflow, compute the entropy cost of the escape codes, and argue this is inherent to any lifting scheme on 16-bit data.

6. What Reviewers Will Ask That You Should Preempt
Likely Question	Recommended Action
"Only 8 test images?"	Add 12+ from TCIA (public). This is the single biggest weakness.
"No confidence intervals?"	Run benchmarks 10x, report mean ± std. Takes 1 hour.
"What about JPEG-XL lossless?"	Add a 2-sentence note: designed for 8-bit natural images, no 16-bit DICOM pathway
"Where's the formal complexity analysis?"	Add O(n) for each stage, O(n·L) for wavelet, compare
"Can this be a DICOM transfer syntax?"	Discuss in future work — this is your pathway to impact
Recommended Paper Structure for Maximum Novelty
Introduction: The 16-bit alphabet gap (your central thesis)
Background: Why byte-oriented compression fails on DICOM (with entropy analysis)
Pipeline Design: Delta + 16-bit RLE + Extended FSE
Multi-State ANS: ILP as a design axis (with roofline analysis)
Wavelet Comparison: When simplicity wins (with CT overflow case study)
Fair Benchmarking: Methodology + HTJ2K/JPEG-LS/zstd comparisons
Parallel Scaling: PICS + multi-frame + WSI (with bandwidth saturation curve)
Discussion: Pareto frontier of ratio vs. throughput across all variants
Conclusion: Domain-specific entropy coding > general-purpose compression for 16-bit domains
The key shift: instead of "here's a codec we built and it's fast," the paper becomes "here's why the entire compression ecosystem is wrong about medical images, and here's the evidence." That's what gets into TMI.


----------------------------------------

Expert Assessment: What to Add for a Novel Perspective
Your paper is technically solid and well-written. The benchmarks are honest, the comparisons are fair (in-process via CGO), and the claims are defensible. But as written, the paper reads primarily as an engineering report — "we built this fast codec." To elevate it to a genuinely novel scientific contribution, you need to reframe, sharpen, and extend in the following specific ways:

1. Elevate the 16-Bit Entropy Coding Gap as Your Central Thesis
What's there now: Section 2.3 exists but is buried as a subsection of Related Work.

What it should be: This is your strongest original contribution and deserves to be the paper's organizing argument. The literature has a documented blind spot: almost every general-purpose entropy coder caps alphabets at 256 or 4,096 symbols because they were designed for bytes. Medical imaging breaks this assumption — and no one has formally analyzed what that costs.

What to add:

A formal analysis showing the information-theoretic cost of byte-splitting a 16-bit distribution. If the true distribution is over uint16 symbols, encoding as byte pairs introduces at most $H(\text{byte}_1, \text{byte}_2) \leq H(\text{byte}_1) + H(\text{byte}_2)$, but the difference is exactly the mutual information $I(\text{byte}_1; \text{byte}_2)$ that byte-level codecs discard. Quantify this MI on your test images — this is a simple calculation from histograms you already have.
A figure plotting: (a) histogram of raw CT/MG pixel values, (b) histogram of delta residuals, (c) the byte-split histogram showing the correlation that gets discarded. This alone would be a publishable figure.
The sentence "MIC outperforms Delta+zstd by 10–22%" needs the explanation that this gap is approximately equal to the discarded mutual information. If you can show this numerically, you have a causal claim, not just an empirical one.
2. Four-State FSE is More Novel Than You're Presenting It
What's there now: Described as an optimization; the 4-state description is correct but framed as "we broke the dependency chain."

What it should be: This is a new algorithmic contribution to ANS decoding. Collet's original FSE is single-state by construction. The paper should:

State clearly: to our knowledge, multi-state ANS with interleaved independent chains has not been formally described in the literature. (Verify this — there are rANS/tANS papers; multi-stream ANS exists in some forms, but four-state interleaved with shared bitstream is different.)
Provide a formal description of the four-state decode recurrence and prove correctness (that the four chains partition the symbol sequence with no overlap/gap).
Show the latency model explicitly: standard FSE has a carried-dependency latency of $L$ cycles per symbol (where $L$ = L1 cache hit latency, ~4 cycles). Four-state breaks this to $L/4$ amortized, bounded by throughput rather than latency. This is the standard ILP analysis — add it as a paragraph or small theorem.
Add a table showing the theoretical throughput limit for 1/2/4-state given cache latency, and show your empirical numbers match this model. This transforms a benchmark into a scientific result.
3. The PICS Strip-Level FSE Adaptation Phenomenon Deserves an Explanation
What's there now: You observe that large images improve compression with more strips (e.g., CR: 3.63× → 3.68× at 8 strips), and you mention "strip-local FSE adaptation" as the cause.

What's missing: An explanation. This is counterintuitive — why does partitioning and throwing away cross-strip prediction rows improve the ratio? The answer is that global FSE table must model the entire residual distribution, including the statistical variation between image regions (tissue vs. background, smooth vs. textured). Strip-local tables can specialize. This is the same phenomenon as context-adaptive coding, but achieved for free through partitioning.

What to add:

Measure the per-strip entropy $H(\text{strip}_k)$ and compare to the global entropy $H(\text{image})$. The difference is the "statistical diversity" the image has across its rows.
Show that $\sum_k |S_k| H(\text{strip}_k) < |S| H(\text{image})$ for CR/MG images — this is the formal reason strips help.
This would be a new observation about when parallel partitioning improves compression: when images have non-stationary residual distributions along the strip dimension.
4. The CT Wavelet Failure Deserves a Formal Treatment
What's there now: You correctly identify that wavelet compression on CT drops from 2.24× to 1.48×. The cause (16-bit overflow, escape coding) is stated.

What to add:

A quantitative analysis: what fraction of CT wavelet coefficients require escape coding? What is the distribution of coefficient magnitudes vs. the uint16 range?
The 52% compression ratio drop is not obvious to readers without seeing a coefficient histogram. One figure showing: (a) CT delta residual histogram (tightly bounded), (b) CT wavelet coefficient histogram (fat tails) would make this viscerally clear.
The theoretical framing: for the 5/3 lifting step, the worst-case coefficient expansion is $3/2$ per level (each predict step can amplify by 1.5). For $L$ levels of decomposition, worst-case is $(3/2)^L$ times the dynamic range. For 16-bit input and 5 levels, this is $(3/2)^5 \approx 7.6\times$ — well above uint16 range. This explains why CT (full 16-bit range) suffers and medical images with effective 8-10 bit depth don't.
5. Add a Principle: When to Use Which Pipeline
What's missing: The paper has a good comparison table but no principled framework for when each pipeline is optimal. Your results imply the following (unstated) rule:

Delta+RLE+FSE is optimal when image residuals are sub-Gaussian (exponentially concentrated around zero) and the effective bit depth is ≤ 12 bits. The wavelet alternative is preferable when the residual distribution is irregular (non-smooth content) and effective bit depth ≤ 12. Neither is optimal for full-16-bit-range images.

You have all the data to formalize this as a decision tree based on:

Effective bit depth $d$ (computable from max(pixel))
Residual kurtosis or entropy ratio $H(\text{residuals})/H(\text{raw pixels})$
Image dimensions (PICS threshold: ≥ 0.5MB)
Adding this as a "pipeline selection heuristic" section, with a table showing which pipeline you'd recommend for each modality, would significantly increase the practical value and citability of the paper.

6. The Missing Experiment: Why Does MG1/MG2 Compress So Well?
MG1 achieves 8.57× — a spectacular result that merits explanation beyond "smooth breast tissue." You should add:

A visualization of the MG1 residual histogram alongside CT. The extreme compression on mammography reflects that > 99% of residuals are zero or ±1. Showing this distribution would explain the ratio, and make the result reproducible/believable.
A brief discussion of why this matters clinically: mammography is exactly the modality where storage cost is highest (large files, high screening volume, many-year retention requirements). Your compression ratios are most impactful precisely where the volumes are highest.
7. Temporal Prediction — Either Fix It or Cut It
What's there now: MIC2 temporal mode is described as a feature; the only result shows it's worse than independent mode (12.9× vs 13.3× on Breast Tomosynthesis).

The problem: Presenting a feature with a negative result without explanation weakens the paper. Either:

Option A: Add a brief analysis of why temporal fails on DBT. (Answer: tomosynthesis frames differ in projection angle, not temporal motion — there's no inter-frame correlation; each frame sees different tissue structure.) Then reframe temporal mode as appropriate for true video sequences (cardiac cine MRI, fluoroscopy), and note these datasets weren't available.
Option B: Remove the temporal mode from the results section and present it as a design capability without benchmark claims.
Option A is stronger — it shows you understand the failure mode and have a testable prediction.

8. Statistical Rigor
What's missing: Your benchmark tables have single-run measurements. Reviewers at any major venue (MICCAI, MedIA, IEEE TMI) will ask about variance.

What to add:

Run each benchmark 10× and report mean ± std or confidence intervals.
Report whether differences are statistically significant (paired t-test is sufficient for throughput comparisons across same hardware).
This is especially important for the "PICS ratio improvement" claims, where differences of 0.01–0.05× on ratios need significance testing.
Summary: The Paper's Potential Novel Framing
Your strongest claims, reformulated as a clear scientific thesis:

"Classical lossless codecs were designed for 8-bit byte streams. Medical imaging requires 16-bit-native entropy coding. We formalize the information-theoretic cost of byte-splitting for medical image distributions, demonstrate that 16-bit-native RLE+FSE closes this gap, and show that the serial dependency chain in ANS decoding can be broken via multi-state interleaved decode — a technique we formally characterize and show achieves throughput bounded by memory latency rather than state-transition latency."

This frames MIC not as "a fast codec" but as the answer to two specific open problems in the literature: the 16-bit entropy coding gap, and the ANS latency bottleneck. Both are independently publishable insights — together they justify a strong venue submission.