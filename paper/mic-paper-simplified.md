# A 16-Bit-Native Compression Pipeline for Lossless Medical Images

**Kuldeep Singh**

*A plain-language version of the MIC paper. It keeps the main ideas, the
key numbers, and the reasoning, but trades the formal equations and
implementation minutiae for explanations a software engineer can follow.*

---

## Abstract

Medical images have to be stored and transmitted without losing a single
pixel: a radiologist reading a scan must see exactly what the scanner
produced. These images are also "deep" — each pixel is 10 to 16 bits,
not the 8 bits of an ordinary photo. That deep pixel format is where most
compression tools stumble, because almost all of them were built for
8-bit data.

This paper describes **MIC** (Medical Image Codec), a lossless codec that
works *directly* on 16-bit data instead of chopping each pixel into two
bytes. MIC chains together three simple stages: a spatial predictor, a
16-bit run-length encoder, and a large-alphabet entropy coder called FSE
(Finite State Entropy, a fast table-driven form of arithmetic-style
coding). It also includes a "multi-state" decoder trick that keeps the CPU
busier and speeds up decompression.

On 21 real DICOM images across nine modalities, MIC:

- compresses **5–22% better** (14% on average) than a strong general-purpose
  baseline (Delta + Zstandard);
- reaches an average compression ratio of **3.46**, basically tied with
  High-Throughput JPEG 2000 (HTJ2K, 3.45) and about 91% of JPEG-LS (3.82);
- is the **fastest decoder** of the four codecs tested on 20 of 21 images
  on ARM64 and on all 21 on AMD64;
- ships with a tiny **20 KB pure-JavaScript decoder** so the format can be
  decoded right in a web browser.

The main caveat: this is a 21-image study. The results are encouraging but
need confirmation on larger, multi-hospital datasets.

---

## 1. Why Medical Images Need a Different Codec

A modern scanner produces a lot of data. One digital breast tomosynthesis
view alone can be 60+ image slices and over 600 MB; a full screening study
tops 2 GB uncompressed. Hospitals store and move enormous volumes of this
data every day, so good lossless compression directly affects storage cost,
network load, and how fast images appear on a viewer.

DICOM, the medical imaging standard, already supports several lossless
formats — JPEG 2000, HTJ2K, JPEG-LS, and a basic run-length scheme. Each
makes a different trade-off between how small the file gets, how fast it
decodes, and how complex it is to implement. JPEG 2000 and HTJ2K compress
well but rely on fairly heavy machinery. JPEG-LS usually gets the smallest
files. Plain run-length coding is simple but barely compresses.

### The core problem: everything assumes 8-bit data

Here is the observation the whole paper is built on. The popular entropy
coders — Huffman, arithmetic coding, the LZ77 family, and the FSE/ANS coder
inside Zstandard — were all designed for **8-bit byte streams**, where there
are only 256 possible symbols. Medical images break that assumption: a
16-bit pixel has up to 65,536 possible values.

When an 8-bit coder meets 16-bit data, it has two bad options:

1. **Split each pixel into a high byte and a low byte** and code them
   separately. This doubles the number of coding steps and, worse, throws
   away the relationship between the two bytes.
2. **Cap the alphabet at 256 values** and store anything rarer as raw data,
   which wastes space.

Why does splitting hurt? Consider a tiny prediction error like −2, −1, 0,
or +1. The low byte carries the real information, but the high byte is
almost always the same (just a sign-extension). A byte-by-byte coder still
spends bits encoding that high byte even though it's nearly fully
determined by the low one. On the test images, the information shared
between the two bytes ranges from 0.3 to 1.1 bits per pixel — that is the
ceiling on how much a byte-splitting coder is wasting, and MIC's measured
5–22% advantage sits comfortably under that ceiling.

> **The residual stream.** Throughout, a *residual* is the difference
> between a pixel and a prediction of it. Example: if the predictor guesses
> 1020 and the true pixel is 1023, the residual is +3. Smooth regions of an
> image produce lots of residuals near zero, and the same small values show
> up again and again — exactly the pattern run-length and entropy coding
> love.

### What MIC is

MIC keeps the residuals at their native 16-bit width and combines:

1. a **simple spatial predictor**,
2. a **16-bit run-length encoder**, and
3. an **entropy coder (FSE) extended to handle large alphabets**.

Plus a multi-state decoder that improves decompression speed by letting the
CPU work on several symbols at once.

### Contributions, briefly

- A **16-bit-native pipeline** (Delta + 16-bit RLE + extended FSE) that beats
  Delta + Zstandard on every one of the 21 images.
- An **entropy coder for large alphabets** — up to 65,535 symbols, versus the
  4,096-symbol limit in Zstandard's FSE. Tables shrink automatically to fit
  the data (as small as ~2 KB for 8-bit content).
- A **multi-state decoder** that runs 2/4/8 independent decode chains to use
  idle CPU capacity, with no cost to compression ratio.
- An **evaluation** against HTJ2K, JPEG-LS, and Delta + Zstandard.
- A **20 KB JavaScript decoder** plus a WebAssembly build, for client-side
  decoding in browsers.

**Scope.** This work covers lossless, single-frame, grayscale images only.
Color and multi-frame extensions, lossy modes, and progressive decoding are
out of scope.

---

## 2. Related Work, in One Page

**The DICOM lossless codecs.** JPEG 2000 uses a wavelet transform plus a
sophisticated block coder (EBCOT) — great ratios, slow decode. HTJ2K swaps
in a faster block coder and decodes much quicker. JPEG-LS uses a small
predictor (Median Edge Detector) and Golomb-Rice coding; it's a widely
deployed standard and a strong ratio baseline. The basic DICOM run-length
scheme barely compresses because it works on raw bytes with no prediction
step. Research codecs like CALIC and FLIF compress well on natural images
but aren't DICOM standards and lack browser decoders.

These are the codecs MIC is measured against. The goal isn't to dethrone
them, but to explore a **simpler, residual-domain design** aimed squarely at
high-bit-depth images, with strong speed and easy deployment.

**Entropy coding background.** Asymmetric Numeral Systems (ANS) is a family
of entropy coders that replaces arithmetic coding's interval math with
simple integer state updates. FSE is a fast, table-driven version of ANS
made popular by Zstandard. The catch: these implementations are tuned for
byte streams (Zstandard caps FSE at 4,096 symbols).

**Parallel ANS.** Prior work has shown ANS decoders go faster when you run
several independent streams at once (Giesen on interleaved coders; later
work scaling this to many streams and to GPUs). MIC doesn't claim to invent
ANS or interleaving — it applies these known ideas inside a 16-bit-native
medical codec with a specific large-alphabet design.

---

## 3. How MIC Works

The pipeline is three stages, each simple enough to decode in one
straight-line pass with very few branches:

```
Raw 16-bit pixels
   → Spatial prediction  (subtract a guess based on neighbors)
   → 16-bit run-length encoding  (collapse repeated values)
   → FSE entropy coding  (squeeze out the remaining redundancy)
   → Compressed bytes
```

### 3.1 Spatial prediction

For each pixel, MIC predicts its value as the **average of the pixel above
and the pixel to the left**, then stores only the difference (the residual).
Pixels on the top edge or left edge use whatever single neighbor is
available; the very first pixel is stored as-is. Division rounds down, and
the decoder repeats the exact same arithmetic so reconstruction is exact.

This predictor was chosen because it's cheap, has almost no branches on the
decode side, and works well on medical images.

> **Other predictors we tried.** We compared four predictors (left-only,
> average, Paeth from PNG, and MED from JPEG-LS) through the full pipeline.
> MED gave the best ratio (about 1.6% better than average) but decoded
> 1.5–2× slower because of its three-way branch and a diagonal dependency
> that blocks the fast branch-free loop. The average predictor won on
> consistency and speed, so it's the default.

### 3.2 Handling big jumps: overflow coding

Most residuals are small, but occasionally a pixel jumps far from its
prediction. Rather than widen the whole stream to 32 bits to handle rare big
values, MIC reserves one special code as a **delimiter**:

- Small, in-range residuals are stored directly as 16-bit codes (with zero
  mapped to a fixed midpoint value).
- A large, out-of-range residual is stored as the delimiter followed by the
  raw pixel value.

On decode, the reader checks each value: if it's the delimiter, the next
value is a raw pixel; otherwise it's a normal residual. The ranges are
designed not to overlap, so there's never any ambiguity. In normal 8–16-bit
clinical data the overflow path is rare; it exists to guarantee correctness,
not for the common case.

### 3.3 16-bit run-length encoding (RLE)

The residual stream is turned into two kinds of runs:

- **Same runs:** a count plus one repeated value (used when a value appears
  at least three times in a row).
- **Literal runs:** a count followed by that many values copied verbatim
  (the values need not be distinct).

A single header number tells the two apart using a midpoint trick: small
header values mean "same run of this length," large ones mean "literal run."
The longest single run is ~32,767 symbols; longer stretches are split across
several headers automatically.

**Why 16-bit RLE matters.** Suppose 1,000 identical 16-bit residuals appear
in a row. MIC encodes that as just two numbers: a count and the value. A
*byte*-oriented RLE sees the high and low bytes interleaved, so no single
byte repeats 1,000 times — it's forced into two interleaved runs or falls
back to literals. Working at 16 bits captures the repetition that
byte-splitting hides.

**Fast decode.** Same runs (often 80% of all runs on smooth images) are
fast-pathed: the decoder just hands back the cached value and decrements a
counter, without touching the compressed data at all.

### 3.4 The entropy coder (FSE) for large alphabets

**FSE in one paragraph.** FSE keeps a single integer "state." To decode one
symbol, it looks up the current state in a table; the entry tells it which
symbol to output, how many bits to read next, and how to compute the next
state. So each symbol costs **one table lookup, one bit read, and one
addition** — no multiplies or divides. (Encoding runs the symbols in reverse
and builds the bitstream backwards; decoding reads forwards. That reversal
is just how ANS works.)

MIC extends FSE to handle up to **65,535 symbols** instead of Zstandard's
4,096, because high-bit-depth residuals genuinely need a big alphabet. It
also **sizes its tables to the data**: an 8-bit image stored in 16-bit
containers (common for MR) shrinks the working tables from ~512 KB down to
about 2 KB, which keeps everything in fast cache.

**Picking the table size automatically.** FSE has a "tableLog" knob that sets
how big and precise the probability table is. MIC picks it based on how many
distinct values it sees and how much data backs each value: it uses a larger
table only when there are many active symbols *and* enough data to estimate
their probabilities reliably (for example, mammography images after
prediction). On the test set this adaptive choice correctly turned on the
larger table for exactly the nine images that benefit (1–10% better ratio),
and decode speed is unaffected by the choice.

---

## 4. Going Faster: Multi-State Decoding

### The bottleneck

A normal single-state ANS decoder has a problem: each symbol depends on the
one before it. To decode symbol N you need the state produced by symbol
N−1, which means the work can't start early. With a table lookup taking
roughly 4–5 CPU cycles, the loop is stuck at about one symbol every few
cycles — even though modern CPUs have 4–6 execution ports sitting mostly
idle. A single decode chain uses just one of them, leaving ~75–83% of the
CPU's capacity unused.

### The fix

MIC runs **several independent decoders at once** on interleaved positions.
With 8 chains, chain 0 handles positions 0, 8, 16, …; chain 1 handles 1, 9,
17, …; and so on. The chains share nothing during decoding — each has its
own state and its own stream of lookups — so the CPU's out-of-order engine
can keep all of them in flight simultaneously.

On the encoder side, the symbols are split into N interleaved groups, each
compressed independently, and the resulting streams are concatenated. They
all share one decode table, and each chain's starting state is recorded in
the header. The decoder just writes each chain's output back to its
positions.

### How much it helps

In theory, N chains could be up to N× faster. In practice the speedup is
smaller because the chains share some bookkeeping (refilling the bit reader),
the unrolled loop puts pressure on the instruction cache, and reading the
compressed data has its own bandwidth limit. Measured on the FSE stage
alone:

- **4 states:** +46% on ARM64, +8% on AMD64 (vs. single state)
- **8 states:** a further +7% on ARM64, +2% on AMD64

with **no change to the compressed file**. ARM64 keeps improving as chains
double; AMD64 flattens out earlier because its core already extracts a lot of
parallelism from a single chain on its own.

The decoder signals its mode with a short magic header (a reserved byte
value that can't be a valid single-state setting), so single-, two-, four-,
and eight-state files all coexist without confusion. On AMD64 and ARM64 the
inner loop uses each architecture's native variable-shift instructions; other
platforms get a pure-Go fallback.

---

## 5. How We Measured It

**Dataset.** 21 de-identified DICOM images across nine modalities (CT, MR,
CR, X-ray, mammography, tomosynthesis, and others), from public NEMA sample
sets and a public breast-tomosynthesis case. It's a varied set but small
relative to a real clinical archive — hence "on this 21-image dataset"
rather than sweeping claims.

**Baselines.** HTJ2K (via OpenJPH), JPEG-LS (via CharLS), and Delta +
Zstandard at its maximum level. All were called in-process (no file or
subprocess overhead) for a fair speed comparison.

**Metrics.** Compression ratio (uncompressed ÷ compressed), and decode and
encode throughput in MB/s of uncompressed pixels.

**Platforms.** Two reference machines: an ARM64 (Apple M4 Pro) and an AMD64
(AWS Intel Xeon 6). Go code used the standard compiler; C components were
built with `-O3`. Each benchmark ran the full image 10 times; run-to-run
variance stayed under 2% on ARM64 and 5% on AMD64.

The "MIC" column in the result tables is the fastest available C decoder on
each platform — the eight-state decoder, with AVX-512 acceleration of the
run-length and prediction-reversal stages on AMD64. Both read identical
compressed bytes; only the vector width differs.

**Browser decoder.** A 20 KB pure-JavaScript ES module (zero npm
dependencies) plus a 2.5 MB Go WebAssembly build. The JS decoder
auto-detects 1/2/4/8-state streams and was benchmarked under Node.js on the
ARM64 machine.

---

## 6. Results

### 6.1 Compression ratio

JPEG-LS gets the smallest files on every image — its context-adaptive
predictor is hard to beat on pure ratio. But MIC isn't optimizing ratio
alone; it's after the best balance of ratio, speed, and simplicity. Across
the dataset MIC averages a **3.46** ratio: about 91% of JPEG-LS (3.82) and
essentially tied with HTJ2K (3.45), while decoding faster than both.
Mammography compresses best of all (up to 8.79×) because its smooth tissue
produces long near-zero runs.

### 6.2 Encoding speed

MIC encodes faster than the others on most images, on both platforms,
because its encoder is a single pass (predict, run-length, table-driven
entropy code) while HTJ2K and JPEG-LS do more work per pixel.

- **ARM64:** MIC wins on all 21 images — roughly 1.9–3.2× HTJ2K, 3–7×
  JPEG-LS, and 35–240× Delta + Zstandard (whose max-level LZ77 search is
  what makes it so slow).
- **AMD64:** MIC wins on 18 of 21; HTJ2K narrowly takes the two biggest
  mammography slabs and one radiography image. The gap is smaller here
  because the AMD64 baselines are themselves heavily vectorized.

### 6.3 Decoding speed

This is MIC's strongest result. Its decoder is dominated by that tight FSE
loop — one lookup, one bit read, one add — with no hard-to-predict branches.
HTJ2K and JPEG-LS both have data-dependent branches that cause CPU
mispredictions. Zstandard is the closest relative (same predictor, also uses
FSE) but works on bytes, so it pays the 2× byte-splitting cost on deep data.

- **ARM64:** MIC is fastest on **20 of 21** images (Zstandard edges it out
  only on CT, by 1.3%). Roughly 1.5–2.6× HTJ2K, 1.1–1.7× Zstandard, and
  2.0–4.5× JPEG-LS.
- **AMD64:** MIC is fastest on **all 21** images, from a hair ahead on
  mammography up to 1.62× on X-ray. Switching from four to eight states (plus
  the AVX-512 widening of the post-entropy stages) was what flipped the few
  remaining close calls into MIC wins.

So MIC delivers both **better ratio than Zstandard on every image** and
**faster decode on nearly every image** — the signature of the 16-bit-native
design. JPEG-LS keeps about a 10% ratio edge, so the real choice between them
is decode speed versus storage cost.

### 6.4 Other MIC variants (for reference)

The main tables use one MIC column, but the codebase has several variants
that exist for different deployment needs rather than to compete for the
fastest number:

- **Pure Go:** the whole thing in Go with no C dependency, at roughly
  40–60% of the C decoder's speed. Useful where you can't link C (mobile,
  WebAssembly, locked-down runtimes).
- **1/2/4-state decoders:** the same C decoder with fewer chains; they exist
  as baselines for the multi-state comparison.
- **Wavelet-based MIC:** swaps the predictor for a reversible 5/3 wavelet
  transform feeding the same back end. Competitive on a couple of images but
  generally slower on this corpus, since smooth medical images don't reward
  the wavelet's extra cost. Kept as an option, not in the main tables.

The eight-state C decoder ends up being both the simplest and the fastest
configuration on this dataset.

### 6.5 Multi-threaded decoding

MIC can also split an image into horizontal strips and decode them on
separate threads. This scales nearly linearly up to four threads on both
platforms and keeps scaling to eight threads on images above ~5 MB, hitting
**4–4.6 GB/s** on the largest mammography images. Small images don't benefit
— below about half a megabyte, thread setup costs more than it saves. We
report this separately because the other codecs run single-threaded in our
pipeline, so a head-to-head wouldn't be fair.

Practical rule of thumb: decode many small images? Use single-threaded MIC
per request (already fast). Decode one big image on a multi-core box? Use
strip-parallel with four to eight threads.

### 6.6 In the browser

The JavaScript decoder runs well. Interestingly, the **four-state** variant
is 7–13% *faster* than eight-state in JS, even on the same hardware where
eight states win in C. The reason is the JavaScript engine (V8): it can keep
four independent chains busy but not eight, so the extra chains just add
overhead without shortening the critical path. The eight-state path still
works in JS — it lets one decoder accept C-encoded files without conversion —
but four states is the sweet spot for the browser.

Concretely, a 9.35 MB mammography image decodes in 19.8 ms (473 MB/s) using
eight workers with four-state strips. That means a web DICOM viewer can pull
a `.mic` file straight from object storage and decode it client-side, with no
server doing compression or format conversion. (These numbers are from
Node.js; full in-browser benchmarking is future work.)

---

## 7. Discussion

### Reading the results together

Three things tie the numbers together:

1. **MIC's ratio edge over Zstandard** (5–22%, 14% average) comes mainly from
   working on 16-bit residuals directly instead of splitting them into bytes
   — and that same choice removes the per-byte branches that slow decoding.
2. **On the very largest mammography images on AMD64**, HTJ2K's block coder
   regains the speed lead by amortizing its setup over many pixels; MIC stays
   within 0–20% there.
3. **Multi-state decoding** is what makes MIC's decoder competitive with
   HTJ2K's vectorized block coder, and it costs nothing in file size.

### Which codec to pick

- **Low-latency viewing (PACS, thumbnails, tomosynthesis playback):**
  single-threaded MIC is a sensible default (~600–900 MB/s per request);
  strip-parallel MIC for one big image at a time.
- **Smallest archival files:** JPEG-LS, which keeps ~10% better ratio — at
  the cost of decoding 1.6–5.7× slower on ARM64.
- **DICOM transfer-syntax compatibility required:** HTJ2K, since it's a
  standard (and it's specifically strong on big mammography on AMD64).
- **No native code allowed (browsers, sandboxes):** the pure-Go reference or
  the 20 KB JavaScript decoder, at about half the C decoder's speed.

A more structured comparison (a Pareto plot of ratio vs. decode speed, plus a
weighted scoring matrix over ratio, speed, platform coverage, and deployment
ease) puts MIC and JPEG-LS on the efficiency frontier — MIC for throughput,
JPEG-LS for ratio — with HTJ2K and Zstandard dominated on those two axes.
HTJ2K still wins whenever standard compatibility is a hard requirement that
the scoring doesn't capture.

### Limitations

- **Small dataset.** 21 images across nine modalities is varied but far from
  a clinical archive; broader validation is needed.
- **Tuning on the test set.** The adaptive table-size thresholds and the
  predictor choice were informed by this dataset. The rules are simple (two
  thresholds, one predictor), which limits overfitting risk, but larger
  studies should confirm they generalize.
- **Baseline coverage.** Stronger byte-oriented baselines (Delta +
  byte/bit-shuffle + Zstd, DICOM RLE) would more sharply isolate the value of
  the 16-bit entropy stage; left to future work.
- **Reporting.** Tables give single representative throughput values rather
  than full confidence intervals.
- **Not yet a DICOM transfer syntax.** MIC works on the extracted pixel
  array; real PACS integration would need a private encapsulation or a
  standardization step.

---

## 8. Conclusion

MIC is a 16-bit-native lossless codec for medical images: spatial prediction,
16-bit run-length coding, a large-alphabet table-based entropy coder, and a
multi-state decoder for speed. On 21 DICOM images it reaches an average
ratio of 3.46 (tied with HTJ2K, ~91% of JPEG-LS) while being the fastest
decoder on 20/21 images on ARM64 and all 21 on AMD64, and the fastest encoder
on all 21 ARM64 images and 18/21 on AMD64. A 20 KB JavaScript decoder and a
WebAssembly build show the format can be decoded client-side in a browser.

**Future work:** larger multi-hospital datasets; stronger shuffle-based
baselines; full statistical reporting; an ARM64 vector kernel for the wavelet
variant; and benchmarking the JavaScript decoder in real browsers.

MIC is open source: https://github.com/pappuks/medical-image-codec
