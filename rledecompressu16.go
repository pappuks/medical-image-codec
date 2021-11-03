// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"math/bits"
)

type RleDecompressU16 struct {
	in             []uint16
	maxValue       uint16
	midCount       uint16
	i              int
	o              int
	out            []uint16
	c              uint16
	recurringValue uint16
}

func (r *RleDecompressU16) Init(input []uint16) {
	r.in = input
	r.maxValue = r.in[0]
	pixelDepth := bits.Len16(r.maxValue)
	r.midCount = uint16((1 << (pixelDepth - 1)) - 1)
	r.i = 1
	r.o = 0
	r.out = make([]uint16, 0, r.midCount)
	r.c = 0
}

func (r *RleDecompressU16) DecodeNextBlock() {
	r.out = r.out[:0]
	r.o = 0
	if r.i < len(r.in) {
		count := r.in[r.i]
		r.i += 1
		if count > r.midCount {
			r.out = append(r.out, r.in[r.i:(r.i+int(count-r.midCount))]...)
			r.i += int(count - r.midCount)
		} else {
			for k := 0; k < int(count); k++ {
				r.out = append(r.out, r.in[r.i])
			}
			r.i += 1
		}
	}
}

func (r *RleDecompressU16) DecodeNext() uint16 {
	if len(r.out) == 0 || r.o > len(r.out)-1 {
		r.DecodeNextBlock()
	}
	retVal := r.out[r.o]
	r.o += 1
	return retVal
}

func (r *RleDecompressU16) DecodeNext2() uint16 {
	if r.c == 0 || r.c == r.midCount {
		r.c = r.in[r.i]
		r.i++
		if r.c <= r.midCount {
			r.recurringValue = r.in[r.i]
			r.i++
		}
	}
	output := uint16(0)
	if r.c > r.midCount {
		output = r.in[r.i]
		r.i++
		r.c--
	} else {
		output = r.recurringValue
		r.c--
	}

	return output

}
