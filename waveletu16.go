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
