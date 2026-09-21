// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

// MEDDeltaCompressU16 applies the JPEG-LS MED (Median Edge Detector) predictor
// to 16-bit pixel data. The MED predictor uses median(left, top, left+top-diag)
// which adapts to horizontal, vertical, and diagonal edges better than the
// simple average-of-neighbors predictor used in MIC's standard delta encoder.
func MEDDeltaCompressU16(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := uint16((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := uint16((1 << pixelDepth) - 1)
	out := make([]uint16, 0, width*height*2)
	out = append(out, maxValue)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x

			var predicted int32
			if x == 0 && y == 0 {
				predicted = 0
			} else if y == 0 {
				predicted = int32(in[index-1])
			} else if x == 0 {
				predicted = int32(in[index-width])
			} else {
				a := int32(in[index-1])      // left
				b := int32(in[index-width])   // top
				c := int32(in[index-width-1]) // top-left (diagonal)
				predicted = medPredict(a, b, c)
			}

			inputVal := in[index]
			diff := int32(inputVal) - predicted

			if uint16(abs(diff)) >= deltaThreshold {
				out = append(out, delimiterForOverflow)
				out = append(out, inputVal)
			} else {
				out = append(out, uint16(int32(deltaThreshold)+diff))
			}
		}
	}

	return out, nil
}

// MEDDeltaDecompressU16 decompresses a MED-predicted delta stream back to pixels.
func MEDDeltaDecompressU16(in []uint16, width int, height int) []uint16 {
	maxValue := in[0]
	out := make([]uint16, width*height)
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := uint16((1 << (pixelDepth - 1)) - 1)
	delimiterForOverflow := uint16((1 << pixelDepth) - 1)
	ic := 1

	// Top-left corner
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

	// First row: only left neighbor
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

	// Remaining rows
	for y := 1; y < height; y++ {
		rowStart := y * width

		// First column: only top neighbor
		inputVal := in[ic]
		ic++
		if inputVal == delimiterForOverflow {
			out[rowStart] = in[ic]
			ic++
		} else {
			diff := int32(inputVal) - int32(deltaThreshold)
			out[rowStart] = uint16(int32(out[rowStart-width]) + diff)
		}

		// Interior: MED predictor
		for x := 1; x < width; x++ {
			idx := rowStart + x
			inputVal := in[ic]
			ic++
			if inputVal == delimiterForOverflow {
				out[idx] = in[ic]
				ic++
			} else {
				diff := int32(inputVal) - int32(deltaThreshold)
				a := int32(out[idx-1])       // left
				b := int32(out[idx-width])    // top
				c := int32(out[idx-width-1])  // top-left
				predicted := medPredict(a, b, c)
				out[idx] = uint16(predicted + diff)
			}
		}
	}

	return out
}

// medPredict implements the JPEG-LS MED (Median Edge Detector) predictor:
//
//	if c >= max(a, b) -> min(a, b)   [vertical or horizontal edge from top-left]
//	if c <= min(a, b) -> max(a, b)   [smooth gradient]
//	otherwise         -> a + b - c   [no dominant edge]
//
// where a=left, b=top, c=top-left diagonal.
func medPredict(a, b, c int32) int32 {
	if c >= a && c >= b {
		if a < b {
			return a
		}
		return b
	}
	if c <= a && c <= b {
		if a > b {
			return a
		}
		return b
	}
	return a + b - c
}
