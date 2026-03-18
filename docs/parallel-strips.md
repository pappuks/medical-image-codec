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

### Measured results (MR 256×256, Delta+RLE+FSE two-state)

| Strips | Compressed | Ratio | Overhead vs 1-strip |
|--------|-----------|-------|---------------------|
| 1 | 55,821 B | **2.35×** | baseline |
| 2 | 56,377 B | 2.32× | +1.00% |
| 4 | 57,388 B | 2.28× | +2.81% |
| 8 | 59,199 B | 2.21× | +6.05% |
| 16 | 62,419 B | 2.10× | +11.82% |

The MR image is 256 rows tall — a relatively severe case.  For larger images
(CR: 2140 rows, MG: 3064–4096 rows) the per-strip overhead is proportionally
smaller.

**Recommendation**: 4 strips delivers the best practical trade-off — ~2.5–2.9×
faster compression/decompression with only ~2–3% ratio loss on a 4-core
machine.

---

## Throughput Scaling

All numbers below use the **CR image** (1760×2140, 7.18 MB raw,
3.63× compression), **Intel Xeon @ 2.10 GHz, 4 physical cores**, 5 iterations.

### Compression throughput

| Strips | MB/s | Speedup | Wall time (ms) |
|:------:|:----:|:-------:|:--------------:|
| 1 | 133 | 1.00× | 56.7 |
| 2 | 219 | **1.65×** | 34.4 |
| 4 | 401 | **3.02×** | 18.8 |
| 8 | 479 | **3.61×** | 15.7 |

Compression speedup continues past the core count at 8 strips because goroutine
scheduling and lock-free parallel writes allow the runtime to overlap I/O with
computation.

### Decompression throughput

| Strips | MB/s | Speedup | Wall time (ms) |
|:------:|:----:|:-------:|:--------------:|
| 1 | 186 | 1.00× | 40.6 |
| 2 | 346 | **1.86×** | 21.8 |
| 4 | 583 | **3.14×** | 12.9 |
| 8 | 540 | 2.91× | 14.0 |

Decompression peaks at 4 strips on this 4-core machine; 8 strips shows
slightly lower throughput due to goroutine scheduling overhead and memory
bandwidth saturation beyond the physical core count.

### Scaling efficiency

| Strips | Compress efficiency | Decompress efficiency |
|:------:|:-------------------:|:---------------------:|
| 2 | 82% | 93% |
| 4 | 75% | 78% |
| 8 | 45% | 36% |

Efficiency drops above the core count due to goroutine scheduling overhead,
memory bandwidth contention, and unequal strip compression times.  On a machine
with more cores, the 8-strip efficiency would be proportionally higher.

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

- You need to decode a single large medical image as fast as possible on a
  multi-core server or workstation.
- Your application has spare CPU cores during a decompression request (e.g.,
  a DICOM viewer that serves one image at a time).
- A small ratio overhead (2–3% for 4 strips) is acceptable in exchange for
  near-linear decompression speedup.

### Use MIC2 (multi-frame) instead when

- You have multiple frames (Tomosynthesis, Cine MRI, Fluoroscopy).
  Each frame is already an independent MIC stream — frame-level parallelism
  is available for free with no ratio overhead.

### Use MIC3 (WSI) instead when

- You have a large RGB whole-slide image.  The tiled format provides O(1)
  random tile access, pyramid levels, and parallel tile encode/decode.

### Use 1 strip (standard `CompressSingleFrame`) when

- You are on a single-core environment (embedded, WASM).
- You need the maximum compression ratio (no boundary overhead).
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
# Compression throughput at 1/2/4/8 strips (CR image)
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkParallelStripsCompress mic

# Decompression throughput at 1/2/4/8 strips (CR image)
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkParallelStripsDecompress mic

# Combined (both compress and decompress)
go test -benchmem -run=^$ -benchtime=5x -bench ^BenchmarkParallelStrips mic
```

### Benchmark output (Intel Xeon @ 2.10 GHz, 4 cores)

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
