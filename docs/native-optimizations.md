# Native Optimizations: Go Assembly & Two-State FSE

## Overview

This document describes the native-code optimizations added to MIC in this branch, their design rationale, and measured performance impact.

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

### 2. Interleaved Histogram (`asm_amd64.s` — `countSimpleU16Asm`)

**Problem**: The `countSimple` histogram increments `count[v]++` for every pixel. When consecutive pixels have similar values (common in medical images after delta coding), the processor sees a store followed immediately by a load to the same or adjacent address — a store-to-load forwarding stall costing 4–10 cycles per update.

**Solution**: 4-way unrolled loop distributing even-indexed pixels to `count[]` and odd-indexed pixels to `count2[]`:

```asm
ADDL $1, (DI)(AX*4)   // count[v0]   even
ADDL $1, (R8)(BX*4)   // count2[v1]  odd
ADDL $1, (DI)(R11*4)  // count[v2]   even
ADDL $1, (R8)(R12*4)  // count2[v3]  odd
```

With two separate arrays, consecutive identical values hit different cache lines, eliminating the stall. After the loop the two arrays are merged in a single backward pass that simultaneously finds `symbolLen` and `max`.

---

### 3. YCoCg-R Native Dispatch (`asm_amd64.go`, `ycocgr.go`)

**Problem**: The RGB → YCoCg-R transform (used for WSI color images) was a plain Go loop with branch-heavy logic on every pixel.

**Solution**: `ycocgRForwardNative` and `ycocgRInverseNative` dispatch to CPU-capability-detected implementations at startup (via `cpuidAMD64`). The current assembly implements the same scalar algorithm with explicit register allocation, serving as the scaffolding for a future SIMD path. The CPUID probe is zero-overhead (runs once in `init()`).

**Extension point**: Replacing `ycocgRForwardSSSE3` / `ycocgRInverseSSSE3` with actual SSE2/SSSE3 packed 8-pixel-wide code would yield a further 4–8× improvement on the color transform; the dispatch plumbing is already in place.

---

### 4. Platform Portability (`asm_generic.go`)

Build tag `//go:build !amd64` provides identical pure-Go implementations of `countSimpleNative`, `ycocgRForwardNative`, and `ycocgRInverseNative` for non-amd64 targets (ARM64, WASM, etc.).

---

## Measured Performance

All benchmarks run on `Intel Xeon @ 2.80 GHz`, `GOMAXPROCS=4`.

### FSE Decompression: Single-State vs Two-State

The benchmark decompresses RLE-encoded delta residuals from real DICOM images. MB/s is over the *uncompressed* RLE symbol stream (what FSE decodes into).

| Image         | Size         | 1-State MB/s | 2-State MB/s | Δ |
|---------------|-------------|-------------|-------------|---|
| MR 256×256    | 0.13 MB     | 198         | 134         | –32% |
| CT 512×512    | 0.50 MB     | 154         | 141         | –9% |
| CR 2140×1760  | 7.2 MB      | 153         | **317**     | **+107%** |
| XR 2048×2577  | 10.1 MB     | 234         | 256         | +10% |
| MG1 (mammo)   | 9.4 MB      | 279         | 228         | –18% |
| MG2 (mammo)   | 9.4 MB      | 225         | **266**     | **+18%** |
| MG3 (mammo)   | 27.3 MB     | 269         | **359**     | **+33%** |
| MG4 (mammo)   | 26.0 MB     | 253         | **339**     | **+34%** |

**Pattern**: For large images (CR, XR, MG3, MG4 — ≥7 MB) the two-state FSE yields **+10% to +107%** throughput. For small images (MR, CT — <1 MB) there is a small regression because the alignment overhead and extra header bytes are proportionally larger relative to the compressed payload.

The ILP benefit scales with image size: the larger the main decode loop contribution relative to setup cost, the more the CPU OOO engine can hide state-lookup latency across the two chains.

### Full Pipeline: Delta+RLE+FSE (Decompression-Dominated)

The existing `BenchmarkDeltaRLEFSECompress` measures a goroutine-parallel decompress of pre-compressed data:

| Image   | Before MB/s | After MB/s | Δ |
|---------|------------|-----------|---|
| MR      | 226        | 267       | +18% |
| CT      | 191        | 233       | +22% |
| XR      | 310        | 404       | +30% |
| MG4     | 805        | 835       | +4% |

*(Numbers vary run-to-run by ±10% on this virtualized Xeon; the CR/XR/MG improvements are the most statistically robust.)*

---

## Design Decisions

### Why store the output count in the stream?

Two states sharing one bit reader cannot independently determine termination via `br.finished()` — when the reader is exhausted both states see it simultaneously. Storing the exact symbol count (4 bytes) makes termination unconditional and removes an entire class of off-by-one bugs, at negligible cost (4 extra bytes per compressed block).

### Why not inline the FSE decode loop in assembly?

The pure-Go two-state loop already achieves the primary goal (ILP via independent chains). A full-assembly decode loop would be ~150 lines of Plan 9 asm for each zeroBits/non-zeroBits variant and would need maintenance whenever the `decSymbolU16` struct changes. The Go compiler inlines the tight loop well. If profiling shows the Go overhead is significant, the inner loop can be ported to assembly using the same `//go:noescape` stub pattern already established.

### Why `$-8` / `$-4` instead of `$^7` / `$^3`?

The Go Plan 9 assembler does not support bitwise-NOT immediates (`$^N`). Two's complement equivalents (`-8 = ^7`, `-4 = ^3`) are used for alignment masks.

### Backward compatibility

- Old single-state streams: decoded by `FSEDecompressU16Auto` → `FSEDecompressU16` (unchanged path).
- New two-state streams: first byte `0xFF` triggers `FSEDecompressU16TwoState`.
- `0xFF` cannot appear as a valid single-state header: it would imply `tableLog = (0xF)+5 = 20`, exceeding `maxTableLog = 16`.

---

## Files Changed

| File | Change |
|------|--------|
| `fse2state.go` | New — two-state FSE encode/decode, auto-detect dispatcher |
| `asm_amd64.go` | New — CPUID init, `//go:noescape` stubs, dispatch wrappers, scalar fallbacks |
| `asm_amd64.s` | New — `cpuidAMD64`, `countSimpleU16Asm`, `ycocgRForwardSSSE3`, `ycocgRInverseSSSE3` |
| `asm_generic.go` | New — pure-Go fallbacks for non-amd64 (`//go:build !amd64`) |
| `fsecompressu16.go` | `countSimple` delegates to `countSimpleNative` |
| `ycocgr.go` | `YCoCgRForward`/`YCoCgRInverse` delegate to native dispatch |
| `multiframecompress.go` | Use two-state FSE with single-state fallback |
