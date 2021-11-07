// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

type DeltaZZU16 struct {
	pixelDepth     uint8
	upperThreshold uint16
	Out            []uint16
}

func (c *DeltaZZU16) Compress(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	c.pixelDepth = uint8(bits.Len16(maxValue))
	c.upperThreshold = (uint16)((1 << (c.pixelDepth - 1)) - 1)
	c.Out = make([]uint16, width*height*2)
	c.Out[0] = maxValue

	o := 1

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x

			divVal := 0
			prevSymbol := int32(0)
			if x > 0 {
				prevSymbol = int32(in[index-1])
				divVal += 1
			}
			if y > 0 {
				prevSymbol += int32(in[index-width])
				divVal += 1
			}

			if divVal == 2 {
				prevSymbol = prevSymbol >> 1
			}

			inputVal := in[index]

			diff := int32(int32(inputVal) - prevSymbol)

			if uint16(abs(diff)) >= c.upperThreshold { // We have to ensure that diff + deltaThreshold is not equal to delimiter.
				c.Out[o] = c.upperThreshold
				c.Out[o+1] = inputVal
				o += 2
			} else {
				c.Out[o] = ZigZag(int16(diff))
				o++
			}

		}
	}

	return c.Out[:o], nil
}

func (d *DeltaZZU16) Decompress(in []uint16, width int, height int) []uint16 {
	maxValue := in[0]
	d.Out = make([]uint16, width*height)
	d.pixelDepth = uint8(bits.Len16(maxValue))
	d.upperThreshold = (uint16)((1 << (d.pixelDepth - 1)) - 1)
	inputCounter := int32(1)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x
			inputVal := in[inputCounter]
			inputCounter++
			if inputVal == d.upperThreshold {
				d.Out[index] = in[inputCounter]
				inputCounter++
			} else {
				diff := int32(UnZigZag(inputVal))

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

	return d.Out
}

func ZigZag(x int16) uint16 {
	ux := uint64(x) << 1
	if x < 0 {
		ux = ^ux
	}
	return uint16(ux)
}

func UnZigZag(ux uint16) int16 {
	x := int64(ux >> 1)
	if ux&1 != 0 {
		x = ^x
	}
	return int16(x)
}
