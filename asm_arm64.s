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

// func fse4StateDecompNEON(dt, br, states, out unsafe.Pointer, count int) int
//
// ABI0 stack layout on ARM64 (Go ABI0 passes args at RSP+8, +16, ...):
//   RSP+8  = dt     unsafe.Pointer — *decSymbolU16 base
//   RSP+16 = br     unsafe.Pointer — *bitReader
//   RSP+24 = states unsafe.Pointer — *[4]uint32
//   RSP+32 = out    unsafe.Pointer — *uint16 output
//   RSP+40 = count  int
//   RSP+48 = ret    int            (return value)
//
// bitReader layout:
//   0(br)  = in.ptr   *byte
//   24(br) = off      uint
//   32(br) = value    uint64
//   40(br) = bitsRead uint8
//
// decSymbolU16 layout (8 bytes):
//   0: newState uint32
//   4: symbol   uint16
//   6: nbBits   uint8
//   7: padding
//
// Register allocation:
//   R0  = decTable base (dt)
//   R1  = br.in.ptr
//   R2  = br.off
//   R3  = br.value
//   R4  = br.bitsRead (zero-extended)
//   R5  = sA.state
//   R6  = sB.state
//   R7  = sC.state
//   R8  = sD.state
//   R9  = output pointer (advances)
//   R10 = remaining count
//   R11 = produced count
//   R12 = temp: entry byte offset (state << 3)
//   R13 = temp: nbBits
//   R14 = temp: symbol / lowBits
//   R15 = temp: 64-nbBits, fill scratch
//
TEXT ·fse4StateDecompNEON(SB),NOSPLIT,$0-48
    // Load args from stack (Go ABI0).
    MOVD 8(RSP),  R0    // dt
    MOVD 16(RSP), R1    // br (temp, read fields then freed)
    MOVD 24(RSP), R12   // states pointer (temp)
    MOVD 32(RSP), R9    // out
    MOVD 40(RSP), R10   // count (remaining)

    // Load bitReader fields.
    MOVD 0(R1),  R2    // br.in.ptr (saved for fill)
    MOVD 24(R1), R3    // br.off   -- note: loaded as whole uint, stored back later
    // We need br.off later for writeback, keep it in R3 and also save br ptr.
    // But R1 will be freed after loading. Save br ptr on stack? We can't with $0 frame.
    // Instead, save br ptr in R8 temporarily... but R8 will be sD.state.
    // Solution: load all br fields, reload br ptr from stack at end for writeback.
    MOVD 32(R1), R14   // br.value (temp: load into R14, then move)
    MOVBU 40(R1), R4   // br.bitsRead (uint8, zero-extended)
    // Move value to R3_value register. We need: br.in.ptr=R2, br.off=R3, br.value, bitsRead.
    // Use: R2=in.ptr, R3=value, R4=bitsRead, and need off.
    // Reshuffle: load off separately.
    MOVD 24(R1), R15   // br.off into R15 temporarily
    MOVD R14, R3       // br.value → R3
    MOVD R15, R2       // br.off → R2  (and re-use R15 for in.ptr)
    MOVD 0(R1), R15    // br.in.ptr → R15 (R15 is scratch but we need it persistent)
    // This is getting messy. Let me use different register layout.
    // Reload cleanly:

    // Final register layout for bit reader:
    //   R15 = br.in.ptr
    //   R2  = br.off
    //   R3  = br.value
    //   R4  = br.bitsRead
    MOVD 0(R1),   R15  // br.in.ptr
    MOVD 24(R1),  R2   // br.off
    MOVD 32(R1),  R3   // br.value
    MOVBU 40(R1), R4   // br.bitsRead

    // Load states (R1 was br ptr, now reuse R1 for states ptr for a moment).
    MOVD 24(RSP), R1   // reload states ptr
    MOVWU 0(R1),  R5   // sA.state
    MOVWU 4(R1),  R6   // sB.state
    MOVWU 8(R1),  R7   // sC.state
    MOVWU 12(R1), R8   // sD.state

    // R11 = produced = 0
    MOVD $0, R11

fse4_loop:
    CMP  $4, R10
    BLT  fse4_done
    CMP  $8, R2        // br.off >= 8?
    BLT  fse4_done

    // === fillFast 1: if bitsRead >= 32, load 4 bytes ===
    CMP  $32, R4
    BLT  nf4_1
    SUB  $4, R2, R12   // R12 = off - 4
    MOVWU (R15)(R12),  R13  // R13 = *(uint32 LE)(in + off-4)
    LSL  $32, R3, R3   // value <<= 32
    ORR  R13, R3, R3   // value |= new bytes
    SUB  $32, R4, R4   // bitsRead -= 32
    SUB  $4, R2, R2    // off -= 4
nf4_1:

    // === Symbol A (state in R5) ===
    LSL  $3, R5, R12         // R12 = sA.state * 8 (byte offset)
    ADD  R0, R12, R12
    MOVBU 6(R12),  R13   // R13 = nbBits_A
    MOVHU 4(R12),  R14   // R14 = symbol_A
    MOVH  R14, 0(R9)         // store symbol_A
    LSL R4, R3, R14         // R14 = value << bitsRead  (variable shift)
    ADD  R13, R4, R4          // bitsRead += nbBits_A
    MOVD $64, R1
    SUB  R13, R1, R1          // R1 = 64 - nbBits_A
    LSR R1, R14, R14         // R14 = lowBits_A
    MOVWU (R12), R5      // R5 = newState_A
    ADD  R14, R5, R5          // R5 = newState_A + lowBits_A  (new sA.state)
    // Mask to 32-bit: AND $0xFFFFFFFF, R5 — not needed, newState+lowBits fits in 32-bit

    // === Symbol B (state in R6) ===
    LSL  $3, R6, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 2(R9)
    LSL R4, R3, R14
    ADD  R13, R4, R4
    MOVD $64, R1
    SUB  R13, R1, R1
    LSR R1, R14, R14
    MOVWU (R12), R6
    ADD  R14, R6, R6

    // === fillFast 2 ===
    CMP  $32, R4
    BLT  nf4_2
    SUB  $4, R2, R12
    MOVWU (R15)(R12), R13
    LSL  $32, R3, R3
    ORR  R13, R3, R3
    SUB  $32, R4, R4
    SUB  $4, R2, R2
nf4_2:

    // === Symbol C (state in R7) ===
    LSL  $3, R7, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 4(R9)
    LSL R4, R3, R14
    ADD  R13, R4, R4
    MOVD $64, R1
    SUB  R13, R1, R1
    LSR R1, R14, R14
    MOVWU (R12), R7
    ADD  R14, R7, R7

    // === Symbol D (state in R8) ===
    LSL  $3, R8, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 6(R9)
    LSL R4, R3, R14
    ADD  R13, R4, R4
    MOVD $64, R1
    SUB  R13, R1, R1
    LSR R1, R14, R14
    MOVWU (R12), R8
    ADD  R14, R8, R8

    ADD  $8, R9,  R9    // out += 4 × 2 bytes
    ADD  $4, R11, R11   // produced += 4
    SUB  $4, R10, R10   // remaining -= 4
    B    fse4_loop

fse4_done:
    // Write back bitReader fields.
    MOVD 16(RSP), R1   // reload br ptr
    MOVD R15, 0(R1)    // br.in.ptr (unchanged, but writeback for consistency)
    MOVD R2, 24(R1)    // br.off
    MOVD R3, 32(R1)    // br.value
    MOVB R4, 40(R1)    // br.bitsRead

    // Write back states.
    MOVD 24(RSP), R1   // reload states ptr
    MOVW R5, 0(R1)
    MOVW R6, 4(R1)
    MOVW R7, 8(R1)
    MOVW R8, 12(R1)

    // Return produced.
    MOVD R11, 48(RSP)
    RET


// func rans8StateDecompNEON(dt, br, states, out unsafe.Pointer, count int) int
//
// 8-state rANS decode kernel for ARM64.
//
// ABI0 stack layout ($16 frame, so args are at frame+8, frame+16, ...):
//   (frame=16)
//   24(RSP) = dt      — *decSymbolU16
//   32(RSP) = br      — *bitReader
//   40(RSP) = states  — *[8]uint32
//   48(RSP) = out     — *uint16
//   56(RSP) = count   int
//   64(RSP) = ret     int (return value slot)
//
// Register allocation:
//   R0  = decTable base
//   R1  = scratch / 64-constant
//   R2  = br.off
//   R3  = br.value
//   R4  = br.bitsRead
//   R5  = sA, R6 = sB, R7 = sC, R8 = sD
//   R9  = out pointer
//   R10 = remaining count
//   R11 = produced count
//   R12 = byte offset (state × 8)
//   R13 = nbBits scratch
//   R14 = symbol/lowBits scratch
//   R15 = br.in.ptr
//   R16 = sE  (IP0, caller-saved — no save needed)
//   R17 = sF  (IP1, caller-saved)
//   R19 = sG  (callee-saved — saved in frame)
//   R20 = sH  (callee-saved — saved in frame)
//
TEXT ·rans8StateDecompNEON(SB),NOSPLIT,$16-48
    // Save callee-saved registers in local frame.
    MOVD R19, 0(RSP)
    MOVD R20, 8(RSP)

    // Load args (base offset = frame_size + 8 = 16 + 8 = 24).
    MOVD 24(RSP), R0   // dt
    MOVD 32(RSP), R1   // br (temp)
    MOVD 40(RSP), R2   // states (temp — will reload later)
    MOVD 48(RSP), R9   // out
    MOVD 56(RSP), R10  // count

    // Load bitReader fields.
    MOVD  0(R1),  R15   // br.in.ptr
    MOVD  24(R1), R2    // br.off
    MOVD  32(R1), R3    // br.value
    MOVBU 40(R1), R4    // br.bitsRead

    // Load all 8 states.
    MOVD  40(RSP), R1   // reload states ptr
    MOVWU 0(R1),   R5   // sA
    MOVWU 4(R1),   R6   // sB
    MOVWU 8(R1),   R7   // sC
    MOVWU 12(R1),  R8   // sD
    MOVWU 16(R1),  R16  // sE
    MOVWU 20(R1),  R17  // sF
    MOVWU 24(R1),  R19  // sG
    MOVWU 28(R1),  R20  // sH

    MOVD $0, R11   // produced = 0

rans8_loop:
    CMP $8, R10
    BLT rans8_done
    CMP $16, R2         // need 16 bytes: up to 4 fillFast each consuming 4 bytes
    BLT rans8_done

    // fillFast 1: before symbols A-B.
    CMP $32, R4
    BLT rnf1
    SUB $4, R2, R13
    MOVWU (R15)(R13), R12
    LSL  $32, R3, R3
    ORR  R12, R3, R3
    SUB  $32, R4, R4
    SUB  $4,  R2, R2
rnf1:

    // Symbol A (state in R5)
    LSL   $3, R5, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 0(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R5
    ADD   R14, R5, R5

    // Symbol B (state in R6)
    LSL   $3, R6, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 2(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R6
    ADD   R14, R6, R6

    // fillFast 2: before symbols C-D.
    CMP $32, R4
    BLT rnf2
    SUB $4, R2, R13
    MOVWU (R15)(R13), R12
    LSL  $32, R3, R3
    ORR  R12, R3, R3
    SUB  $32, R4, R4
    SUB  $4,  R2, R2
rnf2:

    // Symbol C (state in R7)
    LSL   $3, R7, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 4(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R7
    ADD   R14, R7, R7

    // Symbol D (state in R8)
    LSL   $3, R8, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 6(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R8
    ADD   R14, R8, R8

    // fillFast 3: before symbols E-F.
    CMP $32, R4
    BLT rnf3
    SUB $4, R2, R13
    MOVWU (R15)(R13), R12
    LSL  $32, R3, R3
    ORR  R12, R3, R3
    SUB  $32, R4, R4
    SUB  $4,  R2, R2
rnf3:

    // Symbol E (state in R16)
    LSL   $3, R16, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 8(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R16
    ADD   R14, R16, R16

    // Symbol F (state in R17)
    LSL   $3, R17, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 10(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R17
    ADD   R14, R17, R17

    // fillFast 4: before symbols G-H.
    CMP $32, R4
    BLT rnf4
    SUB $4, R2, R13
    MOVWU (R15)(R13), R12
    LSL  $32, R3, R3
    ORR  R12, R3, R3
    SUB  $32, R4, R4
    SUB  $4,  R2, R2
rnf4:

    // Symbol G (state in R19)
    LSL   $3, R19, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 12(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R19
    ADD   R14, R19, R19

    // Symbol H (state in R20)
    LSL   $3, R20, R12
    ADD  R0, R12, R12
    MOVBU 6(R12), R13
    MOVHU 4(R12), R14
    MOVH  R14, 14(R9)
    LSL  R4, R3, R14
    ADD   R13, R4, R4
    MOVD  $64, R1
    SUB   R13, R1, R1
    LSR  R1, R14, R14
    MOVWU (R12), R20
    ADD   R14, R20, R20

    ADD $16, R9,  R9    // out += 8 x 2 bytes
    ADD $8,  R11, R11   // produced += 8
    SUB $8,  R10, R10   // remaining -= 8
    B   rans8_loop

rans8_done:
    // Write back bitReader fields.
    MOVD  32(RSP), R1   // reload br ptr
    MOVD  R15, 0(R1)    // br.in.ptr
    MOVD  R2,  24(R1)   // br.off
    MOVD  R3,  32(R1)   // br.value
    MOVB  R4,  40(R1)   // br.bitsRead

    // Write back all 8 states.
    MOVD 40(RSP), R1
    MOVW R5,  0(R1)
    MOVW R6,  4(R1)
    MOVW R7,  8(R1)
    MOVW R8,  12(R1)
    MOVW R16, 16(R1)
    MOVW R17, 20(R1)
    MOVW R19, 24(R1)
    MOVW R20, 28(R1)

    // Return produced count.
    MOVD R11, 64(RSP)

    // Restore callee-saved registers.
    MOVD 0(RSP), R19
    MOVD 8(RSP), R20
    RET
