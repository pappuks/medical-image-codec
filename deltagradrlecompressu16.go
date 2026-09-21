// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

// GradDeltaRleCompressU16 combines the gradient-adaptive delta predictor with RLE.
type GradDeltaRleCompressU16 struct {
	deltaThreshold       uint16
	delimiterForOverflow uint16
	out                  RleCompressU16
}

// GradDeltaRleDecompressU16 decompresses a gradient-adaptive delta+RLE stream.
type GradDeltaRleDecompressU16 struct {
	deltaThreshold       uint16
	delimiterForOverflow uint16
	decomp               RleDecompressU16
	Out                  []uint16
}

func (d *GradDeltaRleCompressU16) Compress(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	pixelDepth := bits.Len16(maxValue)
	d.deltaThreshold = uint16((1 << (pixelDepth - 1)) - 1)
	d.delimiterForOverflow = uint16((1 << pixelDepth) - 1)
	d.out.Init(width, height, d.delimiterForOverflow)
	d.out.Encode(maxValue)

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
				w := int32(in[index-1])
				n := int32(in[index-width])
				nw := int32(in[index-width-1])
				ne := nw
				if x+1 < width {
					ne = int32(in[index-width+1])
				}
				predicted = gradPredict(w, n, nw, ne)
			}

			inputVal := in[index]
			diff := int32(inputVal) - predicted

			if uint16(abs(diff)) >= d.deltaThreshold {
				d.out.Encode(d.delimiterForOverflow)
				d.out.Encode(inputVal)
			} else {
				d.out.Encode(uint16(int32(d.deltaThreshold) + diff))
			}
		}
	}

	d.out.Flush()
	return d.out.Out[:], nil
}

func (d *GradDeltaRleDecompressU16) Decompress(in []uint16, width int, height int) {
	d.decomp.Init(in)
	maxValue := d.decomp.DecodeNext2()
	d.Out = make([]uint16, width*height)
	pixelDepth := bits.Len16(maxValue)
	d.deltaThreshold = uint16((1 << (pixelDepth - 1)) - 1)
	d.delimiterForOverflow = uint16((1 << pixelDepth) - 1)

	// First row: left only
	d.decodePixel(0) // corner
	for x := 1; x < width; x++ {
		inputVal := d.decomp.DecodeNext2()
		if inputVal == d.delimiterForOverflow {
			d.Out[x] = d.decomp.DecodeNext2()
		} else {
			diff := int32(inputVal) - int32(d.deltaThreshold)
			d.Out[x] = uint16(int32(d.Out[x-1]) + diff)
		}
	}

	// Remaining rows
	for y := 1; y < height; y++ {
		rowStart := y * width

		// First column: top only
		inputVal := d.decomp.DecodeNext2()
		if inputVal == d.delimiterForOverflow {
			d.Out[rowStart] = d.decomp.DecodeNext2()
		} else {
			diff := int32(inputVal) - int32(d.deltaThreshold)
			d.Out[rowStart] = uint16(int32(d.Out[rowStart-width]) + diff)
		}

		// Interior: gradient-adaptive
		for x := 1; x < width; x++ {
			idx := rowStart + x
			inputVal := d.decomp.DecodeNext2()
			if inputVal == d.delimiterForOverflow {
				d.Out[idx] = d.decomp.DecodeNext2()
			} else {
				diff := int32(inputVal) - int32(d.deltaThreshold)
				w := int32(d.Out[idx-1])
				n := int32(d.Out[idx-width])
				nw := int32(d.Out[idx-width-1])
				ne := nw
				if x+1 < width {
					ne = int32(d.Out[idx-width+1])
				}
				predicted := gradPredict(w, n, nw, ne)
				d.Out[idx] = uint16(predicted + diff)
			}
		}
	}
}

func (d *GradDeltaRleDecompressU16) decodePixel(idx int) {
	inputVal := d.decomp.DecodeNext2()
	if inputVal == d.delimiterForOverflow {
		d.Out[idx] = d.decomp.DecodeNext2()
	} else {
		d.Out[idx] = uint16(int32(inputVal) - int32(d.deltaThreshold))
	}
}
