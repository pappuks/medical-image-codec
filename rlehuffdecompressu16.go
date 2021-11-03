// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"math/bits"
)

type RleHuffDecompressU16 struct {
	maxValue       uint16
	midCount       uint16
	o              int
	out            []uint16
	c              uint16
	d              CanHuffmanDecompressU16
	recurringValue uint16
}

func (r *RleHuffDecompressU16) Init(input []byte) {
	r.d.Init(input)
	r.d.ReadTable()
	r.d.DecompressInit()
	r.maxValue = r.d.DecodeNext()
	pixelDepth := bits.Len16(r.maxValue)
	r.midCount = uint16((1 << (pixelDepth - 1)) - 1)
	r.o = 0
	r.out = make([]uint16, 0, r.midCount)
	r.c = 0
}

func (r *RleHuffDecompressU16) DecodeNext() uint16 {
	if r.c == 0 || r.c == r.midCount {
		r.c = r.d.DecodeNext()
		if r.c <= r.midCount {
			r.recurringValue = r.d.DecodeNext()
		}
	}
	output := uint16(0)
	if r.c > r.midCount {
		output = r.d.DecodeNext()
		r.c--
	} else {
		output = r.recurringValue
		r.c--
	}

	return output

}
