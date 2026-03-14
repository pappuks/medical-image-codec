//go:build amd64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// rans8StateDecompKernel is the AMD64 assembly hot loop for 8-state rANS decode.
//
// Unlike tANS, rANS states are pure slot indices (x ∈ [0, tableSize)).
// The decode step is identical to tANS: state = dt[state].newState + readBits(nbBits).
// The kernel processes 8 independent lanes per iteration, giving 8× ILP.
//
// AVX2 SIMD is used for:
//   - Parallel gather of nbBits (from the 8 independent decTable entries)
//   - Parallel gather of symbols (8 uint16 from decTable)
//   - Batch newState loads (8 × uint32 VPGATHERDD)
//
// BMI2 SHLXQ/SHRXQ handles variable-count bit extraction without using CL.
// Only valid for the non-zeroBits path (all nbBits > 0).
// Returns the number of symbols written to out (0 if AVX2 not available).
//
//go:noescape
func rans8StateDecompKernel(dt, br, states, out unsafe.Pointer, count int) int

// rans8StateDecompNative dispatches to the AVX2 assembly kernel on amd64.
func rans8StateDecompNative(dt, br, states, out unsafe.Pointer, count int) int {
	if cpuHasAVX2 {
		return rans8StateDecompKernel(dt, br, states, out, count)
	}
	return 0
}
