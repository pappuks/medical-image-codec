//go:build amd64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// wt53PredictAVX2 is the AMD64 AVX2 kernel.
// Computes: odd[i] -= (left[i] + right[i]) >> 1  for i = 0..n-1
// n must be a multiple of 8; caller handles the remainder.
//
//go:noescape
func wt53PredictAVX2(left, right, odd unsafe.Pointer, n int)

// wt53UpdateAVX2 is the AMD64 AVX2 kernel.
// Computes: even[i] += (dLeft[i] + dRight[i] + 2) >> 2  for i = 0..n-1
// n must be a multiple of 8; caller handles the remainder.
//
//go:noescape
func wt53UpdateAVX2(dLeft, dRight, even unsafe.Pointer, n int)

// wt53PredictBlocks dispatches to AVX2 for the aligned interior, scalar for tail.
func wt53PredictBlocks(left, right, odd unsafe.Pointer, n int) {
	n8 := n &^ 7 // round down to multiple of 8
	if n8 > 0 && cpuHasAVX2 {
		wt53PredictAVX2(left, right, odd, n8)
	} else {
		n8 = 0
	}
	if n8 < n {
		wt53PredictScalar(
			unsafe.Add(left, n8*4),
			unsafe.Add(right, n8*4),
			unsafe.Add(odd, n8*4),
			n-n8,
		)
	}
}

// wt53UpdateBlocks dispatches to AVX2 for the aligned interior, scalar for tail.
func wt53UpdateBlocks(dLeft, dRight, even unsafe.Pointer, n int) {
	n8 := n &^ 7
	if n8 > 0 && cpuHasAVX2 {
		wt53UpdateAVX2(dLeft, dRight, even, n8)
	} else {
		n8 = 0
	}
	if n8 < n {
		wt53UpdateScalar(
			unsafe.Add(dLeft, n8*4),
			unsafe.Add(dRight, n8*4),
			unsafe.Add(even, n8*4),
			n-n8,
		)
	}
}

// wt53InvPredictAVX2 is the inverse predict AVX2 kernel.
// Computes: odd[i] += (left[i] + right[i]) >> 1  for i = 0..n-1
// n must be a multiple of 8.
//
//go:noescape
func wt53InvPredictAVX2(left, right, odd unsafe.Pointer, n int)

// wt53InvUpdateAVX2 is the inverse update AVX2 kernel.
// Computes: even[i] -= (dLeft[i] + dRight[i] + 2) >> 2  for i = 0..n-1
// n must be a multiple of 8.
//
//go:noescape
func wt53InvUpdateAVX2(dLeft, dRight, even unsafe.Pointer, n int)

// wt53InvPredictBlocks dispatches to AVX2 inverse predict.
func wt53InvPredictBlocks(left, right, odd unsafe.Pointer, n int) {
	n8 := n &^ 7
	if n8 > 0 && cpuHasAVX2 {
		wt53InvPredictAVX2(left, right, odd, n8)
	} else {
		n8 = 0
	}
	if n8 < n {
		wt53InvPredictScalar(
			unsafe.Add(left, n8*4),
			unsafe.Add(right, n8*4),
			unsafe.Add(odd, n8*4),
			n-n8,
		)
	}
}

// wt53InvUpdateBlocks dispatches to AVX2 inverse update.
func wt53InvUpdateBlocks(dLeft, dRight, even unsafe.Pointer, n int) {
	n8 := n &^ 7
	if n8 > 0 && cpuHasAVX2 {
		wt53InvUpdateAVX2(dLeft, dRight, even, n8)
	} else {
		n8 = 0
	}
	if n8 < n {
		wt53InvUpdateScalar(
			unsafe.Add(dLeft, n8*4),
			unsafe.Add(dRight, n8*4),
			unsafe.Add(even, n8*4),
			n-n8,
		)
	}
}

// waveletHasAVX2 reports whether AVX2 is available on this CPU.
func waveletHasAVX2() bool { return cpuHasAVX2 }
