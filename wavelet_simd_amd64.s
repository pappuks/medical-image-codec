// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

#include "textflag.h"

// func wt53PredictAVX2(left, right, odd unsafe.Pointer, n int)
//
// Computes: odd[i] -= (left[i] + right[i]) >> 1  for i = 0..n-1
// where left, right, odd are contiguous []int32 arrays.
// n must be a multiple of 8 (caller rounds down; remainder handled in Go).
//
// ABI0 stack layout:
//   left+0(FP)   unsafe.Pointer  (8 bytes)
//   right+8(FP)  unsafe.Pointer  (8 bytes)
//   odd+16(FP)   unsafe.Pointer  (8 bytes)
//   n+24(FP)     int             (8 bytes)
//
// Register allocation:
//   SI = left pointer
//   DI = right pointer
//   BX = odd pointer
//   CX = iteration count (n/8)
//   Y0 = left[i..i+7]
//   Y1 = right[i..i+7]
//   Y2 = odd[i..i+7]
//   Y3 = scratch: left+right, then >>1
TEXT ·wt53PredictAVX2(SB),NOSPLIT,$0-32
    MOVQ left+0(FP),  SI
    MOVQ right+8(FP), DI
    MOVQ odd+16(FP),  BX
    MOVQ n+24(FP),    CX
    SHRQ $3, CX              // CX = n / 8
    TESTQ CX, CX
    JE   wt53pred_done

wt53pred_loop:
    VMOVDQU (SI), Y0          // Y0 = left[i..i+7]   (8 × int32)
    VMOVDQU (DI), Y1          // Y1 = right[i..i+7]
    VMOVDQU (BX), Y2          // Y2 = odd[i..i+7]
    VPADDD  Y1, Y0, Y3        // Y3 = left + right
    VPSRAD  $1, Y3, Y3        // Y3 = (left + right) >> 1  (arithmetic shift)
    VPSUBD  Y3, Y2, Y2        // Y2 = odd - Y3
    VMOVDQU Y2, (BX)          // store updated odd
    ADDQ    $32, SI           // advance pointers by 8 × 4 bytes
    ADDQ    $32, DI
    ADDQ    $32, BX
    DECQ    CX
    JNZ     wt53pred_loop

wt53pred_done:
    VZEROUPPER
    RET


// func wt53UpdateAVX2(dLeft, dRight, even unsafe.Pointer, n int)
//
// Computes: even[i] += (dLeft[i] + dRight[i] + 2) >> 2  for i = 0..n-1
// n must be a multiple of 8.
//
// ABI0 stack layout:
//   dLeft+0(FP)   unsafe.Pointer  (8 bytes)
//   dRight+8(FP)  unsafe.Pointer  (8 bytes)
//   even+16(FP)   unsafe.Pointer  (8 bytes)
//   n+24(FP)      int             (8 bytes)
//
// Register allocation:
//   SI = dLeft pointer
//   DI = dRight pointer
//   BX = even pointer
//   CX = iteration count (n/8)
//   Y0 = dLeft[i..i+7]
//   Y1 = dRight[i..i+7]
//   Y2 = even[i..i+7]
//   Y3 = scratch
//   Y4 = constant {2, 2, 2, 2, 2, 2, 2, 2}
TEXT ·wt53UpdateAVX2(SB),NOSPLIT,$0-32
    MOVQ dLeft+0(FP),  SI
    MOVQ dRight+8(FP), DI
    MOVQ even+16(FP),  BX
    MOVQ n+24(FP),     CX
    SHRQ $3, CX
    TESTQ CX, CX
    JE   wt53upd_done

    // Broadcast constant 2 into Y4
    MOVL      $2, AX
    VMOVD     AX, X4
    VPBROADCASTD X4, Y4       // Y4 = {2, 2, 2, 2, 2, 2, 2, 2}

wt53upd_loop:
    VMOVDQU (SI), Y0           // Y0 = dLeft[i..i+7]
    VMOVDQU (DI), Y1           // Y1 = dRight[i..i+7]
    VMOVDQU (BX), Y2           // Y2 = even[i..i+7]
    VPADDD  Y1, Y0, Y3         // Y3 = dLeft + dRight
    VPADDD  Y4, Y3, Y3         // Y3 += 2
    VPSRAD  $2, Y3, Y3         // Y3 >>= 2  (arithmetic)
    VPADDD  Y3, Y2, Y2         // Y2 = even + Y3
    VMOVDQU Y2, (BX)           // store updated even
    ADDQ    $32, SI
    ADDQ    $32, DI
    ADDQ    $32, BX
    DECQ    CX
    JNZ     wt53upd_loop

wt53upd_done:
    VZEROUPPER
    RET


// func wt53InvPredictAVX2(left, right, odd unsafe.Pointer, n int)
// Computes: odd[i] += (left[i] + right[i]) >> 1  (inverse predict, add)
TEXT ·wt53InvPredictAVX2(SB),NOSPLIT,$0-32
    MOVQ left+0(FP),  SI
    MOVQ right+8(FP), DI
    MOVQ odd+16(FP),  BX
    MOVQ n+24(FP),    CX
    SHRQ $3, CX
    TESTQ CX, CX
    JE   wt53invpred_done

wt53invpred_loop:
    VMOVDQU (SI), Y0
    VMOVDQU (DI), Y1
    VMOVDQU (BX), Y2
    VPADDD  Y1, Y0, Y3
    VPSRAD  $1, Y3, Y3
    VPADDD  Y3, Y2, Y2        // add instead of subtract
    VMOVDQU Y2, (BX)
    ADDQ    $32, SI
    ADDQ    $32, DI
    ADDQ    $32, BX
    DECQ    CX
    JNZ     wt53invpred_loop

wt53invpred_done:
    VZEROUPPER
    RET


// func wt53InvUpdateAVX2(dLeft, dRight, even unsafe.Pointer, n int)
// Computes: even[i] -= (dLeft[i] + dRight[i] + 2) >> 2  (inverse update, subtract)
TEXT ·wt53InvUpdateAVX2(SB),NOSPLIT,$0-32
    MOVQ dLeft+0(FP),  SI
    MOVQ dRight+8(FP), DI
    MOVQ even+16(FP),  BX
    MOVQ n+24(FP),     CX
    SHRQ $3, CX
    TESTQ CX, CX
    JE   wt53invupd_done

    MOVL         $2, AX
    VMOVD        AX, X4
    VPBROADCASTD X4, Y4

wt53invupd_loop:
    VMOVDQU (SI), Y0
    VMOVDQU (DI), Y1
    VMOVDQU (BX), Y2
    VPADDD  Y1, Y0, Y3
    VPADDD  Y4, Y3, Y3
    VPSRAD  $2, Y3, Y3
    VPSUBD  Y3, Y2, Y2        // subtract instead of add
    VMOVDQU Y2, (BX)
    ADDQ    $32, SI
    ADDQ    $32, DI
    ADDQ    $32, BX
    DECQ    CX
    JNZ     wt53invupd_loop

wt53invupd_done:
    VZEROUPPER
    RET
