// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// func countSimpleU16Asm(in unsafe.Pointer, inLen int, count, count2 unsafe.Pointer)
// Args (Go ABI0 stack layout — caller stores args to RSP+8..32 before CALL):
//   RSP+8  = in       *uint16
//   RSP+16 = inLen    int
//   RSP+24 = count    *uint32  [65536]
//   RSP+32 = count2   *uint32  [65536]
//
// 4-way unrolled: elements 0,2 → count[], elements 1,3 → count2[].
// This breaks store-to-load forwarding stalls when consecutive pixels share
// the same or adjacent values (common after delta coding).
TEXT ·countSimpleU16Asm(SB),NOSPLIT,$0-32
    // Load args from stack (Go ABI0 on ARM64 passes via RSP offsets).
    MOVD 8(RSP), R0          // in
    MOVD 16(RSP), R1         // inLen
    MOVD 24(RSP), R2         // count
    MOVD 32(RSP), R3         // count2
    CBZ R1, done_hist        // len == 0 → skip

    // R4 = number of 4-element groups
    LSR $2, R1, R4
    CBZ R4, tail_hist

loop4_hist:
    // Load 4 consecutive uint16 values
    MOVHU 0(R0), R5          // v0  (even → count)
    MOVHU 2(R0), R6          // v1  (odd  → count2)
    MOVHU 4(R0), R7          // v2  (even → count)
    MOVHU 6(R0), R8          // v3  (odd  → count2)

    // count[v0]++
    LSL $2, R5, R9           // byte offset = v0 * 4
    MOVWU (R2)(R9), R10
    ADD $1, R10, R10
    MOVW R10, (R2)(R9)

    // count2[v1]++
    LSL $2, R6, R9
    MOVWU (R3)(R9), R10
    ADD $1, R10, R10
    MOVW R10, (R3)(R9)

    // count[v2]++
    LSL $2, R7, R9
    MOVWU (R2)(R9), R10
    ADD $1, R10, R10
    MOVW R10, (R2)(R9)

    // count2[v3]++
    LSL $2, R8, R9
    MOVWU (R3)(R9), R10
    ADD $1, R10, R10
    MOVW R10, (R3)(R9)

    ADD $8, R0, R0           // advance pointer by 4 × 2 bytes
    SUB $1, R4, R4
    CBNZ R4, loop4_hist

tail_hist:
    AND $3, R1, R1           // remaining = len & 3
    CBZ R1, done_hist

tail_loop_hist:
    MOVHU 0(R0), R5          // load one uint16
    LSL $2, R5, R9
    MOVWU (R2)(R9), R10
    ADD $1, R10, R10
    MOVW R10, (R2)(R9)
    ADD $2, R0, R0
    SUB $1, R1, R1
    CBNZ R1, tail_loop_hist

done_hist:
    RET

// func ycocgRForwardNEON(rgb unsafe.Pointer, n int, y, co, cg unsafe.Pointer)
// Scalar pixel-by-pixel YCoCg-R forward transform.
// Per pixel: Co=R-B, t=B+(Co>>1), Cg=G-t, Y=t+(Cg>>1), ZigZag(co,cg).
//
// Args (Go ABI0 stack layout — caller stores args to RSP+8..40 before CALL):
//   RSP+8  = rgb  unsafe.Pointer
//   RSP+16 = n    int
//   RSP+24 = y    unsafe.Pointer
//   RSP+32 = co   unsafe.Pointer
//   RSP+40 = cg   unsafe.Pointer
//
// Register allocation:
//   R0=rgb, R1=n, R2=y, R3=co, R4=cg
//   R5=i(byte offset into rgb), R6=R, R7=G, R8=B
//   R9=Co, R10=t, R11=Cg, R12=Y
//   R13=scratch, R14=loop counter
TEXT ·ycocgRForwardNEON(SB),NOSPLIT,$0-40
    // Load args from stack (Go ABI0 on ARM64 passes via RSP offsets).
    MOVD 8(RSP), R0          // rgb
    MOVD 16(RSP), R1         // n
    MOVD 24(RSP), R2         // y
    MOVD 32(RSP), R3         // co
    MOVD 40(RSP), R4         // cg
    CBZ R1, fwd_done

    MOVD $0, R14             // i = 0

fwd_loop:
    // Compute byte offset R5 = i * 3
    ADD R14, R14, R5
    ADD R14, R5, R5          // R5 = i*3

    // Load R, G, B
    MOVBU (R0)(R5), R6       // R
    ADD $1, R5, R13
    MOVBU (R0)(R13), R7      // G
    ADD $2, R5, R13
    MOVBU (R0)(R13), R8      // B

    // Co = R - B
    SUB R8, R6, R9           // R9 = Co (signed 32-bit in 64-bit reg)

    // t = B + (Co >> 1)  (arithmetic right shift)
    ASR $1, R9, R13
    ADD R8, R13, R10         // R10 = t

    // Cg = G - t
    SUB R10, R7, R11         // R11 = Cg

    // Y = t + (Cg >> 1)
    ASR $1, R11, R13
    ADD R10, R13, R12        // R12 = Y

    // Store Y[i] as uint16
    LSL $1, R14, R13         // byte offset = i * 2
    MOVH R12, (R2)(R13)

    // ZigZag(Co) = (Co<<1) ^ (Co>>15)
    LSL $1, R9, R13
    ASR $15, R9, R5
    EOR R5, R13, R13
    LSL $1, R14, R5
    MOVH R13, (R3)(R5)

    // ZigZag(Cg) = (Cg<<1) ^ (Cg>>15)
    LSL $1, R11, R13
    ASR $15, R11, R5
    EOR R5, R13, R13
    LSL $1, R14, R5
    MOVH R13, (R4)(R5)

    ADD $1, R14, R14
    CMP R1, R14
    BLT fwd_loop

fwd_done:
    RET

// func ycocgRInverseNEON(y, co, cg unsafe.Pointer, n int, rgb unsafe.Pointer)
// Scalar pixel-by-pixel YCoCg-R inverse transform.
// Per pixel: unzigzag Co,Cg; t=Y-(Cg>>1); G=Cg+t; B=t-(Co>>1); R=Co+B.
//
// Args (Go ABI0 stack layout — caller stores args to RSP+8..40 before CALL):
//   RSP+8  = y    unsafe.Pointer
//   RSP+16 = co   unsafe.Pointer
//   RSP+24 = cg   unsafe.Pointer
//   RSP+32 = n    int
//   RSP+40 = rgb  unsafe.Pointer
//
// Register allocation:
//   R0=y, R1=co, R2=cg, R3=n, R4=rgb
//   R5=i, R6=Y, R7=co_zz, R8=cg_zz
//   R9=Co(unzz), R10=Cg(unzz), R11=t, R12=G, R13=B, R14=R, R15=scratch
TEXT ·ycocgRInverseNEON(SB),NOSPLIT,$0-40
    // Load args from stack (Go ABI0 on ARM64 passes via RSP offsets).
    MOVD 8(RSP), R0          // y
    MOVD 16(RSP), R1         // co
    MOVD 24(RSP), R2         // cg
    MOVD 32(RSP), R3         // n
    MOVD 40(RSP), R4         // rgb
    CBZ R3, inv_done

    MOVD $0, R5              // i = 0

inv_loop:
    LSL $1, R5, R15          // byte offset = i * 2

    // Load Y, co_zigzag, cg_zigzag
    MOVHU (R0)(R15), R6      // Y
    MOVHU (R1)(R15), R7      // co zigzag
    MOVHU (R2)(R15), R8      // cg zigzag

    // UnZigZag(v) = (v>>1) ^ -(v&1)
    // Co = (co_zz >> 1) ^ -(co_zz & 1)
    LSR $1, R7, R9           // R9 = co_zz >> 1
    AND $1, R7, R15
    NEG R15, R15             // R15 = -(co_zz & 1)
    EOR R15, R9, R9          // R9 = Co (sign-extended in 64-bit)
    // Sign-extend from 16-bit result: the original values are ≤16-bit signed
    SXTW R9, R9

    // Cg = (cg_zz >> 1) ^ -(cg_zz & 1)
    LSR $1, R8, R10
    AND $1, R8, R15
    NEG R15, R15
    EOR R15, R10, R10
    SXTW R10, R10

    // t = Y - (Cg >> 1)
    ASR $1, R10, R15
    SUB R15, R6, R11         // R11 = t

    // G = Cg + t
    ADD R10, R11, R12        // R12 = G

    // B = t - (Co >> 1)
    ASR $1, R9, R15
    SUB R15, R11, R13        // R13 = B

    // R = Co + B
    ADD R9, R13, R14         // R14 = R

    // Store R, G, B at rgb[i*3]
    ADD R5, R5, R15
    ADD R5, R15, R15         // R15 = i*3
    MOVB R14, (R4)(R15)
    ADD $1, R15, R15
    MOVB R12, (R4)(R15)
    ADD $1, R15, R15
    MOVB R13, (R4)(R15)

    ADD $1, R5, R5
    CMP R3, R5
    BLT inv_loop

inv_done:
    RET
