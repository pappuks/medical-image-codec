# MIC Paper Editorial Rules

Apply these rules whenever editing or creating versions of the MIC IEEE paper
(`paper/mic-paper-*.tex`). They capture peer-review feedback received after v4
and applied in v5.

---

## 1. SE Explainability

### Plain-language sentences
Every major concept introduced for the first time must be followed by a
plain-language sentence that a software engineer unfamiliar with compression
research can understand. Examples:

- When introducing 16-bit-native coding, add: "In contrast to codecs that split
  16-bit pixels into byte pairs, MIC treats each predicted residual as a single
  16-bit symbol, allowing the entropy model to capture correlations between the
  high and low byte directly."
- When introducing residuals, add a numeric example: "For example, if the
  predicted value is 1020 and the true pixel is 1023, the residual is +3."
- When introducing byte-splitting cost, give a concrete example with small
  signed residuals (-2, -1, 0, +1) and explain why the high byte is nearly
  deterministic.

### FSE overview
Section III.D or wherever FSE is first explained must include a "FSE in one
paragraph" that describes the decode loop in plain terms: one table lookup, one
bit read, one addition per symbol — no divisions or multiplications.

### RLE midCount protocol
Section III.C must fully specify:
- `midCount = 32767` (half of uint16 range)
- Counts stored as uint16 values
- `c ≤ midCount` → same run of length `c`; `c > midCount` → diff run of length `c - midCount`
- Maximum run length = 32767 symbols; long runs are split transparently
- Include an encoding example showing both same and diff runs with actual byte values

### Overflow coding (Section III.B)
Must include:
- A small table showing residual → emitted code(s) for a representative bit depth
- A decoding pseudocode paragraph (read c; if c ≠ D recover residual; if c = D read raw pixel)
- Explicit statement that residual zero maps to `deltaThreshold`
- Explicit statement that the threshold is strict `<`, not `≤`

### symbolLen / symbolDensity (Section III.E)
Must be defined precisely:
- `symbolLen` = number of distinct symbols in the observed RLE output (equivalently, max_value - min_value + 1, counting all positions in that range)
- `inputLength` = total number of symbols in the RLE output fed to FSE
- `symbolDensity = inputLength / symbolLen`

### Huffman backend slowness (Section III.F)
When mentioning that Huffman is slower than FSE despite being "simpler," explain
why: the large alphabet requires wider, less-predictable variable-length code
lookups, whereas FSE uses a fixed-width table transition that the CPU branch
predictor handles more efficiently.

### Four-state bitstream format (Section IV)
Must describe: four independent streams encoded separately, concatenated with
boundary offsets stored in a header, shared FSE decode table, initial states
stored per chain, and how the decoder reorders output.

### Magic header explanation (Section IV.B / Signaling)
Must explain what the first byte normally means in single-state mode (it encodes
`tableLog`; max valid `tableLog` is 17), and why 0xFF is safe as an
extended-format marker.

### Compiler flags (Section V.D)
Must clarify:
- Go uses the gc compiler with default optimization
- `-O3` (and `-march=native` on AMD64) applies only to CGO-linked C components
- These are separate compilers with separate flag sets

### Variant table (Section V.D)
Always include `tab:variants`: a table listing each codec variant with columns
for language, FSE decoder type, SIMD, and whether the compressed format differs
from MIC-Go.

### Practitioner takeaway
After the JavaScript results section, add a "Practitioner takeaway" paragraph:
- Pure Go / JS for simplicity
- MIC-4state-C for maximum single-threaded server throughput
- PICS-C-8 for large images on multicore systems

---

## 2. Acronym Rules

Expand every acronym on first use. Required expansions (in order of typical
first appearance):

| Acronym | Full form |
|---------|-----------|
| MIC | (codec name, not an acronym — say "MIC, the proposed codec" or define if named) |
| ANS | asymmetric numeral systems |
| FSE | Finite State Entropy |
| DICOM | Digital Imaging and Communications in Medicine |
| HTJ2K | High-Throughput JPEG 2000 |
| ILP | instruction-level parallelism |
| CT | computed tomography |
| MR | magnetic resonance imaging |
| CR | computed radiography |
| XR | plain X-ray / radiography |
| DBT | digital breast tomosynthesis |
| PACS | Picture Archiving and Communication Systems |
| RLE | run-length encoding |
| JPEG-LS | (describe as "a lossless/near-lossless predictive JPEG standard") |
| EBCOT | Embedded Block Coding with Optimized Truncation |
| MED | Median Edge Detector |
| CALIC | Context-based Adaptive Lossless Image Codec |
| FLIF | Free Lossless Image Format |
| tANS | table-based asymmetric numeral systems |
| rANS | range asymmetric numeral systems |
| LZ77 | Lempel-Ziv 1977 |
| Zstd | Zstandard |
| SIMD | Single Instruction, Multiple Data |
| AVX2 | Advanced Vector Extensions 2 |
| BMI2 | Bit Manipulation Instruction Set 2 |
| CGO | Go's C foreign-function interface |
| WASM | WebAssembly |
| JS | JavaScript |
| npm | Node Package Manager |
| RGB | red-green-blue |
| YCoCg-R | reversible YCoCg color transform (luma/chroma, maps RGB integers to integers without loss) |
| MIC2 | (explain: "2" = version 2 of the container format, multi-frame grayscale) |
| MICR | (explain: "R" = RGB, single-frame RGB format) |
| AMD64 | x86-64/AMD64 |
| ARM64 | ARM64/AArch64 |
| ILP | instruction-level parallelism |
| LOC | source lines (spell out in tables: "Source lines") |

Avoid unexplained table abbreviations: "Gen." → "General-purpose", "Part." →
"Partial", "Config." → "Configurable".

---

## 3. Em Dash (---) Usage

### Acceptable IEEE-style uses (do NOT change):
- `Abstract---` and `Index Terms---` (required by IEEE style)
- Table/figure captions using `---` as a subtitle separator where a colon
  would read awkwardly

### Replace em dashes with cleaner alternatives in body text:

| Original pattern | Replacement |
|-----------------|-------------|
| `slices---e.g., N frames` | `slices (for example, N frames)` |
| `systems---X, Y, and Z---were designed` | `systems, including X, Y, and Z, were designed` |
| `given $X_L$---yet must still be encoded` | `given $X_L$, but it must still be encoded` |
| `pipeline---Raw pixels are` | `pipeline. Raw pixels are` (caption) |
| `client-side---zero server-side compute` | `without server-side compute or transcoding latency` |
| `code---approximately 18×` | `code, about 18×` |
| Table captions: `summary---geometric means` | `summary: geometric means` |

### Avoid promotional-sounding constructions:
- Not: "decode it client-side—zero server-side compute, zero transcoding latency."
- Use: "decode it client-side without server-side compute or transcoding latency."

---

## 4. Avoiding Duplication

### Claims that appear too often — consolidate:

**"JPEG-LS achieves the highest ratio on all 21 images"**
- Keep in: Results (analysis paragraph) and Conclusion
- Remove from: Related Work (replace with neutral positioning)
- Related Work should say: "JPEG-LS is an important baseline as a standardized
  predictive codec widely deployed in medical PACS."
- Do NOT use in table captions

**"5–22%, geomean +14% over Delta+Zstandard"**
- Keep exact numbers in: Results and Conclusion
- Contributions: shorten to "consistently outperforms Delta+Zstandard on all 21 images"
- Abstract: headline only, no repetition of exact numbers

**Multi-state speedup (+68% ARM64, +43% AMD64)**
- Keep exact numbers in: Section IV results tables and Conclusion
- Abstract: include headline geomean only
- Contributions: say "evaluated multi-state decoding" without repeating all numbers

**Browser/client-side decoder**
- Keep detailed description in: Section V.E (JS evaluation) and Discussion C
- Conclusion: one bullet only
- Do not restate in multiple consecutive sections

**Dataset description (21 images, 10 modalities)**
- Keep full description in: Dataset section (V.A)
- Abstract and Conclusion: mention "21 DICOM images spanning 10 modalities" at most once each

---

## 5. Numerical Consistency and Precision

| Issue | Rule |
|-------|------|
| "within 1%" | Always write "within 1% relative to" to avoid confusion with absolute percentage points |
| "18× less" | Always write "18× fewer source lines" — "less" is incorrect for countable items |
| 4,095 vs 4,096 | Use **4,096** consistently for Zstandard's FSE symbol limit |
| 65,535 vs 65,536 | Use **65,535** for MIC's FSE symbol limit; clarify that one code (2^16−1) is reserved as the overflow delimiter |
| "approximately 91%" | Always specify which MIC variant and platform: "On ARM64, MIC-4state-C achieves approximately 91% of JPEG-LS's geometric mean ratio" |
| "-benchtime=10x" | Clarify this means 10 iterations per benchmark, not 10 seconds |

---

## 6. CPU / Platform Descriptions

- ARM64 platform: "Apple M2 Max (12-core ARM64)"
- AMD64 platform: "Intel Core Ultra 9 285K (x86-64/AMD64, mixed P-core/E-core
  topology)" — NOT "24 P-core AMD64" (the 285K has both P-cores and E-cores)
- The XA1 outlier on AMD64 is due to small-image effects and mixed P/E-core
  scheduling — do NOT call it "within measurement noise" unless repeated
  measurements confirm variance that large

---

## 7. Browser Deployment Claims

Always hedge browser-decoder availability claims with "at the time of writing":

- Not: "no production-ready WebAssembly or JavaScript decoder exists for HTJ2K"
- Use: "we did not identify a production-ready npm-distributed WebAssembly or
  JavaScript decoder for HTJ2K at the time of writing"

---

## 8. PICS Definition

Define PICS (Parallel Image Compressed Strips) at first use in the Methodology
section before using the acronym in results tables. Include:
- What PICS does: splits image into N horizontal strips
- Each strip has its own FSE table
- Strips are independently decodable
- Decompression uses a C pthreads worker pool

---

## 9. Table Caption Style

- Use colon (`:`) not em dash (`---`) as subtitle separator in table captions
- Expand all abbreviations in table headers and footers (no "Gen.", "Part.", etc.)
- "LOC" → "Source lines" in table headers
