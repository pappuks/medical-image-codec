//go:build !amd64 && !arm64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// wt53PredictBlocks applies: odd[i] -= (left[i] + right[i]) >> 1
// for i = 0..n-1 on contiguous int32 arrays.
func wt53PredictBlocks(left, right, odd unsafe.Pointer, n int) {
	wt53PredictScalar(left, right, odd, n)
}

// wt53UpdateBlocks applies: even[i] += (dLeft[i] + dRight[i] + 2) >> 2
// for i = 0..n-1 on contiguous int32 arrays.
func wt53UpdateBlocks(dLeft, dRight, even unsafe.Pointer, n int) {
	wt53UpdateScalar(dLeft, dRight, even, n)
}

// wt53InvPredictBlocks: odd[i] += (left[i] + right[i]) >> 1  (inverse predict)
func wt53InvPredictBlocks(left, right, odd unsafe.Pointer, n int) {
	wt53InvPredictScalar(left, right, odd, n)
}

// wt53InvUpdateBlocks: even[i] -= (dLeft[i] + dRight[i] + 2) >> 2  (inverse update)
func wt53InvUpdateBlocks(dLeft, dRight, even unsafe.Pointer, n int) {
	wt53InvUpdateScalar(dLeft, dRight, even, n)
}

// waveletHasAVX2 reports whether the AVX2 path is active on this platform.
func waveletHasAVX2() bool { return false }
