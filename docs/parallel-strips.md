# Parallel Single-Image Compression — PICS Format

## Overview

PICS (Parallel Image Compressed Strips) is a thin envelope format that divides a
single image into N horizontal strips, each independently compressed with the
standard MIC Delta+RLE+FSE pipeline.  Because the strips share no data, all N
strips can be compressed or decompressed concurrently on separate CPU cores.

This is distinct from the frame-level parallelism in MIC2 (multi-frame) and the
tile-level parallelism in MIC3 (WSI): PICS parallelises the processing of a
**single** ordinary medical image.

---

## Motivation

### The serial bottleneck in Delta+RLE+FSE

The MIC single-frame pipeline runs in three sequential stages:

```
Delta encode → RLE → FSE compress
```

Delta decompression has a pixel-by-pixel dependency:

```
pixel[y,x] = predictor(left=pixel[y,x-1], top=pixel[y-1,x]) + delta[y,x]
```

This means row `y` depends on row `y-1` and cannot start until row `y-1` is
complete — making the entire image inherently single-threaded.

### 4-state FSE is ILP, not multi-core

The 4-state FSE decoder (`fse4state.go`) exploits **instruction-level
parallelism** (ILP): four independent ANS state machines let the CPU's
out-of-order engine issue four table lookups simultaneously within a *single*
core.  It does not use additional CPU cores.

### Strip partitioning breaks the row dependency

If we cut the image at row boundaries into N strips and treat each strip as a
self-contained image (with its own row 0), the strips become fully independent:

```
┌─────────────────────────────────────────┐
│  Strip 0   rows   0 …  H/N-1           │ ← independent Delta+RLE+FSE
├─────────────────────────────────────────┤
│  Strip 1   rows H/N …  2H/N-1         │ ← independent Delta+RLE+FSE
├─────────────────────────────────────────┤
│             ⋮                            │
├─────────────────────────────────────────┤
│  Strip N-1 rows (N-1)H/N …  H-1       │ ← independent Delta+RLE+FSE
└─────────────────────────────────────────┘
```

Strips 1…N-1 cannot use the last row of their predecessor as the top-neighbour
predictor.  Their first row uses `top = 0` instead — exactly the same as the
very first row of the image.  This is the only accuracy loss.

---

## Compression Ratio Impact

### Theory

For a height-H image split into N strips, exactly N-1 strip boundaries exist.
At each boundary, one row of pixels loses vertical prediction accuracy.
Assuming the average delta error per boundary row is `ε` times worse than
normal rows:

```
ratio_degradation ≈ (N-1) / H × ε
```

For medical images, vertical correlation is similar in magnitude to horizontal
correlation, so a first-row pixel (left-only predictor) typically needs ~10–20%
more bits than an interior pixel (both neighbours).  With `ε ≈ 0.15`:

| Strips (N) | Boundary rows | H=256 | H=512 | H=2048 |
|:----------:|:-------------:|:-----:|:-----:|:------:|
| 2 | 1 | ~0.6% | ~0.3% | ~0.07% |
| 4 | 3 | ~1.8% | ~0.9% | ~0.22% |
| 8 | 7 | ~4.1% | ~2.1% | ~0.51% |
| 16 | 15 | ~8.8% | ~4.4% | ~1.1% |

Larger images (CR, MG) incur smaller relative overhead because boundary rows
are a smaller fraction of total rows.

### Measured results across all modalities (Delta+RLE+FSE two-state)

Compression ratios measured at 1/2/4/8 strips (Intel Xeon @ 2.10 GHz):

| Image | Size | 1-strip | 2-strip | 4-strip | 8-strip | Δ(1→4) |
|-------|------|:-------:|:-------:|:-------:|:-------:|:------:|
| MR (256×256) | 0.12 MB | **2.35×** | 2.33× | 2.28× | 2.21× | −3.0% |
| CT (512×512) | 0.50 MB | **2.24×** | 2.21× | 2.15× | 1.96× | −4.0% |
| CR (1760×2140) | 7.18 MB | 3.63× | 3.64× | **3.66×** | 3.68× | +0.8% |
| XR (2048×2577) | 10.07 MB | 1.74× | 1.75× | **1.75×** | 1.76× | +0.6% |
| MG1 (2457×1996) | 9.35 MB | 8.57× | 8.60× | **8.69×** | 8.77× | +1.4% |
| MG2 (2457×1996) | 9.35 MB | 8.55× | 8.59× | **8.68×** | 8.76× | +1.5% |
| MG3 (4774×3064) | 27.90 MB | 2.29× | 2.33× | **2.36×** | 2.39× | +3.1% |
| MG4 (4096×3328) | 26.00 MB | 3.47× | 3.52× | **3.59×** | 3.62× | +3.5% |

> **CR, XR, and MG images improve in ratio at higher strip counts** because
> strip-local FSE tables adapt to sub-image content distributions better than a
> single global table.  Small images (MR, CT) show the expected small ratio
> loss at strip boundaries.

The MR image is 256 rows tall — a relatively severe case for boundary overhead.
For larger images (CR: 2140 rows, MG: 3064–4096 rows) the per-strip overhead
is proportionally smaller and often outweighed by the FSE localisation gain.

**Recommendation**: 4 strips delivers the best practical trade-off — ~2–4×
faster decompression with negligible or positive ratio impact on large images,
and <3% ratio loss on small images (MR, CT) on a 4-core machine.

---

## Throughput Scaling

All benchmarks run on **Intel Xeon @ 2.10 GHz, 4 physical cores**, `GOMAXPROCS=4`,
10 iterations, using the Go testing framework (`b.SetBytes`).

### CR image (1760×2140) — compression throughput

| Strips | MB/s | Speedup | Wall time (ms) |
|:------:|:----:|:-------:|:--------------:|
| 1 | 133 | 1.00× | 56.7 |
| 2 | 219 | **1.65×** | 34.4 |
| 4 | 401 | **3.02×** | 18.8 |
| 8 | 479 | **3.61×** | 15.7 |

### CR image (1760×2140) — decompression throughput

| Strips | MB/s | Speedup | Wall time (ms) |
|:------:|:----:|:-------:|:--------------:|
| 1 | 186 | 1.00× | 40.6 |
| 2 | 346 | **1.86×** | 21.8 |
| 4 | 583 | **3.14×** | 12.9 |
| 8 | 540 | 2.91× | 14.0 |

### Scaling efficiency (CR image)

| Strips | Compress efficiency | Decompress efficiency |
|:------:|:-------------------:|:---------------------:|
| 2 | 82% | 93% |
| 4 | 75% | 78% |
| 8 | 45% | 36% |

Efficiency drops above the core count due to goroutine scheduling overhead,
memory bandwidth contention, and unequal strip compression times.

---

## All-Image Comparison: PICS vs MIC-Go vs MIC-4state

Decompression throughput in **MB/s** across all modalities
(`BenchmarkPICSVsAllCodecs`, Intel Xeon @ 2.10 GHz, 4 cores, 10 iterations):

| Image | Orig (MB) | MIC-Go | MIC-4state | PICS-1 | PICS-2 | PICS-4 | PICS-8 |
|-------|:---------:|:------:|:----------:|:------:|:------:|:------:|:------:|
| MR (256×256) | 0.12 | 122 | 141 | 104 | 111 | 138 | 68 |
| CT (512×512) | 0.50 | 138 | 154 | 126 | 191 | **217** | 163 |
| CR (1760×2140) | 7.18 | 165 | 226 | 216 | 374 | **599** | 564 |
| XR (2048×2577) | 10.07 | 221 | 237 | 213 | 342 | **738** | 716 |
| MG1 (2457×1996) | 9.35 | 381 | 365 | 370 | 579 | **849** | 816 |
| MG2 (2457×1996) | 9.35 | 386 | 384 | 359 | 555 | 797 | **951** |
| MG3 (4774×3064) | 27.90 | 214 | 192 | 184 | 374 | 679 | **682** |
| MG4 (4096×3328) | 26.00 | 327 | 324 | 293 | 565 | **808** | 788 |

> MR and CT are small images where goroutine startup cost approaches strip
> processing time.  All images ≥ 7 MB show substantial speedup from PICS.

### PICS-4 and PICS-8 speedup over single-threaded codecs

| Image | PICS-4 / MIC-Go | PICS-4 / MIC-4state | PICS-8 / MIC-Go | PICS-8 / MIC-4state |
|-------|:---------------:|:-------------------:|:---------------:|:-------------------:|
| MR (256×256) | 1.13× | 0.98× | 0.56× | 0.48× |
| CT (512×512) | 1.57× | 1.41× | 1.18× | 1.06× |
| CR (1760×2140) | **3.63×** | **2.65×** | 3.42× | 2.50× |
| XR (2048×2577) | **3.34×** | **3.11×** | 3.24× | 3.02× |
| MG1 (2457×1996) | **2.23×** | **2.33×** | 2.14× | 2.23× |
| MG2 (2457×1996) | **2.06×** | **2.08×** | **2.46×** | **2.48×** |
| MG3 (4774×3064) | **3.17×** | **3.54×** | 3.19× | 3.55× |
| MG4 (4096×3328) | **2.47×** | **2.49×** | 2.41× | 2.43× |

**Key findings:**
- **Large images (≥ 7 MB)**: PICS-4 delivers 2.1–3.6× speedup over MIC-Go.
  PICS-8 is competitive with PICS-4 and sometimes faster (MG2, MG3).
- **Small images (MR, CT)**: goroutine overhead dominates for MR (256×256);
  PICS should not be used below ~1 MB.  CT (0.5 MB) is a borderline case with
  only modest gain.
- **vs MIC-4state**: PICS-4 beats 4-state by 2–3.5× on large images; on MR
  where goroutine overhead matters, MIC-4state is the better choice.

### Compression ratio across all images and strip counts

| Image | MIC-Go | MIC-4state | PICS-1 | PICS-2 | PICS-4 | PICS-8 |
|-------|:------:|:----------:|:------:|:------:|:------:|:------:|
| MR | 2.35× | 2.35× | 2.35× | 2.33× | 2.28× | 2.21× |
| CT | 2.24× | 2.24× | 2.24× | 2.21× | 2.15× | 1.96× |
| CR | 3.63× | 3.63× | 3.63× | 3.64× | 3.66× | 3.68× |
| XR | 1.74× | 1.74× | 1.74× | 1.75× | 1.75× | 1.76× |
| MG1 | 8.57× | 8.57× | 8.57× | 8.60× | 8.69× | 8.77× |
| MG2 | 8.55× | 8.55× | 8.55× | 8.59× | 8.68× | 8.76× |
| MG3 | 2.29× | 2.29× | 2.29× | 2.33× | 2.36× | 2.39× |
| MG4 | 3.47× | 3.47× | 3.47× | 3.52× | 3.59× | 3.62× |

PICS-1 equals MIC-Go exactly (single strip, no boundary overhead).  For
CR/XR/MG modalities, ratio actually improves at higher strip counts due to
better FSE table localisation within each strip.  Only MR and CT show the
expected boundary overhead, with CT being more sensitive due to fewer rows.

---

## PICS Binary Format

```
Byte offset   Field
────────────  ─────────────────────────────────────────
0  – 3        Magic: "PICS"
4  – 7        Width           (uint32 LE)
8  – 11       Total height    (uint32 LE)
12 – 15       NumStrips       (uint32 LE)
16 – 19       StripHeight     (uint32 LE) — nominal rows per strip;
                                            last strip may be shorter
────────────  ─────────────────────────────────────────
20 – …        Offset table    (NumStrips × 8 bytes each)
              └─ per strip: data_offset (uint32 LE) + data_length (uint32 LE)
                 offset is relative to the start of the data block
                 (i.e., add headerSize = 20 + NumStrips × 8 to get file offset)
────────────  ─────────────────────────────────────────
…             Concatenated compressed strip blobs
              Each blob is a valid CompressSingleFrame output
              (auto-detected as 1-state, 2-state, or 4-state FSE)
```

### Key properties

- **Self-contained strips**: each blob can be decompressed independently using
  `DecompressSingleFrame`; no cross-strip data dependencies.
- **Random strip access**: decompress only strip K by reading its offset from
  the table — no need to decode preceding strips.
- **Format backward compatibility**: the magic `"PICS"` is unambiguous with
  `"MIC2"` and `"MIC3"`; existing decoders ignore PICS blobs cleanly.
- **Composability**: strips use the same FSE format as MIC2/MIC3 — existing
  FSE decoder code is reused without modification.

---

## Go Implementation

### API

```go
// Compress pixels into a PICS blob using numStrips concurrent goroutines.
// numStrips <= 0 → auto (GOMAXPROCS).
// maxValue is the maximum uint16 pixel value in the image (for bit-depth selection).
func CompressParallelStrips(pixels []uint16, width, height int,
                            maxValue uint16, numStrips int) ([]byte, error)

// Decompress a PICS blob back to pixels.
// Returns pixels (row-major uint16), width, height.
// All strips decompress concurrently.
func DecompressParallelStrips(compressed []byte) ([]uint16, int, int, error)
```

### Compress internals (`parallelstrips.go`)

```
numStrips = clamp(numStrips, 1, height)
stripH    = ceil(height / numStrips)

for s in 0 .. actualStrips-1:
    goroutine s:
        y0 = s × stripH
        y1 = min(y0 + stripH, height)
        CompressSingleFrame(pixels[y0×width : y1×width], width, y1-y0, maxValue)
        → results[s]

wg.Wait()
assemble PICS header + offset table + concat(results[0..N-1])
```

Each `CompressSingleFrame` call tries 2-state FSE first, falls back to
1-state — identical to single-frame encoding.

### Decompress internals

```
parse PICS header → width, height, numStrips, stripH

out = make([]uint16, width × height)

for s in 0 .. numStrips-1:
    goroutine s:
        strip_data = compressed[header_end + offset[s] : ... + length[s]]
        y0 = s × stripH
        y1 = min(y0 + stripH, height)
        pixels = DecompressSingleFrame(strip_data, width, y1-y0)
        copy(out[y0×width:], pixels)   // disjoint range — no lock needed

wg.Wait()
return out
```

Output rows are disjoint between goroutines, so concurrent writes to `out` are
safe without any synchronisation primitives.

### Architecture portability

Goroutines are scheduled by the Go runtime and run on any platform (amd64,
arm64, WASM, RISC-V, …).  The SIMD acceleration inside each strip (AVX2 on
amd64 via `fse4StateDecompNative`, NEON on arm64) activates automatically —
no architecture-specific code is needed in `parallelstrips.go`.

---

## C Implementation

### Files

| File | Description |
|------|-------------|
| `ojph/mic_parallel.h` | Public header: `mic_decompress_parallel`, `mic_decompress_parallel_scalar` |
| `ojph/mic_parallel.c` | POSIX pthread worker pool + PICS header parser |

### API

```c
// Decompress a PICS blob using max_threads concurrent pthreads.
// max_threads = 0 → one thread per strip.
// Returns 0 on success, non-zero error code on failure.
int mic_decompress_parallel(const uint8_t *compressed, size_t compressed_len,
                            uint16_t *pixels_out, int width, int height,
                            int max_threads);

// Same but forces scalar four-state inner decoder (no SIMD).
int mic_decompress_parallel_scalar(const uint8_t *compressed, size_t compressed_len,
                                   uint16_t *pixels_out, int width, int height,
                                   int max_threads);
```

### Inner decoder selection

```c
static decomp_fn_t best_decomp_fn(void) {
#if defined(__x86_64__) || defined(_M_X64)
    return mic_decompress_four_state_simd;  // SSE2 RLE/delta + AVX2 FSE bulk
#else
    return mic_decompress_four_state;        // scalar; NEON wiring planned
#endif
}
```

On **AMD64** the inner decoder is `mic_decompress_four_state_simd`, which
uses SSE2 for the RLE/delta fill operations and (where AVX2 is available) the
4-state FSE bulk decoder.  On **ARM64** and other architectures the scalar
`mic_decompress_four_state` is used — still faster than single-state FSE due
to the 4× ILP from independent ANS states.

### Thread pool design

The implementation uses a **bounded round-robin thread pool** without a mutex
or condition variable:

```
slot = s % max_threads

if launched >= max_threads:
    pthread_join(threads[slot])     // reclaim oldest slot
    check jobs[s - max_threads].error

pthread_create(&threads[slot], strip_worker, &jobs[s])
launched++
```

After dispatching all strips, remaining live threads are joined in slot order.

**Properties**:
- O(max_threads) memory for thread handles
- No semaphore, mutex, or condition variable
- At most `max_threads` pthreads alive at any time
- Works on Linux, macOS, and any POSIX platform

### Build

```bash
# AMD64 with AVX2 inner decoder
gcc -O3 -msse2 -mavx2 -pthread \
    mic_decompress_c.c mic_parallel.c -shared -fPIC -o libmic_parallel.so

# ARM64 (NEON always present; inner decoder is currently scalar)
gcc -O3 -pthread \
    mic_decompress_c.c mic_parallel.c -shared -fPIC -o libmic_parallel.so
```

### CGO bindings (`ojph/mic_c.go`, build tag `cgo_ojph`)

```go
// Best-available SIMD inner decoder
pixels, err := ojph.MICDecompressParallelC(pics, width, height, maxThreads)

// Scalar inner decoder (for isolating thread speedup from SIMD speedup)
pixels, err := ojph.MICDecompressParallelScalarC(pics, width, height, maxThreads)
```

---

## AMD64 vs ARM64 — Architecture Notes

| Aspect | AMD64 | ARM64 |
|--------|-------|-------|
| Thread API | POSIX pthreads | POSIX pthreads (identical) |
| Go goroutines | Identical | Identical |
| Inner FSE decoder | `mic_decompress_four_state_simd` (SSE2 + AVX2) | `mic_decompress_four_state` (scalar) |
| NEON acceleration | N/A | Planned (4-state NEON FSE kernel) |
| Strip-level parallelism | Architecture-agnostic | Architecture-agnostic |
| PICS format | Identical — interoperable | Identical — interoperable |

PICS blobs compressed on AMD64 decompress correctly on ARM64 and vice versa
— the format is byte-identical across architectures.

The thread-level and instruction-level speedups are **additive**: with 4-state
FSE (ILP within one core) + 4 strips (multi-core parallelism), the combined
throughput approaches 4 × (ILP gain) on a 4-core machine.

---

## When to Use PICS

### Use PICS when

- The image is **≥ 0.5 MB** (e.g. CT 512×512 and larger): images this size
  provide enough work per strip to amortise goroutine startup.
- You need to decode a single large medical image as fast as possible on a
  multi-core server or workstation.
- Your application has spare CPU cores during a decompression request (e.g.,
  a DICOM viewer that serves one image at a time).
- A small ratio overhead (<3% for MR/CT) or even a slight ratio improvement
  (CR/XR/MG) is acceptable in exchange for 2–4× decompression speedup.

### Use MIC2 (multi-frame) instead when

- You have multiple frames (Tomosynthesis, Cine MRI, Fluoroscopy).
  Each frame is already an independent MIC stream — frame-level parallelism
  is available for free with no ratio overhead.

### Use MIC3 (WSI) instead when

- You have a large RGB whole-slide image.  The tiled format provides O(1)
  random tile access, pyramid levels, and parallel tile encode/decode.

### Use MIC-4state instead when

- The image is **< 0.5 MB** (e.g. MR 256×256): benchmarks show PICS-4 gives
  only 1.1× speedup (and PICS-8 regresses 0.56×) on this size; MIC-4state
  at 141 MB/s outperforms PICS-4 at 138 MB/s with no format overhead.

### Use 1 strip (standard `CompressSingleFrame`) when

- You are on a single-core environment (embedded, WASM).
- You need the maximum compression ratio (no boundary overhead on MR/CT).
- You are archiving images for long-term storage where ratio is critical.

---

## Tests and Benchmarks

### Running the tests

```bash
# Pixel-exact round-trip: all modalities, strips 1/2/4/GOMAXPROCS
go test -run TestParallelStripsRoundtrip -v mic

# Compression ratio table (strips 2/4/8/16, logged output)
go test -run TestParallelStripsCompressionRatio -v mic

# Format validation and error handling
go test -run TestParallelStripsFormatValidation -v mic

# numStrips > height clamping edge case
go test -run TestParallelStripsSingleRowImage -v mic
```

### Running the benchmarks

```bash
# Compression and decompression throughput at 1/2/4/8 strips (CR image)
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkParallelStrips mic

# Full multi-image comparison: MIC-Go, MIC-4state, PICS-1/2/4/8 (all 8 test images)
go test -benchmem -run=^$ -benchtime=10x -bench ^BenchmarkPICSVsAllCodecs mic

# Human-readable comparison table (throughput, speedup, ratio — all images)
go test -v -run TestPICSComparisonTable -timeout 120s mic
```

### Benchmark output — CR image throughput (Intel Xeon @ 2.10 GHz, 4 cores)

```
goos: linux
goarch: amd64
cpu: Intel(R) Xeon(R) Processor @ 2.10GHz

BenchmarkParallelStripsCompress/strips1-4    5   56729052 ns/op   132.79 MB/s
BenchmarkParallelStripsCompress/strips2-4    5   34415010 ns/op   218.88 MB/s
BenchmarkParallelStripsCompress/strips4-4    5   18806517 ns/op   400.54 MB/s
BenchmarkParallelStripsCompress/strips8-4    5   15734718 ns/op   478.74 MB/s

BenchmarkParallelStripsDecompress/strips1-4  5   40592969 ns/op   185.57 MB/s
BenchmarkParallelStripsDecompress/strips2-4  5   21778416 ns/op   345.88 MB/s
BenchmarkParallelStripsDecompress/strips4-4  5   12918462 ns/op   583.10 MB/s
BenchmarkParallelStripsDecompress/strips8-4  5   13951239 ns/op   539.94 MB/s
```

### Benchmark output — all images (BenchmarkPICSVsAllCodecs, 10 iterations)

```
BenchmarkPICSVsAllCodecs/MIC-Go/MR-4           10    1070774 ns/op   122.41 MB/s   2.348 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/MR-4       10     927448 ns/op   141.33 MB/s   2.347 ratio
BenchmarkPICSVsAllCodecs/PICS-1/MR-4           10    1263047 ns/op   103.77 MB/s   2.347 ratio
BenchmarkPICSVsAllCodecs/PICS-2/MR-4           10    1179887 ns/op   111.09 MB/s   2.325 ratio
BenchmarkPICSVsAllCodecs/PICS-4/MR-4           10     950023 ns/op   137.97 MB/s   2.284 ratio
BenchmarkPICSVsAllCodecs/PICS-8/MR-4           10    1919178 ns/op    68.30 MB/s   2.214 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/CT-4           10    3795041 ns/op   138.15 MB/s   2.237 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/CT-4       10    3397327 ns/op   154.32 MB/s   2.237 ratio
BenchmarkPICSVsAllCodecs/PICS-1/CT-4           10    4166955 ns/op   125.82 MB/s   2.237 ratio
BenchmarkPICSVsAllCodecs/PICS-2/CT-4           10    2749362 ns/op   190.69 MB/s   2.214 ratio
BenchmarkPICSVsAllCodecs/PICS-4/CT-4           10    2415497 ns/op   217.05 MB/s   2.145 ratio
BenchmarkPICSVsAllCodecs/PICS-8/CT-4           10    3213578 ns/op   163.15 MB/s   1.962 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/CR-4           10   45542034 ns/op   165.40 MB/s   3.628 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/CR-4       10   33372518 ns/op   225.72 MB/s   3.628 ratio
BenchmarkPICSVsAllCodecs/PICS-1/CR-4           10   34794888 ns/op   216.49 MB/s   3.628 ratio
BenchmarkPICSVsAllCodecs/PICS-2/CR-4           10   20143545 ns/op   373.96 MB/s   3.641 ratio
BenchmarkPICSVsAllCodecs/PICS-4/CR-4           10   12577328 ns/op   598.92 MB/s   3.657 ratio
BenchmarkPICSVsAllCodecs/PICS-8/CR-4           10   13361385 ns/op   563.77 MB/s   3.678 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/XR-4           10   47818953 ns/op   220.74 MB/s   1.738 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/XR-4       10   44498836 ns/op   237.21 MB/s   1.738 ratio
BenchmarkPICSVsAllCodecs/PICS-1/XR-4           10   49627215 ns/op   212.69 MB/s   1.738 ratio
BenchmarkPICSVsAllCodecs/PICS-2/XR-4           10   30853689 ns/op   342.11 MB/s   1.748 ratio
BenchmarkPICSVsAllCodecs/PICS-4/XR-4           10   14307291 ns/op   737.76 MB/s   1.753 ratio
BenchmarkPICSVsAllCodecs/PICS-8/XR-4           10   14750650 ns/op   715.59 MB/s   1.755 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/MG1-4          10   25726358 ns/op   381.26 MB/s   8.566 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/MG1-4      10   26881777 ns/op   364.87 MB/s   8.565 ratio
BenchmarkPICSVsAllCodecs/PICS-1/MG1-4          10   26536179 ns/op   369.62 MB/s   8.566 ratio
BenchmarkPICSVsAllCodecs/PICS-2/MG1-4          10   16939822 ns/op   579.01 MB/s   8.597 ratio
BenchmarkPICSVsAllCodecs/PICS-4/MG1-4          10   11559448 ns/op   848.51 MB/s   8.693 ratio
BenchmarkPICSVsAllCodecs/PICS-8/MG1-4          10   12020529 ns/op   815.97 MB/s   8.771 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/MG2-4          10   25441251 ns/op   385.53 MB/s   8.553 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/MG2-4      10   25551903 ns/op   383.86 MB/s   8.552 ratio
BenchmarkPICSVsAllCodecs/PICS-1/MG2-4          10   27317878 ns/op   359.04 MB/s   8.553 ratio
BenchmarkPICSVsAllCodecs/PICS-2/MG2-4          10   17687067 ns/op   554.55 MB/s   8.587 ratio
BenchmarkPICSVsAllCodecs/PICS-4/MG2-4          10   12311340 ns/op   796.69 MB/s   8.682 ratio
BenchmarkPICSVsAllCodecs/PICS-8/MG2-4          10   10313084 ns/op   951.06 MB/s   8.755 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/MG3-4          10  136631577 ns/op   214.12 MB/s   2.289 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/MG3-4      10  152345604 ns/op   192.03 MB/s   2.289 ratio
BenchmarkPICSVsAllCodecs/PICS-1/MG3-4          10  159223203 ns/op   183.74 MB/s   2.289 ratio
BenchmarkPICSVsAllCodecs/PICS-2/MG3-4          10   78184850 ns/op   374.18 MB/s   2.325 ratio
BenchmarkPICSVsAllCodecs/PICS-4/MG3-4          10   43092903 ns/op   678.88 MB/s   2.362 ratio
BenchmarkPICSVsAllCodecs/PICS-8/MG3-4          10   42891987 ns/op   682.06 MB/s   2.390 ratio

BenchmarkPICSVsAllCodecs/MIC-Go/MG4-4          10   83422985 ns/op   326.80 MB/s   3.474 ratio
BenchmarkPICSVsAllCodecs/MIC-4state/MG4-4      10   84235072 ns/op   323.65 MB/s   3.473 ratio
BenchmarkPICSVsAllCodecs/PICS-1/MG4-4          10   93133681 ns/op   292.73 MB/s   3.474 ratio
BenchmarkPICSVsAllCodecs/PICS-2/MG4-4          10   48294341 ns/op   564.52 MB/s   3.517 ratio
BenchmarkPICSVsAllCodecs/PICS-4/MG4-4          10   33725830 ns/op   808.37 MB/s   3.585 ratio
BenchmarkPICSVsAllCodecs/PICS-8/MG4-4          10   34613094 ns/op   787.65 MB/s   3.618 ratio
```

---

## Related Work

- **MIC2 independent mode** (`multiframecompress.go`): frame-level parallelism
  for multi-frame DICOM; same idea applied at frame granularity.
- **MIC3 WSI** (`wsicompress.go`): tile-level parallelism with a goroutine
  worker pool (`Workers` option).  PICS brings analogous parallelism to the
  single-frame greyscale path.
- **4-state FSE** (`fse4state.go`): intra-core ILP; combines with PICS for
  multiplicative speedup (ILP × cores).
- **Wavelet SIMD** (`wavelet_simd_amd64.s`): a complementary approach — the
  blocked 2D wavelet transform uses AVX2 to parallelize arithmetic within one
  core.  PICS works with any compression pipeline, including the wavelet path.
