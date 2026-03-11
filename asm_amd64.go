//go:build amd64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

var (
	cpuHasSSSE3 bool
	cpuHasAVX2  bool
)

func init() {
	_, _, ecx, _ := cpuidAMD64(1, 0)
	cpuHasSSSE3 = ecx&(1<<9) != 0

	_, ebx, _, _ := cpuidAMD64(7, 0)
	cpuHasAVX2 = ebx&(1<<5) != 0
}

//go:noescape
func cpuidAMD64(leaf, subleaf uint32) (eax, ebx, ecx, edx uint32)

//go:noescape
func countSimpleU16Asm(in unsafe.Pointer, inLen int, count, count2 unsafe.Pointer)

//go:noescape
func ycocgRForwardSSSE3(rgb unsafe.Pointer, n int, y, co, cg unsafe.Pointer)

//go:noescape
func ycocgRInverseSSSE3(y, co, cg unsafe.Pointer, n int, rgb unsafe.Pointer)

// countSimpleNative dispatches to the assembly histogram on amd64.
func countSimpleNative(in []uint16, count, count2 *[maxSymbolValue + 1]uint32) {
	if len(in) == 0 {
		return
	}
	countSimpleU16Asm(unsafe.Pointer(&in[0]), len(in),
		unsafe.Pointer(&count[0]), unsafe.Pointer(&count2[0]))
}

// ycocgRForwardNative dispatches YCoCg-R forward transform.
func ycocgRForwardNative(rgb []byte, n int, y, co, cg []uint16) {
	if cpuHasSSSE3 {
		ycocgRForwardSSSE3(unsafe.Pointer(&rgb[0]), n,
			unsafe.Pointer(&y[0]), unsafe.Pointer(&co[0]), unsafe.Pointer(&cg[0]))
		return
	}
	ycocgRForwardScalar(rgb, n, y, co, cg)
}

// ycocgRInverseNative dispatches YCoCg-R inverse transform.
func ycocgRInverseNative(y, co, cg []uint16, n int, rgb []byte) {
	if cpuHasSSSE3 {
		ycocgRInverseSSSE3(unsafe.Pointer(&y[0]), unsafe.Pointer(&co[0]),
			unsafe.Pointer(&cg[0]), n, unsafe.Pointer(&rgb[0]))
		return
	}
	ycocgRInverseScalar(y, co, cg, n, rgb)
}

func ycocgRForwardScalar(rgb []byte, n int, y, co, cg []uint16) {
	for i := 0; i < n; i++ {
		r := int(rgb[i*3])
		g := int(rgb[i*3+1])
		b := int(rgb[i*3+2])
		coVal := r - b
		t := b + (coVal >> 1)
		cgVal := g - t
		yVal := t + (cgVal >> 1)
		y[i] = uint16(yVal)
		co[i] = ZigZag(int16(coVal))
		cg[i] = ZigZag(int16(cgVal))
	}
}

func ycocgRInverseScalar(y, co, cg []uint16, n int, rgb []byte) {
	for i := 0; i < n; i++ {
		yVal := int(y[i])
		coVal := int(UnZigZag(co[i]))
		cgVal := int(UnZigZag(cg[i]))
		t := yVal - (cgVal >> 1)
		g := cgVal + t
		b := t - (coVal >> 1)
		r := coVal + b
		rgb[i*3] = byte(r)
		rgb[i*3+1] = byte(g)
		rgb[i*3+2] = byte(b)
	}
}
