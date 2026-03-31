// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// func cpuidAMD64(leaf, subleaf uint32) (eax, ebx, ecx, edx uint32)
// Stack layout (Plan 9 ABI0, args grow from lower FP address):
//   leaf    uint32  : +0(FP)
//   subleaf uint32  : +4(FP)
//   eax     uint32  : +8(FP)
//   ebx     uint32  : +12(FP)
//   ecx     uint32  : +16(FP)
//   edx     uint32  : +20(FP)
TEXT ·cpuidAMD64(SB),NOSPLIT,$0-24
    MOVL leaf+0(FP), AX
    MOVL subleaf+4(FP), CX
    CPUID
    MOVL AX, eax+8(FP)
    MOVL BX, ebx+12(FP)
    MOVL CX, ecx+16(FP)
    MOVL DX, edx+20(FP)
    RET

// func countSimpleU16Asm(in unsafe.Pointer, inLen int, count, count2 unsafe.Pointer)
// Builds two interleaved histograms of uint16 values to reduce store-to-load stalls.
// Uses a 4-way unrolled loop: even-indexed elements go to count[], odd go to count2[].
TEXT ·countSimpleU16Asm(SB),NOSPLIT,$0-32
    MOVQ in+0(FP),   SI   // *uint16 input
    MOVQ inLen+8(FP), CX  // element count
    MOVQ count+16(FP), DI // *uint32 count[65536]
    MOVQ count2+24(FP), R8 // *uint32 count2[65536]

    TESTQ CX, CX
    JE   done_hist

    // 4-way unrolled loop: process 4 elements per iteration (2 to count, 2 to count2)
    MOVQ CX, R9
    SHRQ $2, R9       // R9 = CX / 4 (number of 4-element groups)
    TESTQ R9, R9
    JE   tail_hist

loop4_hist:
    MOVWQZX 0(SI), AX
    MOVWQZX 2(SI), BX
    MOVWQZX 4(SI), R11
    MOVWQZX 6(SI), R12
    ADDL $1, (DI)(AX*4)
    ADDL $1, (R8)(BX*4)
    ADDL $1, (DI)(R11*4)
    ADDL $1, (R8)(R12*4)
    ADDQ $8, SI
    DECQ R9
    JNZ loop4_hist

tail_hist:
    ANDQ $3, CX
    JE   done_hist

tail_loop_hist:
    MOVWQZX 0(SI), AX
    ADDL $1, (DI)(AX*4)
    ADDQ $2, SI
    DECQ CX
    JNZ tail_loop_hist

done_hist:
    RET

// func ycocgRForwardSSSE3(rgb unsafe.Pointer, n int, y, co, cg unsafe.Pointer)
// YCoCg-R forward transform, 1 pixel per iteration using SSE2 arithmetic.
// Per pixel: Co=R-B, t=B+(Co>>1), Cg=G-t, Y=t+(Cg>>1), ZigZag co and cg.
TEXT ·ycocgRForwardSSSE3(SB),NOSPLIT,$0-40
    MOVQ rgb+0(FP), SI   // byte* rgb
    MOVQ n+8(FP),   CX   // pixel count
    MOVQ y+16(FP),  R8   // uint16* y
    MOVQ co+24(FP), R9   // uint16* co
    MOVQ cg+32(FP), R10  // uint16* cg

    TESTQ CX, CX
    JE fwd_done

    MOVQ CX, R11   // pixel index
    XORQ R11, R11  // i = 0

fwd_loop:
    // Load R, G, B as 16-bit signed into XMM registers via scalar path.
    // rgb[i*3], rgb[i*3+1], rgb[i*3+2]
    MOVQ R11, R12
    IMULQ $3, R12       // R12 = i*3

    MOVBLZX 0(SI)(R12*1), AX  // R
    MOVBLZX 1(SI)(R12*1), BX  // G
    MOVBLZX 2(SI)(R12*1), DX  // B

    // Co = R - B
    MOVL AX, R13
    SUBL DX, R13        // R13 = Co

    // t = B + (Co >> 1)   (arithmetic right shift)
    MOVL R13, R14
    SARL $1, R14
    ADDL DX, R14        // R14 = t

    // Cg = G - t
    MOVL BX, R15
    SUBL R14, R15       // R15 = Cg

    // Y = t + (Cg >> 1)
    MOVL R15, AX
    SARL $1, AX
    ADDL R14, AX        // AX = Y

    // Store Y[i]
    MOVW AX, 0(R8)(R11*2)

    // ZigZag(Co): (Co<<1) ^ (Co>>15)
    MOVL R13, AX
    SHLL $1, AX
    MOVL R13, DX
    SARL $15, DX
    XORL DX, AX
    MOVW AX, 0(R9)(R11*2)

    // ZigZag(Cg): (Cg<<1) ^ (Cg>>15)
    MOVL R15, AX
    SHLL $1, AX
    MOVL R15, DX
    SARL $15, DX
    XORL DX, AX
    MOVW AX, 0(R10)(R11*2)

    INCQ R11
    CMPQ R11, CX
    JL fwd_loop

fwd_done:
    RET

// func ycocgRInverseSSSE3(y, co, cg unsafe.Pointer, n int, rgb unsafe.Pointer)
// YCoCg-R inverse transform, 1 pixel per iteration.
TEXT ·ycocgRInverseSSSE3(SB),NOSPLIT,$0-40
    MOVQ y+0(FP),   SI   // uint16* y
    MOVQ co+8(FP),  R8   // uint16* co
    MOVQ cg+16(FP), R9   // uint16* cg
    MOVQ n+24(FP),  CX   // pixel count
    MOVQ rgb+32(FP), DI  // byte* rgb

    TESTQ CX, CX
    JE inv_done

    XORQ R11, R11  // i = 0

inv_loop:
    // Load Y, unzigzag Co and Cg
    MOVWQZX 0(SI)(R11*2), AX  // Y
    MOVWQZX 0(R8)(R11*2), BX  // co zigzag
    MOVWQZX 0(R9)(R11*2), DX  // cg zigzag

    // UnZigZag(v): (v>>1) ^ -(v&1)
    // Co
    MOVL BX, R13
    SHRL $1, R13        // R13 = BX >> 1
    MOVL BX, R14
    ANDL $1, R14
    NEGL R14            // R14 = -(BX & 1)
    XORL R14, R13       // R13 = Co (signed)

    // Cg
    MOVL DX, R15
    SHRL $1, R15
    MOVL DX, R12
    ANDL $1, R12
    NEGL R12
    XORL R12, R15       // R15 = Cg (signed)

    // Sign-extend to 64-bit for arithmetic
    MOVLQSX R13, R13    // Co
    MOVLQSX R15, R15    // Cg
    MOVWQZX AX, AX      // Y (unsigned 0..255)

    // t = Y - (Cg >> 1)
    MOVQ R15, R12
    SARQ $1, R12
    MOVQ AX, R14
    SUBQ R12, R14       // R14 = t

    // G = Cg + t
    MOVQ R15, R12
    ADDQ R14, R12       // R12 = G

    // B = t - (Co >> 1)
    MOVQ R13, AX
    SARQ $1, AX
    MOVQ R14, BX
    SUBQ AX, BX         // BX = B

    // R = Co + B
    ADDQ R13, BX        // BX = R ... wait, R = Co + B
    // Let me redo: R = Co + B, but B is in BX which I just modified. Let me use different regs.
    // Redo more carefully:
    // At this point: R14=t, R12=G, R13=Co, R15=Cg, Y in AX (but AX was overwritten)
    // Let me recompute:
    // B = t - (Co>>1): t=R14, Co=R13
    // R = Co + B

    // Actually the issue is register reuse. Let me redo from t:
    // t = R14
    // G = R15 + R14 (Cg + t) -> already in R12
    // B = R14 - (R13>>1)
    MOVQ R13, DX
    SARQ $1, DX
    MOVQ R14, BX
    SUBQ DX, BX     // BX = B

    // R = R13 + BX  (Co + B)
    MOVQ R13, AX
    ADDQ BX, AX     // AX = R

    // Store R, G, B
    MOVQ R11, R10
    IMULQ $3, R10   // R10 = i*3
    MOVB AX, 0(DI)(R10*1)
    MOVB R12, 1(DI)(R10*1)
    MOVB BX, 2(DI)(R10*1)

    INCQ R11
    CMPQ R11, CX
    JL inv_loop

inv_done:
    RET

// func fse4StateDecompKernel(dt, br, states, out unsafe.Pointer, count int) int
//
// ABI0 stack layout (Go calls assembly via stack):
//   dt+0(FP)     unsafe.Pointer  (8 bytes) — *decSymbolU16 base
//   br+8(FP)     unsafe.Pointer  (8 bytes) — *bitReader
//   states+16(FP) unsafe.Pointer (8 bytes) — *[4]uint32
//   out+24(FP)   unsafe.Pointer  (8 bytes) — *uint16 output buffer
//   count+32(FP) int             (8 bytes) — symbols to decode
//   ret+40(FP)   int             (8 bytes) — return: symbols written
//
// bitReader layout (offsets from br pointer):
//   0(br)  = in.ptr   *byte
//   24(br) = off      uint    (next byte: in[off-1])
//   32(br) = value    uint64
//   40(br) = bitsRead uint8
//
// states layout: [sA, sB, sC, sD] uint32 × 4
//
// decSymbolU16 layout (8 bytes each):
//   0: newState uint32
//   4: symbol   uint16
//   6: nbBits   uint8
//   7: padding
//
// Register allocation during loop:
//   AX  = decTable base
//   BX  = output pointer (advances)
//   CX  = temp: nbBits, shift counts
//   DX  = temp: symbol, lowBits, fill scratch
//   DI  = remaining count
//   SI  = produced count (returned)
//   R8  = br.in.ptr
//   R9  = br.off
//   R10 = br.value
//   R11 = br.bitsRead (zero-extended uint8 → uint64)
//   R12 = sA.state  (callee-saved)
//   R13 = sB.state  (callee-saved)
//   R14 = sC.state  (callee-saved)
//   R15 = sD.state  (callee-saved)
//
// Uses BMI2 SHLXQ/SHRXQ for variable-shift bit extraction without needing CL.
// Safe to use when cpuHasAVX2=true (Haswell+ implies BMI2).
//
TEXT ·fse4StateDecompKernel(SB),NOSPLIT,$32-48
    // Save callee-saved registers.
    MOVQ R12, 0(SP)
    MOVQ R13, 8(SP)
    MOVQ R14, 16(SP)
    MOVQ R15, 24(SP)

    // Load args.
    MOVQ dt+0(FP),     AX   // decTable base
    MOVQ br+8(FP),     DX   // br pointer (temporary, then freed)
    MOVQ states+16(FP), CX  // states pointer (temporary)
    MOVQ out+24(FP),   BX   // output pointer
    MOVQ count+32(FP), DI   // count (remaining)

    // Load bitReader fields from DX (br pointer).
    MOVQ 0(DX),  R8    // br.in.ptr
    MOVQ 24(DX), R9    // br.off
    MOVQ 32(DX), R10   // br.value
    MOVBLZX 40(DX), R11 // br.bitsRead (byte → 32-bit ZX, upper 32 auto-zeroed)

    // Load states from CX.
    MOVL 0(CX),  R12   // sA.state
    MOVL 4(CX),  R13   // sB.state
    MOVL 8(CX),  R14   // sC.state
    MOVL 12(CX), R15   // sD.state

    // SI = 0 (produced count)
    XORQ SI, SI

decode4_loop:
    CMPQ DI, $4
    JL   decode4_done
    CMPQ R9, $8
    JL   decode4_done

    // === fillFast 1: ensure 32+ bits available ===
    CMPQ R11, $32
    JL   nf1
    MOVL -4(R8)(R9*1), DX  // DX = *(uint32 LE)(in + off - 4)
    SHLQ $32, R10           // value <<= 32
    MOVLQZX DX, DX          // zero-extend DX to 64-bit
    ORQ  DX, R10            // value |= new low 32 bits
    SUBQ $32, R11           // bitsRead -= 32
    SUBQ $4, R9             // off -= 4
nf1:

    // === Symbol A (state in R12) ===
    MOVBLZX 6(AX)(R12*8), CX   // CX = nbBits_A
    MOVWQZX 4(AX)(R12*8), DX   // DX = symbol_A
    MOVW    DX, 0(BX)           // store symbol_A to output
    SHLXQ  R11, R10, DX         // DX = value << bitsRead  (BMI2)
    ADDQ    CX, R11             // bitsRead += nbBits_A
    NEGQ    CX
    ADDQ    $64, CX             // CX = 64 - nbBits_A
    SHRXQ  CX, DX, DX           // DX = lowBits_A  (BMI2)
    MOVL    0(AX)(R12*8), R12   // R12 = newState_A (32-bit, auto ZX to 64)
    ADDL    DX, R12             // R12 = new sA.state

    // === Symbol B (state in R13) ===
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

    // === fillFast 2 ===
    CMPQ R11, $32
    JL   nf2
    MOVL -4(R8)(R9*1), DX
    SHLQ $32, R10
    MOVLQZX DX, DX
    ORQ  DX, R10
    SUBQ $32, R11
    SUBQ $4, R9
nf2:

    // === Symbol C (state in R14) ===
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

    // === Symbol D (state in R15) ===
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

    ADDQ $8, BX   // output pointer += 4 symbols × 2 bytes
    ADDQ $4, SI   // produced += 4
    SUBQ $4, DI   // remaining -= 4
    JMP  decode4_loop

decode4_done:
    // Write back bitReader fields.
    MOVQ br+8(FP), CX
    MOVQ R10, 32(CX)   // br.value
    MOVB R11, 40(CX)   // br.bitsRead
    MOVQ R9, 24(CX)    // br.off

    // Write back states.
    MOVQ states+16(FP), CX
    MOVL R12, 0(CX)
    MOVL R13, 4(CX)
    MOVL R14, 8(CX)
    MOVL R15, 12(CX)

    // Return produced count.
    MOVQ SI, ret+40(FP)

    // Restore callee-saved registers.
    MOVQ 0(SP), R12
    MOVQ 8(SP), R13
    MOVQ 16(SP), R14
    MOVQ 24(SP), R15
    RET
