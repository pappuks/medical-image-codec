// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

type DeltaZZU16 struct {
	pixelDepth           uint8
	upperThreshold       uint16
	delimiterForOverflow uint16
	Out                  []uint16
	in                   []uint16
	inputCounter         int32
}

func (c *DeltaZZU16) Compress(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	c.pixelDepth = uint8(bits.Len16(maxValue))
	c.upperThreshold = (uint16)((1 << (c.pixelDepth - 1)) - 1)
	c.delimiterForOverflow = (uint16)((1 << (c.pixelDepth)) - 1)
	c.Out = make([]uint16, width*height*2)
	c.Out[0] = maxValue

	o := 1

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x
			prevSymbol := int32(0)
			if x > 0 {
				prevSymbol = int32(in[index-1])
			}

			inputVal := in[index]

			diff := int32(int32(inputVal) - prevSymbol)

			if uint16(abs(diff)) >= c.upperThreshold { // We have to ensure that diff + deltaThreshold is not equal to delimiter.
				c.Out[o] = c.delimiterForOverflow
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
	d.in = in
	maxValue := d.in[0]
	d.Out = make([]uint16, width*height)
	d.pixelDepth = uint8(bits.Len16(maxValue))
	d.upperThreshold = (uint16)((1 << (d.pixelDepth - 1)) - 1)
	d.delimiterForOverflow = (uint16)((1 << (d.pixelDepth)) - 1)
	d.inputCounter = int32(1)

	for y := 0; y < height; y++ {
		d.DecodeNextSymbol(0, y, width, height)
		for x := 1; x < width; x++ {
			d.DecodeNextSymbolNC(x, y, width, height)
		}
	}

	return d.Out
}

func (d *DeltaZZU16) DecodeNextSymbolNC(x int, y int, width int, height int) {
	index := (y * width) + x
	inputVal := d.in[d.inputCounter]
	d.inputCounter++
	if inputVal == d.delimiterForOverflow {
		d.Out[index] = d.in[d.inputCounter]
		d.inputCounter++
	} else {
		diff := int32(UnZigZag(inputVal))
		prevSymbol := int32(d.Out[index-1])
		d.Out[index] = uint16(prevSymbol + diff)
	}
}

func (d *DeltaZZU16) DecodeNextSymbol(x int, y int, width int, height int) {
	index := (y * width) + x
	inputVal := d.in[d.inputCounter]
	d.inputCounter++
	if inputVal == d.delimiterForOverflow {
		d.Out[index] = d.in[d.inputCounter]
		d.inputCounter++
	} else {
		diff := int32(UnZigZag(inputVal))
		prevSymbol := int32(0)

		if x > 0 {
			prevSymbol = int32(d.Out[index-1])
		}

		d.Out[index] = uint16(prevSymbol + diff)
	}
}

func ZigZag(x int16) uint16 {
	ux := uint16(x) << 1
	if x < 0 {
		ux = ^ux
	}
	return uint16(ux)
}

func UnZigZag(ux uint16) int16 {
	x := int16(ux >> 1)
	if ux&1 != 0 {
		x = ^x
	}
	return int16(x)
}
