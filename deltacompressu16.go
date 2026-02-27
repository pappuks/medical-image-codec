// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

func DeltaCompressU16(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := (uint16)((1 << (pixelDepth - 1)) - 1)   // For 16 bits this will be 0x7FFF. We have to ensure that 2 * deltaThreshold is less than delimiter
	delimiterForOverflow := (uint16)((1 << (pixelDepth)) - 1) // For 16 bits this will be 0xFFFF
	out := make([]uint16, 0, width*height*2)
	out = append(out, maxValue)

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

			if uint16(abs(diff)) >= deltaThreshold { // We have to ensure that diff + deltaThreshold is not equal to delimiter.
				out = append(out, delimiterForOverflow)
				out = append(out, inputVal)
			} else {
				out = append(out, uint16(int32(deltaThreshold)+diff))
			}

		}
	}

	return out, nil
}

func DeltaDecompressU16(in []uint16, width int, height int) []uint16 {
	maxValue := in[0]
	out := make([]uint16, width*height)
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := (uint16)((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := (uint16)((1 << (pixelDepth)) - 1)
	ic := 1 // input counter

	// Top-left corner (x=0, y=0): no neighbors
	{
		inputVal := in[ic]
		ic++
		if inputVal == delimiterForOverflow {
			out[0] = in[ic]
			ic++
		} else {
			out[0] = uint16(int32(inputVal) - int32(deltaThreshold))
		}
	}

	// First row (y=0, x>0): only left neighbor, no branching on y
	for x := 1; x < width; x++ {
		inputVal := in[ic]
		ic++
		if inputVal == delimiterForOverflow {
			out[x] = in[ic]
			ic++
		} else {
			diff := int32(inputVal) - int32(deltaThreshold)
			out[x] = uint16(int32(out[x-1]) + diff)
		}
	}

	// First column of remaining rows (x=0, y>0): only top neighbor
	// and interior pixels (x>0, y>0): average of left + top, no boundary checks.
	for y := 1; y < height; y++ {
		rowStart := y * width

		// x=0: only top neighbor
		inputVal := in[ic]
		ic++
		if inputVal == delimiterForOverflow {
			out[rowStart] = in[ic]
			ic++
		} else {
			diff := int32(inputVal) - int32(deltaThreshold)
			out[rowStart] = uint16(int32(out[rowStart-width]) + diff)
		}

		// Interior pixels: left + top averaged. No boundary branching.
		for x := 1; x < width; x++ {
			idx := rowStart + x
			inputVal := in[ic]
			ic++
			if inputVal == delimiterForOverflow {
				out[idx] = in[ic]
				ic++
			} else {
				diff := int32(inputVal) - int32(deltaThreshold)
				prevSymbol := (int32(out[idx-1]) + int32(out[idx-width])) >> 1
				out[idx] = uint16(prevSymbol + diff)
			}
		}
	}

	return out
}

func abs(x int32) int32 {
	// Branchless absolute value using arithmetic right shift.
	mask := x >> 31
	return (x ^ mask) - mask
}
