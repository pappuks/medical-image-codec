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
TEXT ·countSimpleU16Asm(SB),NOSPLIT,$0-40
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
