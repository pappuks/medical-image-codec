// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// func rans8StateDecompKernel(dt, br, states, out unsafe.Pointer, count int) int
//
// ABI0 stack layout (Go calls assembly via stack):
//   dt+0(FP)      unsafe.Pointer (8 bytes) — *decSymbolU16 base
//   br+8(FP)      unsafe.Pointer (8 bytes) — *bitReader
//   states+16(FP) unsafe.Pointer (8 bytes) — *[8]uint32
//   out+24(FP)    unsafe.Pointer (8 bytes) — *uint16 output buffer
//   count+32(FP)  int            (8 bytes) — symbols to decode
//   ret+40(FP)    int            (8 bytes) — return: symbols written
//
// bitReader layout (offsets from br pointer):
//   0(br)  = in.ptr   *byte   (first field of slice header)
//   24(br) = off      uint
//   32(br) = value    uint64
//   40(br) = bitsRead uint8
//
// decSymbolU16 layout (8 bytes each):
//   0: newState uint32
//   4: symbol   uint16
//   6: nbBits   uint8
//   7: padding
//
// Register allocation:
//   AX  = decTable base
//   BX  = output pointer (uint16*)
//   CX  = nbBits (per symbol, then 64-nbBits for SHRXQ)
//   DX  = state index / newState / scratch
//   SI  = symbol / lowBits scratch (not used as loop counter here)
//   DI  = remaining count
//   R8  = br.in.ptr
//   R9  = br.off
//   R10 = br.value
//   R11 = br.bitsRead (zero-extended)
//   R12 = sA  (callee-saved)
//   R13 = sB  (callee-saved)
//   R14 = sC  (callee-saved)
//   R15 = sD  (callee-saved)
//   X1  = [sE, sF, sG, sH] packed as uint32×4 (SSE4.1 PEXTRD/PINSRD)
//
// X1 is NOT callee-saved on Linux/Mac AMD64 ABI; Go's ABI0 makes the
// caller responsible, so we can use X1 freely in this leaf function.
//
// The produced count is computed as (count − remaining) on return,
// freeing SI for use as a general scratch register in the loop body.
//
// Uses BMI2 SHLXQ/SHRXQ for variable-shift bit extraction (Haswell+,
// implied by cpuHasAVX2 guard in the Go dispatcher).
// Uses SSE4.1 PEXTRD/PINSRD for XMM element access (also Haswell+).
//
TEXT ·rans8StateDecompKernel(SB),NOSPLIT,$32-48
    // ── Prologue: save callee-saved registers ─────────────────────────────
    MOVQ R12, 0(SP)
    MOVQ R13, 8(SP)
    MOVQ R14, 16(SP)
    MOVQ R15, 24(SP)

    // ── Load arguments ────────────────────────────────────────────────────
    MOVQ dt+0(FP),     AX   // decTable base
    MOVQ br+8(FP),     DX   // bitReader pointer (temp — freed below)
    MOVQ states+16(FP), CX  // states pointer (temp)
    MOVQ out+24(FP),   BX   // output pointer
    MOVQ count+32(FP), DI   // remaining count

    // ── Load bitReader fields ─────────────────────────────────────────────
    MOVQ 0(DX),  R8    // br.in.ptr
    MOVQ 24(DX), R9    // br.off
    MOVQ 32(DX), R10   // br.value
    MOVBLZX 40(DX), R11 // br.bitsRead (byte → 64-bit ZX)

    // ── Load states A–D into scalar registers ─────────────────────────────
    MOVL 0(CX),  R12   // sA
    MOVL 4(CX),  R13   // sB
    MOVL 8(CX),  R14   // sC
    MOVL 12(CX), R15   // sD

    // ── Load states E–H into XMM1 (packed uint32×4) ──────────────────────
    MOVQ 16(CX), SI     // Load low 64 bits (sE, sF) using GPR
    MOVQ SI, X1         // move to XMM1 low 64 bits
    MOVQ 24(CX), SI     // Load high 64 bits (sG, sH)
    PINSRQ $1, SI, X1   // insert into XMM1 high 64 bits

decode8_loop:
    CMPQ DI, $8
    JL   decode8_done
    CMPQ R9, $16        // need 16 bytes: up to 4 fillFast each consuming 4 bytes
    JL   decode8_done

    // ── fillFast 1: before symbols A–B ────────────────────────────────────
    CMPQ R11, $32
    JL   nf1_8
    MOVL -4(R8)(R9*1), DX
    SHLQ $32, R10
    MOVLQZX DX, DX
    ORQ  DX, R10
    SUBQ $32, R11
    SUBQ $4,  R9
nf1_8:

    // Symbol A (state in R12)
    MOVBLZX 6(AX)(R12*8), CX   // CX = nbBits_A
    MOVWQZX 4(AX)(R12*8), DX   // DX = symbol_A
    MOVW    DX, 0(BX)
    SHLXQ  R11, R10, DX
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, DX, DX
    MOVL    0(AX)(R12*8), R12
    ADDL    DX, R12

    // Symbol B (state in R13)
    MOVBLZX 6(AX)(R13*8), CX
    MOVWQZX 4(AX)(R13*8), DX
    MOVW    DX, 2(BX)
    SHLXQ  R11, R10, DX
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, DX, DX
    MOVL    0(AX)(R13*8), R13
    ADDL    DX, R13

    // ── fillFast 2: before symbols C–D ────────────────────────────────────
    CMPQ R11, $32
    JL   nf2_8
    MOVL -4(R8)(R9*1), DX
    SHLQ $32, R10
    MOVLQZX DX, DX
    ORQ  DX, R10
    SUBQ $32, R11
    SUBQ $4,  R9
nf2_8:

    // Symbol C (state in R14)
    MOVBLZX 6(AX)(R14*8), CX
    MOVWQZX 4(AX)(R14*8), DX
    MOVW    DX, 4(BX)
    SHLXQ  R11, R10, DX
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, DX, DX
    MOVL    0(AX)(R14*8), R14
    ADDL    DX, R14

    // Symbol D (state in R15)
    MOVBLZX 6(AX)(R15*8), CX
    MOVWQZX 4(AX)(R15*8), DX
    MOVW    DX, 6(BX)
    SHLXQ  R11, R10, DX
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, DX, DX
    MOVL    0(AX)(R15*8), R15
    ADDL    DX, R15

    // ── fillFast 3: before symbols E–F ────────────────────────────────────
    CMPQ R11, $32
    JL   nf3_8
    MOVL -4(R8)(R9*1), DX
    SHLQ $32, R10
    MOVLQZX DX, DX
    ORQ  DX, R10
    SUBQ $32, R11
    SUBQ $4,  R9
nf3_8:

    // Symbol E  (X1[0])
    PEXTRD $0, X1, DX           // DX = sE
    MOVBLZX 6(AX)(DX*8), CX   // CX = nbBits_E
    MOVWQZX 4(AX)(DX*8), SI   // SI = symbol_E
    MOVW    SI, 8(BX)
    MOVL    0(AX)(DX*8), DX   // DX = newState_E
    SHLXQ  R11, R10, SI
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, SI, SI          // SI = lowBits_E
    ADDL    SI, DX              // DX = new sE
    PINSRD  $0, DX, X1         // X1[0] = new sE

    // Symbol F  (X1[1])
    PEXTRD $1, X1, DX
    MOVBLZX 6(AX)(DX*8), CX
    MOVWQZX 4(AX)(DX*8), SI
    MOVW    SI, 10(BX)
    MOVL    0(AX)(DX*8), DX
    SHLXQ  R11, R10, SI
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, SI, SI
    ADDL    SI, DX
    PINSRD  $1, DX, X1

    // ── fillFast 4: before symbols G–H ────────────────────────────────────
    CMPQ R11, $32
    JL   nf4_8
    MOVL -4(R8)(R9*1), DX
    SHLQ $32, R10
    MOVLQZX DX, DX
    ORQ  DX, R10
    SUBQ $32, R11
    SUBQ $4,  R9
nf4_8:

    // Symbol G  (X1[2])
    PEXTRD $2, X1, DX
    MOVBLZX 6(AX)(DX*8), CX
    MOVWQZX 4(AX)(DX*8), SI
    MOVW    SI, 12(BX)
    MOVL    0(AX)(DX*8), DX
    SHLXQ  R11, R10, SI
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, SI, SI
    ADDL    SI, DX
    PINSRD  $2, DX, X1

    // Symbol H  (X1[3])
    PEXTRD $3, X1, DX
    MOVBLZX 6(AX)(DX*8), CX
    MOVWQZX 4(AX)(DX*8), SI
    MOVW    SI, 14(BX)
    MOVL    0(AX)(DX*8), DX
    SHLXQ  R11, R10, SI
    ADDQ    CX, R11
    NEGQ    CX
    ADDQ    $64, CX
    SHRXQ  CX, SI, SI
    ADDL    SI, DX
    PINSRD  $3, DX, X1

    ADDQ $16, BX     // advance output: 8 symbols × 2 bytes
    SUBQ $8,  DI     // remaining -= 8
    JMP  decode8_loop

decode8_done:
    // ── Write back bitReader fields ────────────────────────────────────────
    MOVQ br+8(FP), CX
    MOVQ R10, 32(CX)
    MOVB R11, 40(CX)
    MOVQ R9,  24(CX)

    // ── Write back states A–D ─────────────────────────────────────────────
    MOVQ states+16(FP), CX
    MOVL R12, 0(CX)
    MOVL R13, 4(CX)
    MOVL R14, 8(CX)
    MOVL R15, 12(CX)

    // ── Write back states E–H from XMM1 ──────────────────────────────────
    PEXTRD $0, X1, DX
    MOVL   DX, 16(CX)
    PEXTRD $1, X1, DX
    MOVL   DX, 20(CX)
    PEXTRD $2, X1, DX
    MOVL   DX, 24(CX)
    PEXTRD $3, X1, DX
    MOVL   DX, 28(CX)

    // ── Return produced = count − remaining ───────────────────────────────
    MOVQ count+32(FP), AX
    SUBQ DI, AX
    MOVQ AX, ret+40(FP)

    // ── Epilogue: restore callee-saved registers ──────────────────────────
    MOVQ 0(SP),  R12
    MOVQ 8(SP),  R13
    MOVQ 16(SP), R14
    MOVQ 24(SP), R15
    RET
