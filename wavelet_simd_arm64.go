//go:build arm64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// ARM64: use scalar fallback; blocked column layout still improves cache use.

func wt53PredictBlocks(left, right, odd unsafe.Pointer, n int) {
	wt53PredictScalar(left, right, odd, n)
}

func wt53UpdateBlocks(dLeft, dRight, even unsafe.Pointer, n int) {
	wt53UpdateScalar(dLeft, dRight, even, n)
}

func wt53InvPredictBlocks(left, right, odd unsafe.Pointer, n int) {
	wt53InvPredictScalar(left, right, odd, n)
}

func wt53InvUpdateBlocks(dLeft, dRight, even unsafe.Pointer, n int) {
	wt53InvUpdateScalar(dLeft, dRight, even, n)
}

func waveletHasAVX2() bool { return false }
