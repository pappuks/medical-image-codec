// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"math/bits"
)

// GradDeltaCompressU16 applies a CALIC-style gradient-adaptive predictor to
// 16-bit pixel data. For each interior pixel it computes horizontal and vertical
// activity from reconstructed neighbors and selects the best predictor:
//
//   - Strong horizontal gradient → use top (N)
//   - Strong vertical gradient   → use left (W)
//   - Otherwise                  → Paeth (W + N − NW)
//
// No side information is needed: the decoder recomputes the same gradients.
func GradDeltaCompressU16(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
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
				predicted = int32(in[index-1]) // left only
			} else if x == 0 {
				predicted = int32(in[index-width]) // top only
			} else {
				w := int32(in[index-1])       // left
				n := int32(in[index-width])   // top
				nw := int32(in[index-width-1]) // top-left
				ne := nw
				if x+1 < width {
					ne = int32(in[index-width+1])
				}
				predicted = gradPredict(w, n, nw, ne)
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

// GradDeltaDecompressU16 decompresses a gradient-adaptive delta stream back to pixels.
func GradDeltaDecompressU16(in []uint16, width int, height int) []uint16 {
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

	// First row: left only
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

		// First column: top only
		inputVal := in[ic]
		ic++
		if inputVal == delimiterForOverflow {
			out[rowStart] = in[ic]
			ic++
		} else {
			diff := int32(inputVal) - int32(deltaThreshold)
			out[rowStart] = uint16(int32(out[rowStart-width]) + diff)
		}

		// Interior pixels: gradient-adaptive predictor
		for x := 1; x < width; x++ {
			idx := rowStart + x
			inputVal := in[ic]
			ic++
			if inputVal == delimiterForOverflow {
				out[idx] = in[ic]
				ic++
			} else {
				diff := int32(inputVal) - int32(deltaThreshold)
				w := int32(out[idx-1])
				n := int32(out[idx-width])
				nw := int32(out[idx-width-1])
				ne := nw
				if x+1 < width {
					ne = int32(out[idx-width+1])
				}
				predicted := gradPredict(w, n, nw, ne)
				out[idx] = uint16(predicted + diff)
			}
		}
	}

	return out
}

// gradPredict uses a gradient-corrected average predictor with NE slope.
// Base: avg(W, N). Correction: (NE-NW)/8 clamped to ±(gw+gn)/2.
// When the neighborhood is smooth, the correction is ~zero and this
// reduces to plain avg. On gradients, the slope term improves prediction.
// gradShift controls the slope correction divisor (2^gradShift).
// 3 means slope/8. Tuned across 8 test modalities (MR, CT, CR, XR, MG1-4):
// improves 7/8, with CT being the sole regression (~2.5%) due to its
// sharp air-tissue boundaries where NE-based slope correction is unreliable.
const gradShift = 3

func gradPredict(w, n, nw, ne int32) int32 {
	avg := (w + n) >> 1
	gw := abs(w - nw)
	gn := abs(n - nw)
	g := gw + gn
	if g == 0 {
		return avg
	}
	slope := ne - nw
	corr := slope >> gradShift
	limit := g >> 1
	if corr > limit {
		corr = limit
	} else if corr < -limit {
		corr = -limit
	}
	return avg + corr
}

