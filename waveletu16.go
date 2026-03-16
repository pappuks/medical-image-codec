// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// 2D Le Gall 5/3 integer wavelet transform (lossless).
// This is the same wavelet used in JPEG 2000 for reversible (lossless) coding.
//
// Lifting scheme (1D):
//   Forward:
//     Predict: d[n] = x[2n+1] - floor((x[2n] + x[2n+2]) / 2)
//     Update:  s[n] = x[2n]   + floor((d[n-1] + d[n] + 2) / 4)
//   Inverse:
//     Update:  x[2n]   = s[n] - floor((d[n-1] + d[n] + 2) / 4)
//     Predict: x[2n+1] = d[n] + floor((x[2n] + x[2n+2]) / 2)
//
// For 2D: apply horizontal transform to each row, then vertical to each column.
// Result layout: coefficients stay interleaved (even=low, odd=high).

// wt53Forward1D applies the forward 5/3 lifting to n elements starting at
// data[offset] with the given stride. Works entirely with int32 for lossless
// integer arithmetic.
func wt53Forward1D(data []int32, offset, n, stride int) {
	if n < 2 {
		return
	}

	nHalf := n / 2

	// Predict step: update odd-indexed elements.
	// d[i] = x[2i+1] - floor((x[2i] + x[2i+2]) / 2)
	for i := 0; i < nHalf; i++ {
		odd := offset + (2*i+1)*stride
		left := offset + (2*i)*stride
		var right int
		if 2*i+2 < n {
			right = offset + (2*i+2)*stride
		} else {
			// Symmetric extension at right boundary
			right = offset + (2*i)*stride
		}
		data[odd] = data[odd] - (data[left]+data[right])>>1
	}

	// Update step: update even-indexed elements.
	// s[i] = x[2i] + floor((d[i-1] + d[i] + 2) / 4)
	nLow := (n + 1) / 2
	for i := 0; i < nLow; i++ {
		even := offset + (2*i)*stride
		// d[i] is at odd position 2i+1
		var dRight int32
		if 2*i+1 < n {
			dRight = data[offset+(2*i+1)*stride]
		} else {
			// If n is odd, last even has no right detail; use symmetric extension
			if i > 0 {
				dRight = data[offset+(2*i-1)*stride]
			} else {
				dRight = 0
			}
		}
		var dLeft int32
		if i > 0 {
			dLeft = data[offset+(2*i-1)*stride]
		} else {
			// Symmetric extension at left boundary: d[-1] = d[0]
			dLeft = dRight
		}
		data[even] = data[even] + (dLeft+dRight+2)>>2
	}
}

// wt53Inverse1D applies the inverse 5/3 lifting to n elements starting at
// data[offset] with the given stride.
func wt53Inverse1D(data []int32, offset, n, stride int) {
	if n < 2 {
		return
	}

	nHalf := n / 2
	nLow := (n + 1) / 2

	// Undo update step: restore even-indexed elements.
	// x[2i] = s[i] - floor((d[i-1] + d[i] + 2) / 4)
	for i := 0; i < nLow; i++ {
		even := offset + (2*i)*stride
		var dRight int32
		if 2*i+1 < n {
			dRight = data[offset+(2*i+1)*stride]
		} else {
			if i > 0 {
				dRight = data[offset+(2*i-1)*stride]
			} else {
				dRight = 0
			}
		}
		var dLeft int32
		if i > 0 {
			dLeft = data[offset+(2*i-1)*stride]
		} else {
			dLeft = dRight
		}
		data[even] = data[even] - (dLeft+dRight+2)>>2
	}

	// Undo predict step: restore odd-indexed elements.
	// x[2n+1] = d[n] + floor((x[2n] + x[2n+2]) / 2)
	for i := 0; i < nHalf; i++ {
		odd := offset + (2*i+1)*stride
		left := offset + (2*i)*stride
		var right int
		if 2*i+2 < n {
			right = offset + (2*i+2)*stride
		} else {
			right = offset + (2*i)*stride
		}
		data[odd] = data[odd] + (data[left]+data[right])>>1
	}
}

// WaveletForward2D applies a single-level 2D forward 5/3 wavelet transform
// in-place on data of dimensions rows x cols.
func WaveletForward2D(data []int32, rows, cols int) {
	// Horizontal transform: each row
	for y := 0; y < rows; y++ {
		wt53Forward1D(data, y*cols, cols, 1)
	}
	// Vertical transform: each column
	for x := 0; x < cols; x++ {
		wt53Forward1D(data, x, rows, cols)
	}
}

// WaveletInverse2D applies a single-level 2D inverse 5/3 wavelet transform
// in-place on data of dimensions rows x cols.
func WaveletInverse2D(data []int32, rows, cols int) {
	// Inverse vertical transform: each column
	for x := 0; x < cols; x++ {
		wt53Inverse1D(data, x, rows, cols)
	}
	// Inverse horizontal transform: each row
	for y := 0; y < rows; y++ {
		wt53Inverse1D(data, y*cols, cols, 1)
	}
}

// wt53Forward2DSeparated applies the 5/3 forward wavelet transform to the
// rows×cols region in a buffer of fullCols width, producing the standard
// Mallat subband layout:
//
//	[LL | HL]
//	[LH | HH]
//
// where LL occupies top-left (nRowLow × nColLow), HL top-right,
// LH bottom-left, HH bottom-right, with nRowLow=(rows+1)/2, nColLow=(cols+1)/2.
//
// This layout allows correct multi-level transforms: each subsequent level
// is applied only to the contiguous LL region in the top-left corner.
func wt53Forward2DSeparated(data []int32, rows, cols, fullCols int) {
	nColLow := (cols + 1) / 2
	nRowLow := (rows + 1) / 2
	rowTmp := make([]int32, cols)
	colTmp := make([]int32, rows)

	// Horizontal 1D lifting to each row (interleaved output: even=low, odd=high)
	for y := 0; y < rows; y++ {
		wt53Forward1D(data, y*fullCols, cols, 1)
	}
	// De-interleave each row: even positions → first half, odd → second half
	for y := 0; y < rows; y++ {
		start := y * fullCols
		copy(rowTmp, data[start:start+cols])
		for i := 0; i < nColLow; i++ {
			data[start+i] = rowTmp[2*i]
		}
		for i := 0; i < cols/2; i++ {
			data[start+nColLow+i] = rowTmp[2*i+1]
		}
	}
	// Vertical 1D lifting on left-half columns (x=0..nColLow-1), then de-interleave
	for x := 0; x < nColLow; x++ {
		wt53Forward1D(data, x, rows, fullCols)
		for i := 0; i < rows; i++ {
			colTmp[i] = data[i*fullCols+x]
		}
		for i := 0; i < nRowLow; i++ {
			data[i*fullCols+x] = colTmp[2*i]
		}
		for i := 0; i < rows/2; i++ {
			data[(nRowLow+i)*fullCols+x] = colTmp[2*i+1]
		}
	}
	// Vertical 1D lifting on right-half columns (x=nColLow..cols-1), then de-interleave
	for x := nColLow; x < cols; x++ {
		wt53Forward1D(data, x, rows, fullCols)
		for i := 0; i < rows; i++ {
			colTmp[i] = data[i*fullCols+x]
		}
		for i := 0; i < nRowLow; i++ {
			data[i*fullCols+x] = colTmp[2*i]
		}
		for i := 0; i < rows/2; i++ {
			data[(nRowLow+i)*fullCols+x] = colTmp[2*i+1]
		}
	}
}

// wt53Inverse2DSeparated applies the 5/3 inverse wavelet transform to a region
// stored in Mallat subband layout, restoring the original pixels in-place.
func wt53Inverse2DSeparated(data []int32, rows, cols, fullCols int) {
	nColLow := (cols + 1) / 2
	nRowLow := (rows + 1) / 2
	colTmp := make([]int32, rows)
	rowTmp := make([]int32, cols)

	// Re-interleave vertically + inverse vertical lifting: left-half columns
	for x := 0; x < nColLow; x++ {
		for i := 0; i < nRowLow; i++ {
			colTmp[2*i] = data[i*fullCols+x]
		}
		for i := 0; i < rows/2; i++ {
			colTmp[2*i+1] = data[(nRowLow+i)*fullCols+x]
		}
		for i := 0; i < rows; i++ {
			data[i*fullCols+x] = colTmp[i]
		}
		wt53Inverse1D(data, x, rows, fullCols)
	}
	// Re-interleave vertically + inverse vertical lifting: right-half columns
	for x := nColLow; x < cols; x++ {
		for i := 0; i < nRowLow; i++ {
			colTmp[2*i] = data[i*fullCols+x]
		}
		for i := 0; i < rows/2; i++ {
			colTmp[2*i+1] = data[(nRowLow+i)*fullCols+x]
		}
		for i := 0; i < rows; i++ {
			data[i*fullCols+x] = colTmp[i]
		}
		wt53Inverse1D(data, x, rows, fullCols)
	}
	// Re-interleave horizontally + inverse horizontal lifting: each row
	for y := 0; y < rows; y++ {
		start := y * fullCols
		copy(rowTmp, data[start:start+cols])
		for i := 0; i < nColLow; i++ {
			data[start+2*i] = rowTmp[i]
		}
		for i := 0; i < cols/2; i++ {
			data[start+2*i+1] = rowTmp[nColLow+i]
		}
		wt53Inverse1D(data, start, cols, 1)
	}
}
