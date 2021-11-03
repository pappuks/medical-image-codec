// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

type DeltaRleHuffDecompressU16 struct {
	deltaThreshold       uint16
	delimiterForOverflow uint16
	decomp               RleHuffDecompressU16
	Out                  []uint16
}

func (d *DeltaRleHuffDecompressU16) Decompress(in []byte, width int, height int) {
	d.decomp.Init(in)
	maxValue := d.decomp.DecodeNext()
	d.Out = make([]uint16, width*height)
	pixelDepth := bits.Len16(maxValue)
	d.deltaThreshold = (uint16)((1 << (pixelDepth - 1)) - 1)   // For 16 bits this will be 0x7FFF. We have to ensure that 2 * deltaThreshold is less than delimiter
	d.delimiterForOverflow = (uint16)((1 << (pixelDepth)) - 1) // For 16 bits this will be 0xFFFF

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x
			inputVal := d.decomp.DecodeNext()
			if inputVal == d.delimiterForOverflow {
				d.Out[index] = d.decomp.DecodeNext()
			} else {
				diff := int32(inputVal) - int32(d.deltaThreshold) // DeltaThreshhold is already ushort

				divVal := 0
				prevSymbol := int32(0)

				if x > 0 {
					prevSymbol = int32(d.Out[index-1])
					divVal++
				}
				if y > 0 {
					prevSymbol += int32(d.Out[index-width])
					divVal++
				}

				if divVal == 2 {
					prevSymbol = prevSymbol >> 1
				}

				d.Out[index] = uint16(prevSymbol + diff)
			}
		}
	}
}
