//go:build arm64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// fse4StateDecompNEON is the ARM64 assembly hot loop for 4-state FSE decode.
// NEON is always present on ARM64, so this is always dispatched.
// Only valid for the non-zeroBits path (all nbBits > 0).
// Returns the number of symbols written to out.
//
//go:noescape
func fse4StateDecompNEON(dt, br, states, out unsafe.Pointer, count int) int

// fse4StateDecompNative dispatches to the ARM64 assembly kernel.
// NEON/advanced SIMD is mandatory on ARM64.
func fse4StateDecompNative(dt, br, states, out unsafe.Pointer, count int) int {
	return fse4StateDecompNEON(dt, br, states, out, count)
}

// countSimpleU16Asm is implemented in asm_arm64.s.
// NEON is always available on ARM64 — no runtime detection needed.
//
//go:noescape
func countSimpleU16Asm(in unsafe.Pointer, inLen int, count, count2 unsafe.Pointer)

// ycocgRForwardNEON and ycocgRInverseNEON are ARM64 assembly stubs.
// Currently implemented as scalar paths; the dispatch plumbing is in place
// for a future NEON-vectorised path.
//
//go:noescape
func ycocgRForwardNEON(rgb unsafe.Pointer, n int, y, co, cg unsafe.Pointer)

//go:noescape
func ycocgRInverseNEON(y, co, cg unsafe.Pointer, n int, rgb unsafe.Pointer)

// countSimpleNative dispatches to the ARM64 assembly histogram.
func countSimpleNative(in []uint16, count, count2 *[maxSymbolValue + 1]uint32) {
	if len(in) == 0 {
		return
	}
	countSimpleU16Asm(unsafe.Pointer(&in[0]), len(in),
		unsafe.Pointer(&count[0]), unsafe.Pointer(&count2[0]))
}

// ycocgRForwardNative dispatches YCoCg-R forward transform on ARM64.
func ycocgRForwardNative(rgb []byte, n int, y, co, cg []uint16) {
	if n == 0 {
		return
	}
	ycocgRForwardNEON(unsafe.Pointer(&rgb[0]), n,
		unsafe.Pointer(&y[0]), unsafe.Pointer(&co[0]), unsafe.Pointer(&cg[0]))
}

// ycocgRInverseNative dispatches YCoCg-R inverse transform on ARM64.
func ycocgRInverseNative(y, co, cg []uint16, n int, rgb []byte) {
	if n == 0 {
		return
	}
	ycocgRInverseNEON(unsafe.Pointer(&y[0]), unsafe.Pointer(&co[0]),
		unsafe.Pointer(&cg[0]), n, unsafe.Pointer(&rgb[0]))
}
