//go:build !amd64

// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// countSimpleNative is the pure-Go fallback for non-amd64 platforms.
func countSimpleNative(in []uint16, count, count2 *[maxSymbolValue + 1]uint32) {
	for i := 0; i < len(in)-1; i += 2 {
		count[in[i]]++
		count2[in[i+1]]++
	}
	if len(in)&1 != 0 {
		count[in[len(in)-1]]++
	}
}

func ycocgRForwardNative(rgb []byte, n int, y, co, cg []uint16) {
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

func ycocgRInverseNative(y, co, cg []uint16, n int, rgb []byte) {
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
