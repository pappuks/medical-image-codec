//go:build arm64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

// rans8StateDecompNEON is the ARM64 assembly hot loop for 8-state rANS decode.
// NEON is mandatory on ARM64; 8 independent lanes give maximum ILP.
// Returns the number of symbols written (0 signals the Go path to take over).
//
//go:noescape
func rans8StateDecompNEON(dt, br, states, out unsafe.Pointer, count int) int

// rans8StateDecompNative dispatches to the ARM64 assembly kernel.
func rans8StateDecompNative(dt, br, states, out unsafe.Pointer, count int) int {
	return rans8StateDecompNEON(dt, br, states, out, count)
}
