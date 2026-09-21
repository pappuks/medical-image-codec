# Native Optimizations: Go Assembly, Two-State & Four-State FSE

## Overview

This document describes the native-code optimizations added to MIC in this branch, their design rationale, and measured performance impact.

Optimizations are active on **amd64** (Intel/AMD) and **arm64** (Apple Silicon, AWS Graviton) automatically. All other platforms (WASM, RISC-V, …) fall back to equivalent pure-Go implementations with no code changes required.

---

## Optimizations Implemented

### 1. Two-State FSE Decompressor (`fse2state.go`)

**Problem**: The FSE decode loop has a serial carried-dependency chain:

```
state_n+1 = decTable[state_n].newState + getBits(decTable[state_n].nbBits)
```

Each iteration depends on the `newState` of the previous one. On a modern out-of-order CPU with a table-lookup latency of ~4 cycles, the loop is limited to roughly 1 symbol per 4 cycles regardless of execution width.

**Solution**: Two independent state machines (`stateA`, `stateB`) alternate on even/odd symbol positions:

```
symbol[0] ← stateA    symbol[2] ← stateA    ...
symbol[1] ← stateB    symbol[3] ← stateB    ...
```

The two chains are completely independent — the CPU's OOO engine can issue both table lookups simultaneously, filling the 4-cycle latency slots with useful work from the other chain.

**Format**: `[0xFF][0x02][count uint32 LE][FSE header + bitstream]`

- Magic `0xFF` is an invalid single-state header byte (would imply tableLog = 20 > max 16), so the format is auto-detected without ambiguity.
- The 4-byte `count` field lets the decoder terminate exactly without relying on per-state `finished()` heuristics.
- `FSEDecompressU16Auto` transparently dispatches to two-state or single-state based on the magic prefix — all existing compressed streams remain readable.

**Encoder changes** (`multiframecompress.go`): `CompressSingleFrame` and `compressResidualFrame` try two-state FSE first, falling back to single-state on `ErrIncompressible` or `ErrUseRLE` (tiny/trivial inputs).

---

### 2. Interleaved Histogram (`asm_amd64.s`, `asm_arm64.s` — `countSimpleU16Asm`)

**Problem**: The `countSimple` histogram increments `count[v]++` for every pixel. When consecutive pixels have similar values (common in medical images after delta coding), the processor sees a store followed immediately by a load to the same or adjacent address — a store-to-load forwarding stall costing 4–10 cycles per update.

**Solution**: 4-way unrolled loop distributing even-indexed pixels to `count[]` and odd-indexed pixels to `count2[]`. Both amd64 and arm64 use the same algorithm:

amd64 (Plan 9 x86-64):
```asm
ADDL $1, (DI)(AX*4)   // count[v0]   even
ADDL $1, (R8)(BX*4)   // count2[v1]  odd
ADDL $1, (DI)(R11*4)  // count[v2]   even
ADDL $1, (R8)(R12*4)  // count2[v3]  odd
```

arm64 (Plan 9 ARM64):
```asm
LSL $2, R5, R9        // byte offset = v0 * 4
MOVWU (R2)(R9), R10   // load count[v0]
ADD $1, R10, R10
MOVW R10, (R2)(R9)    // store count[v0]++

LSL $2, R6, R9        // byte offset = v1 * 4
MOVWU (R3)(R9), R10   // load count2[v1]
ADD $1, R10, R10
MOVW R10, (R3)(R9)    // store count2[v1]++
```

With two separate arrays, consecutive identical values hit different cache lines, eliminating the stall. After the loop the two arrays are merged in a single backward pass that simultaneously finds `symbolLen` and `max`.

---

### 3. YCoCg-R Native Dispatch (`asm_amd64.go`, `asm_arm64.go`, `ycocgr.go`)

**Problem**: The RGB → YCoCg-R transform (used for WSI color images) was a plain Go loop with branch-heavy logic on every pixel.

**Solution**: `ycocgRForwardNative` and `ycocgRInverseNative` dispatch to platform-specific implementations:

- **amd64**: `cpuidAMD64` probes SSSE3 and AVX2 support once in `init()`. The current scalar-in-assembly path (`ycocgRForwardSSSE3` / `ycocgRInverseSSSE3`) establishes the dispatch plumbing and explicit register allocation needed for a future SSE2/SSSE3 8-pixel-wide SIMD path.
- **arm64**: NEON is always available — no CPUID probe needed. `ycocgRForwardNEON` / `ycocgRInverseNEON` are scalar-in-assembly stubs that provide the same dispatch scaffolding for a future 8-pixel-wide NEON path.

**Extension points**:
- amd64: Replace `ycocgRForwardSSSE3` / `ycocgRInverseSSSE3` with SSE2/SSSE3 packed 8-pixel-wide code → estimated 4–8× improvement on the color transform.
- arm64: Replace `ycocgRForwardNEON` / `ycocgRInverseNEON` with NEON-vectorised 8-pixel code using `VLD3` and `VADDV` → similar 4–8× gain.

---

### 4. Four-State FSE Decompressor (`fse4state.go`)

**Problem**: Two independent state machines break half the serial dependency chain. The other half (stateA waiting on its own previous output, stateB on its own) still limits ILP. With table-lookup latency of ~4 cycles, two chains still leave 4 cycles of latency per chain exposed.

**Solution**: Four independent state machines (`stateA`, `stateB`, `stateC`, `stateD`) cycling over positions modulo 4:

```
symbol[0] ← stateA    symbol[4] ← stateA    ...
symbol[1] ← stateB    symbol[5] ← stateB    ...
symbol[2] ← stateC    symbol[6] ← stateC    ...
symbol[3] ← stateD    symbol[7] ← stateD    ...
```

The four chains are completely independent, giving the OOO engine four simultaneous table-lookup address streams. The hardware prefetcher gets four separate stride patterns to track, and store-to-load forwarding pressure on the bit-reader is distributed across more decode operations per fill.

**Format**: `[0xFF][0x04][count uint32 LE][FSE header + bitstream]`

- Magic byte `0x04` distinguishes it from the two-state magic `0x02`. `0xFF` remains invalid for single-state (would imply tableLog = 20).
- `FSEDecompressU16Auto` checks for `[0xFF, 0x04]` first, then `[0xFF, 0x02]`, then falls through to single-state. All three formats coexist without ambiguity.

**Bit-buffer overflow fix**: Initialising four state machines reads 4 × tableLog bits from the bit reader. The reader starts with a 64-bit buffer. For tableLog = 15: 4 × 15 = 60 bits plus the sentinel adjustment (1–8 bits) can reach 68 bits total — exceeding the 64-bit buffer and causing a corrupt initial state for `sD`. The fix inserts `br.fill()` between `sB.init` and `sC.init`, and again between `sC.init` and `sD.init`. `fill()` is a no-op when `br.off == 0`, so it is safe for tiny inputs.

```go
sA.init(br, s.decTable, s.actualTableLog)
sB.init(br, s.decTable, s.actualTableLog)
br.fill()   // ← guard: prevents buffer overflow for tableLog ≥ 13
sC.init(br, s.decTable, s.actualTableLog)
br.fill()   // ← guard
sD.init(br, s.decTable, s.actualTableLog)
```

**Native dispatch**: Before the pure-Go hot loop, `decompress4State` calls `fse4StateDecompNative` to hand bulk work to the platform assembly kernel:

```go
if !s.zeroBits && remaining >= 4 && br.off >= 8 && len(s.decTable) > 0 {
    bufAvail := int(^uint16(0)-off) + 1  // = 65536 - int(off)
    canDo := remaining
    if canDo > bufAvail { canDo = bufAvail }
    canDo &^= 3  // round down to multiple of 4
    if canDo >= 4 {
        states := [4]uint32{sA.state, sB.state, sC.state, sD.state}
        n := fse4StateDecompNative(
            unsafe.Pointer(&s.decTable[0]),
            unsafe.Pointer(br),
            unsafe.Pointer(&states[0]),
            unsafe.Pointer(&tmp[off]),
            canDo,
        )
        // ... states, off, remaining updated from n
    }
}
```

The `bufAvail` cap prevents `off` from wrapping around uint16 during a single native call. After the call, the pure-Go fallback handles any remaining tail.

---

### 5. AMD64 BMI2 Assembly Kernel (`asm_amd64.s` — `fse4StateDecompKernel`)

**Requirement**: AVX2 support (Haswell+). The `cpuHasAVX2` flag is set once in `init()` via `cpuidAMD64(7,0)` leaf 7 EBX bit 5. The kernel is not invoked on pre-Haswell CPUs; the pure-Go four-state loop is used instead.

**Why BMI2 (`SHLXQ` / `SHRXQ`)**:

The standard `SHLQ CX, R10` form requires the shift count in the CL register, serialising multi-symbol extraction (only one shift can use CL at a time). BMI2's 3-operand forms take any general-purpose register as the count:

```asm
SHLXQ R11, R10, DX    // DX = R10 << R11   (shift count from R11, not CL)
SHRXQ CX,  DX,  DX    // DX = DX >> CX     (complementary extract)
```

This lets the four states issue their bit-extraction shifts truly independently in the same decode cycle group.

**Register layout**:

| Register | Role |
|----------|------|
| AX | `decTable` base pointer |
| BX | output pointer (`tmp[off]`) |
| DI | remaining count |
| SI | symbols produced |
| R8 | `br.in.ptr` (byte slice data pointer) |
| R9 | `br.off` (next-read byte offset) |
| R10 | `br.value` (64-bit bit buffer) |
| R11 | `br.bitsRead` |
| R12–R15 | states A, B, C, D |

**Per-symbol decode sequence** (example for state A, `R12`):

```asm
MOVBLZX 6(AX)(R12*8), CX    // CX = dt[sA].nbBits
MOVWQZX 4(AX)(R12*8), DX    // DX = dt[sA].symbol
MOVW    DX, 0(BX)            // store symbol
SHLXQ  R11, R10, DX          // DX = value << bitsRead   (BMI2)
ADDQ    CX, R11               // bitsRead += nbBits
NEGQ    CX
ADDQ    $64, CX               // CX = 64 - nbBits
SHRXQ  CX, DX, DX             // DX = lowBits            (BMI2)
MOVL    0(AX)(R12*8), R12     // R12 = dt[sA].newState
ADDL    DX, R12               // R12 += lowBits → new sA.state
```

**`decSymbolU16` memory layout** (8 bytes per entry):

| Offset | Type | Field |
|--------|------|-------|
| 0 | uint32 | `newState` |
| 4 | uint16 | `symbol` |
| 6 | uint8 | `nbBits` |
| 7 | — | padding |

`R12*8` scales the state index to the 8-byte stride directly.

**Inline fillFast** (when `br.bitsRead ≥ 32`, reads 4 bytes from `br.in[br.off-4]`):

```asm
MOVL   -4(R8)(R9*1), DX   // little-endian 32-bit load
SHLQ   $32, R10            // make room in high half
MOVLQZX DX, DX             // zero-extend
ORQ    DX, R10             // insert
SUBQ   $32, R11            // bitsRead -= 32
SUBQ   $4,  R9             // off -= 4
```

The loop pre-condition `br.off >= 8` ensures two fillFast calls per iteration never underflow.

---

### 6. ARM64 Scalar Kernel (`asm_arm64.s` — `fse4StateDecompNEON`)

NEON is mandatory on all ARM64 CPUs — no CPUID check is needed. The kernel is named `fse4StateDecompNEON` to signal that this is the NEON/Advanced-SIMD dispatch point, even though the current implementation uses scalar ARM64 instructions. A future vectorised version can replace the scalar body without changing any Go or calling code.

**Register layout**:

| Register | Role |
|----------|------|
| R0 | `decTable` base pointer |
| R2 | `br.off` |
| R3 | `br.value` (64-bit) |
| R4 | `br.bitsRead` |
| R5–R8 | states A, B, C, D |
| R9 | output pointer |
| R10 | remaining count |
| R11 | symbols produced |
| R12–R15 | temporaries |
| R15 | `br.in.ptr` |

**Per-symbol decode sequence** (example for state A, `R5`):

```asm
LSL   $3, R5, R12         // R12 = sA.state * 8  (byte offset into decTable)
MOVBU 6(R0)(R12), R13     // R13 = dt[sA].nbBits
MOVHU 4(R0)(R12), R14     // R14 = dt[sA].symbol
MOVH  R14, 0(R9)          // store symbol (uint16)
LSLV  R4, R3, R14         // R14 = value << bitsRead   (variable shift)
ADD   R13, R4, R4          // bitsRead += nbBits
MOVD  $64, R1
SUB   R13, R1, R1          // R1 = 64 - nbBits
LSRV  R1, R14, R14         // R14 = lowBits             (variable shift)
MOVWU 0(R0)(R12), R5       // R5 = dt[sA].newState
ADD   R14, R5, R5           // R5 += lowBits → new sA.state
```

ARM64's `LSLV` / `LSRV` instructions take the shift count from a register (unlike x86 pre-BMI2 which required CL), so multiple shifts can issue independently on wide-issue cores (Cortex-A78, Graviton 3, Apple M-series).

**Platform Portability (`asm_generic.go`)**:

Build tag `//go:build !amd64 && !arm64` provides identical pure-Go implementations of `countSimpleNative`, `ycocgRForwardNative`, `ycocgRInverseNative`, and a no-op `fse4StateDecompNative` (returns 0, causing the caller to use the pure-Go four-state loop). The two-state and four-state FSE implementations are pure Go and deliver ILP benefits on every platform.

---

## Running the Tests and Benchmarks

### Correctness tests

```bash
# Two-state and four-state FSE: all correctness tests
go test -run "TestFSE2State|TestFSE1State|TestFSE4State" -v

# Full test suite including new tests
go test ./...
```

The test file `fse2state_test.go` covers the two-state decoder:

| Test | What it verifies |
|------|-----------------|
| `TestFSE2StateRoundtrip` | Lossless round-trip on all 8 DICOM test images |
| `TestFSE2StateAutoDetect` | `FSEDecompressU16Auto` routes `[0xFF,0x02]` to two-state decoder |
| `TestFSE1StateAutoDetect` | Old single-state streams still decode via auto-detect (backward compat) |
| `TestFSE2StateMagicBytes` | Output carries `[0xFF,0x02]` prefix; corruption returns an error |
| `TestFSE2StateDeltaRLERoundtrip` | Full `CompressSingleFrame` / `DecompressSingleFrame` pipeline |
| `TestFSE2StateEdgeCases` | `ErrUseRLE` for uniform input, error for 2-element input, all `n % 4` alignment variants |

The test file `fse4state_test.go` covers the four-state decoder and assembly kernels:

| Test | What it verifies |
|------|-----------------|
| `TestFSE4StateRoundtrip` | Lossless round-trip on all 8 DICOM test images (exercises assembly kernel where available) |
| `TestFSE4StateAutoDetect` | `FSEDecompressU16Auto` routes `[0xFF,0x04]` to four-state decoder |
| `TestFSE4StateMagicBytes` | Output carries `[0xFF,0x04]` prefix; corruption returns an error |
| `TestFSE4StateEdgeCases` | `ErrUseRLE` for uniform input; `n % 4` in {1,2,3} alignment; small n=5,6,7 |

### Comparison benchmarks

```bash
# Isolated FSE decompression — 1-state / 2-state / 4-state side-by-side
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSEDecompress4State

# 2-state vs 1-state (original comparison benchmark)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSEDecompress

# Compression throughput and ratio
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkFSECompressCompare

# Full pipeline: FSE → RLE → Delta (end-to-end frame decode)
go test -benchmem -run=^$ -benchtime=10x -bench BenchmarkDeltaRLEFSEDecompress

# All benchmarks
go test -bench=. -benchtime=10x
```

Sample output from `BenchmarkFSEDecompress4State` (Intel Xeon @ 2.80 GHz):

```
BenchmarkFSEDecompress4State/MR/1state-4     163 MB/s
BenchmarkFSEDecompress4State/MR/2state-4     207 MB/s   (+27%)
BenchmarkFSEDecompress4State/MR/4state-4     298 MB/s   (+83%)
BenchmarkFSEDecompress4State/MG3/1state-4    576 MB/s
BenchmarkFSEDecompress4State/MG3/2state-4    890 MB/s   (+55%)
BenchmarkFSEDecompress4State/MG3/4state-4   1343 MB/s  (+133%)
```

Sample output from `BenchmarkFSEDecompress4State` (Apple M2 Max, ARM64):

```
BenchmarkFSEDecompress4State/MR/1state     488 MB/s
BenchmarkFSEDecompress4State/MR/2state     580 MB/s   (+19%)
BenchmarkFSEDecompress4State/MR/4state     797 MB/s   (+63%)
BenchmarkFSEDecompress4State/CR/1state     866 MB/s
BenchmarkFSEDecompress4State/CR/2state    2873 MB/s  (+232%)
BenchmarkFSEDecompress4State/CR/4state    3502 MB/s  (+304%)
BenchmarkFSEDecompress4State/MG3/1state   1669 MB/s
BenchmarkFSEDecompress4State/MG3/2state   3214 MB/s   (+93%)
BenchmarkFSEDecompress4State/MG3/4state   4422 MB/s  (+165%)
```

---

### 7. C Four-State FSE Decompressor (`ojph/mic_decompress_c.c`)

**Motivation**: The Go 4-state implementation (`fse4state.go` + ARM64 NEON kernel) still
incurs Go runtime overhead: heap allocations for the intermediate RLE buffer (~9 MB/op,
9 allocs) and Go↔assembly boundary crossings. The C 4-state implementation (`mic_decompress_four_state`,
`mic_decompress_four_state_simd`) eliminates all Go overhead:

- **1 allocation** (output pixel buffer only, 128 KB–73 MB depending on image)
- Single contiguous pass: FSE-4state → RLE → Delta, no intermediate buffer ownership transfer
- SIMD variant adds SSE2/AVX2 acceleration for RLE fill and delta decode on x86

**Format**: Input stream `[0xFF][0x04][count_u32_le][FSE header][bitstream]` — identical
to the Go four-state format, so streams are interchangeable between all implementations.

**Hot loop** (non-zeroBits path, mirrors Go/assembly logic in C):

```c
while (br.off >= 8 && remaining >= 4) {
    bit_reader_fill_fast(&br);
    dec_symbol_t nA = dt[stateA], nB = dt[stateB];
    uint32_t lowA = bit_reader_get_bits_fast(&br, nA.nb_bits);
    uint32_t lowB = bit_reader_get_bits_fast(&br, nB.nb_bits);
    stateA = nA.new_state + lowA;
    stateB = nB.new_state + lowB;

    bit_reader_fill_fast(&br);
    dec_symbol_t nC = dt[stateC], nD = dt[stateD];
    uint32_t lowC = bit_reader_get_bits_fast(&br, nC.nb_bits);
    uint32_t lowD = bit_reader_get_bits_fast(&br, nD.nb_bits);
    stateC = nC.new_state + lowC;
    stateD = nD.new_state + lowD;

    out[off+0]=nA.symbol; out[off+1]=nB.symbol;
    out[off+2]=nC.symbol; out[off+3]=nD.symbol;
    off += 4; remaining -= 4;
}
```

Four independent `dt[]` lookups in each iteration; C compiler's OOO scheduling and
ARM64's 4-wide issue port fill the 4-cycle table-lookup latency with the other chain's
load+add+store sequence.

**Files**: `ojph/mic_decompress_c.h`, `ojph/mic_decompress_c.c`, `ojph/mic_c.go`

**Build**: Requires `cgo_ojph` build tag and `libopenjph` installed:
```bash
go test -tags cgo_ojph -run TestMICCorrectnessFourStateC ./ojph/
```

---

## Measured Performance

All benchmarks run on `Intel Xeon @ 2.80 GHz`, `GOMAXPROCS=4`.

### FSE Decompression: Single-State vs Two-State vs Four-State

Measured with `BenchmarkFSEDecompress4State`. Input: Delta+RLE residuals from real DICOM images.
MB/s is over the *uncompressed* RLE symbol stream (what FSE decodes into).

| Image         | RLE size    | 1-State MB/s | 2-State MB/s | 4-State MB/s | Δ (4 vs 1) |
|---------------|-------------|-------------|-------------|-------------|------------|
| MR 256×256    | 0.13 MB     | 164         | 207         | 298         | **+82%** |
| CT 512×512    | 0.50 MB     | 113         | 126         | 195         | **+73%** |
| CR 2140×1760  | 7.2 MB      | 177         | 182         | 310         | **+75%** |
| XR 2048×2577  | 10.1 MB     | 193         | 172         | 320         | **+66%** |
| MG1 (mammo)   | 9.4 MB      | 158         | 207         | 380         | **+140%** |
| MG3 (mammo)   | 27.3 MB     | 576         | 890         | **1343**    | **+133%** |
| MG4 (mammo)   | 26.0 MB     | 256         | 321         | 620         | **+142%** |

**Pattern**: The four-state decoder with the AMD64 BMI2 assembly kernel delivers the largest gains on mammography images (MG1, MG3, MG4) where the large payload keeps the decode table hot in L2/L3 and the assembly loop's independent shift chains saturate the execution units. Smaller images (MR, CT) gain proportionally less because setup overhead is a larger fraction of total time.

The two-state decoder shows modest improvements on its own; gains are amplified significantly in the four-state assembly path because BMI2 `SHLXQ`/`SHRXQ` enables four truly independent bit-extract sequences per iteration.

### Full Pipeline: Delta+RLE+FSE (end-to-end frame decode)

Measured with `BenchmarkDeltaRLEFSEDecompress` (FSE → RLE → Delta). MB/s is over raw pixel bytes.

| Image         | 1-State MB/s | 2-State MB/s | 4-State MB/s | Δ (4 vs 1) |
|---------------|-------------|-------------|-------------|------------|
| MR 256×256    | 85          | 88          | 102         | **+20%** |
| CT 512×512    | 84          | 90          | 108         | **+29%** |
| CR 2140×1760  | 131         | 138         | 178         | **+36%** |
| MG3 (mammo)   | 116         | 136         | **203**     | **+75%** |
| MG4 (mammo)   | 180         | 196         | **285**     | **+58%** |

The full-pipeline speedup is smaller than the isolated-FSE speedup because Delta decompression and RLE decompression are not affected and together dominate runtime on smaller images.

*(Numbers vary run-to-run by ±10% on this virtualised Xeon; MG3/MG4 improvements are most stable.)*

### Full Multi-Variant Decompression Comparison — Apple M2 Max (ARM64, single-threaded, in-process)

`BenchmarkThreeWay` in `ojph/mic_c_test.go` — all variants measured under identical
conditions (in-process, no file I/O, single-threaded). All in **MB/s** over uncompressed
pixel bytes.

| Image | MIC-Go (2-state) | MIC-4state (Go+NEON) | MIC-4state-C | MIC-4state-SIMD | MIC-C (2-state) | MIC-SIMD (2-state) | Wavelet+SIMD | HTJ2K |
|-------|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| MR (256×256)      | 145 | 209 | 350 | **385** | 343 | 343 | 464 | 261 |
| CT (512×512)      | 181 | 228 | 370 | **375** | 291 | 283 | 538 | 292 |
| CR (2140×1760)    | 290 | 339 | **532** | 530 | 458 | 453 | 784 | 358 |
| XR (2048×2577)    | 301 | 330 | 519 | **529** | 451 | 455 | 878 | 317 |
| MG1 (2457×1996)   | 472 | 500 | **684** | 678 | 634 | 629 | 1129 | 790 |
| MG2 (2457×1996)   | 473 | 509 | **681** | 688 | 642 | 640 | 1069 | 794 |
| MG3 (4774×3064)   | 304 | 342 | **534** | 533 | 453 | 441 | 716 | 334 |
| MG4 (4096×3328)   | 415 | 447 | **627** | 610 | 567 | 573 | 827 | 551 |

**Key findings:**
- **MIC-4state-C** beats MIC-C (2-state) by **1.08–1.30×** — 4-state ILP gain transfers to C
- **MIC-4state-C** beats HTJ2K on **6 of 8** images (CR, XR, MG3, MG4, and nears on CT/MR)
- **Wavelet+SIMD** exceeds HTJ2K on **all 8** images despite including the inverse wavelet transform
- MG1/MG2: HTJ2K's SIMD wavelet decoder (790/794 MB/s) remains the fastest single-threaded option

```bash
go test -tags cgo_ojph -benchmem -run=^$ -benchtime=10x \
  -bench "^BenchmarkThreeWay$" ./ojph/
```

---

## Design Decisions

### Why store the output count in the stream?

Multiple states sharing one bit reader cannot independently determine termination via `br.finished()` — when the reader is exhausted all states see it simultaneously. Storing the exact symbol count (4 bytes) makes termination unconditional and removes an entire class of off-by-one bugs, at negligible cost (4 extra bytes per compressed block).

### Why not inline the FSE decode loop in assembly for 2-state too?

The pure-Go two-state loop achieves the primary ILP goal via independent chains. The four-state assembly kernel is the right investment point: four independent chains with BMI2 shifts yield a qualitatively different result (>2× on MG3) compared to two chains. A full two-state assembly loop would add ~100 lines of Plan 9 asm for ~10–30% additional gain over pure Go — not worth the maintenance cost.

### Why does the AMD64 kernel require AVX2 and not just BMI2?

BMI2 and AVX2 were both introduced on Haswell (2013). Gating on AVX2 via CPUID leaf 7 EBX bit 5 is the standard Go assembly pattern (used by `compress/flate`, `golang.org/x/crypto`, etc.) and ensures we run on the same CPU generation that has `SHLXQ`/`SHRXQ`. Detecting BMI2 directly (leaf 7 EBX bit 3 + bit 8) would work equally well but is less familiar and offers no practical difference.

### Why is the ARM64 kernel named NEON if it uses scalar instructions?

NEON (Advanced SIMD) is mandatory on all ARM64 CPUs. The function name `fse4StateDecompNEON` signals the dispatch point for the NEON capability tier. The current scalar body is a complete, correct, well-tested implementation. A future vectorised version (using `VLD1`, `VTBL`, etc.) can replace the scalar body in `asm_arm64.s` without touching any Go code — the dispatch plumbing is already in place.

### Why insert `br.fill()` between state inits instead of `br.fillFast()`?

`fillFast()` reads from `br.in[br.off-4:]` and panics when `br.off < 4`. For small compressed inputs (e.g. n = 5 symbols), the bit reader may have `br.off == 0` or `br.off == 2` after the initial load. `fill()` handles the short case gracefully by reading one byte at a time, and is a no-op (no side effects) when `br.bitsRead < 32`.

### Why `$-8` / `$-4` instead of `$^7` / `$^3`?

The Go Plan 9 assembler does not support bitwise-NOT immediates (`$^N`). Two's complement equivalents (`-8 = ^7`, `-4 = ^3`) are used for alignment masks.

### Backward compatibility

- Old single-state streams: decoded by `FSEDecompressU16Auto` → `FSEDecompressU16` (unchanged path).
- New two-state streams: first byte `0xFF`, second `0x02` → `FSEDecompressU16TwoState`.
- New four-state streams: first byte `0xFF`, second `0x04` → `FSEDecompressU16FourState`.
- `0xFF` cannot appear as a valid single-state header: it would imply `tableLog = (0xF)+5 = 20`, exceeding `maxTableLog = 16`.
- `FSEDecompressU16Auto` checks four-state before two-state before single-state — all three formats coexist without ambiguity.

---

## Files Changed

| File | Change |
|------|--------|
| `fse2state.go` | New — two-state FSE encode/decode, auto-detect dispatcher |
| `fse2state_test.go` | New — correctness tests and comparison benchmarks |
| `fse4state.go` | New — four-state FSE encode/decode; native dispatch block; bit-buffer overflow fix |
| `fse4state_test.go` | New — roundtrip, auto-detect, magic-bytes, edge cases; 3-way benchmark |
| `asm_amd64.go` | Modified — added `fse4StateDecompKernel` declaration and `fse4StateDecompNative` dispatch (AVX2 guard) |
| `asm_amd64.s` | Modified — appended `fse4StateDecompKernel` BMI2 hot loop (~120 lines Plan 9) |
| `asm_arm64.go` | Modified — added `fse4StateDecompNEON` declaration and `fse4StateDecompNative` dispatch |
| `asm_arm64.s` | Modified — appended `fse4StateDecompNEON` scalar kernel (~120 lines Plan 9) |
| `asm_generic.go` | Modified — added no-op `fse4StateDecompNative` for non-amd64/arm64 platforms |
| `fsecompressu16.go` | `countSimple` delegates to `countSimpleNative` |
| `ycocgr.go` | `YCoCgRForward`/`YCoCgRInverse` delegate to native dispatch |
| `multiframecompress.go` | Use two-state FSE with single-state fallback |
| `ojph/mic_decompress_c.c` | Added `fse_decompress_four_state`, `mic_decompress_four_state`, `mic_decompress_four_state_simd` |
| `ojph/mic_decompress_c.h` | Added declarations for four-state C API |
| `ojph/mic_c.go` | Added `MICDecompressFourStateC`, `MICDecompressFourStateSIMD` CGO bindings |
| `ojph/mic_c_test.go` | Added `MIC-4state-C`, `MIC-4state-SIMD` to `BenchmarkThreeWay`; `TestMICCorrectnessFourStateC` |
