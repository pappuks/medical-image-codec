// Copyright 2021 Kuldeep Singh
// ZigZag u16 residual helpers added 2026 — MIT, same as this tree.
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// ZigZagDeltaU16 is a wrapping previous-sample delta, then zigzag so
// small signed residuals become small unsigned values (pipeline type 3
// reserved in web/README.md). Inverse of UnzigZagDeltaU16.
func ZigZagDeltaU16(in []uint16) []uint16 {
	out := make([]uint16, len(in))
	var prev uint16
	for i, v := range in {
		d := int16(v - prev)
		out[i] = zigzagI16(d)
		prev = v
	}
	return out
}

// UnzigZagDeltaU16 rebuilds samples from ZigZagDeltaU16 output.
func UnzigZagDeltaU16(in []uint16) []uint16 {
	out := make([]uint16, len(in))
	var prev uint16
	for i, z := range in {
		d := unzigzagI16(z)
		v := prev + uint16(d)
		out[i] = v
		prev = v
	}
	return out
}

func zigzagI16(d int16) uint16 {
	u := uint16(d)
	return (u << 1) ^ uint16(int16(u)>>15)
}

func unzigzagI16(z uint16) int16 {
	return int16((z >> 1) ^ uint16(-int16(z&1)))
}
