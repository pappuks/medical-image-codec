// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import "unsafe"

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

// ---------------------------------------------------------------------------
// Scalar helpers used by the SIMD dispatch shims
// ---------------------------------------------------------------------------

// wt53PredictScalar: odd[i] -= (left[i] + right[i]) >> 1  for i = 0..n-1
func wt53PredictScalar(left, right, odd unsafe.Pointer, n int) {
	l := unsafe.Slice((*int32)(left), n)
	r := unsafe.Slice((*int32)(right), n)
	o := unsafe.Slice((*int32)(odd), n)
	for i := 0; i < n; i++ {
		o[i] -= (l[i] + r[i]) >> 1
	}
}

// wt53UpdateScalar: even[i] += (dLeft[i] + dRight[i] + 2) >> 2  for i = 0..n-1
func wt53UpdateScalar(dLeft, dRight, even unsafe.Pointer, n int) {
	dl := unsafe.Slice((*int32)(dLeft), n)
	dr := unsafe.Slice((*int32)(dRight), n)
	e := unsafe.Slice((*int32)(even), n)
	for i := 0; i < n; i++ {
		e[i] += (dl[i] + dr[i] + 2) >> 2
	}
}

// wt53InvPredictScalar: odd[i] += (left[i] + right[i]) >> 1  (inverse)
func wt53InvPredictScalar(left, right, odd unsafe.Pointer, n int) {
	l := unsafe.Slice((*int32)(left), n)
	r := unsafe.Slice((*int32)(right), n)
	o := unsafe.Slice((*int32)(odd), n)
	for i := 0; i < n; i++ {
		o[i] += (l[i] + r[i]) >> 1
	}
}

// wt53InvUpdateScalar: even[i] -= (dLeft[i] + dRight[i] + 2) >> 2  (inverse)
func wt53InvUpdateScalar(dLeft, dRight, even unsafe.Pointer, n int) {
	dl := unsafe.Slice((*int32)(dLeft), n)
	dr := unsafe.Slice((*int32)(dRight), n)
	e := unsafe.Slice((*int32)(even), n)
	for i := 0; i < n; i++ {
		e[i] -= (dl[i] + dr[i] + 2) >> 2
	}
}

// ---------------------------------------------------------------------------
// SIMD-accelerated 2D separated wavelet transforms
// ---------------------------------------------------------------------------

// wt53Forward2DSeparatedSIMD is the SIMD-accelerated version of
// wt53Forward2DSeparated. It uses blocked column processing (8 columns per
// cache-line load) and dispatches the predict/update inner loops to AVX2 on
// AMD64. Compared to the scalar version, this reduces column-pass cache misses
// ~8× on large images.
//
// Layout produced: same Mallat subband layout as wt53Forward2DSeparated.
func wt53Forward2DSeparatedSIMD(data []int32, rows, cols, fullCols int) {
	nColLow := (cols + 1) / 2
	nRowLow := (rows + 1) / 2
	nRowHigh := rows / 2
	rowTmp := make([]int32, cols)

	// ── Horizontal row pass ──────────────────────────────────────────────
	// Apply 1D forward lifting to each row (interleaved output), then
	// de-interleave in-place.
	for y := 0; y < rows; y++ {
		wt53Forward1D(data, y*fullCols, cols, 1)
		start := y * fullCols
		copy(rowTmp, data[start:start+cols])
		for i := 0; i < nColLow; i++ {
			data[start+i] = rowTmp[2*i]
		}
		for i := 0; i < cols/2; i++ {
			data[start+nColLow+i] = rowTmp[2*i+1]
		}
	}

	// ── Vertical column pass (blocked) ───────────────────────────────────
	// Process columns in blocks of colBlock for cache efficiency.
	// Within each block, do ALL predict steps then ALL update steps so
	// that the predict-then-update ordering of the lifting scheme is respected
	// on a per-block basis (columns are independent, so this is valid).
	const colBlock = 8
	colTmp := make([]int32, rows) // temp for de-interleave

	for x0 := 0; x0 < cols; x0 += colBlock {
		xEnd := x0 + colBlock
		if xEnd > cols {
			xEnd = cols
		}
		bw := xEnd - x0 // actual block width (1–8)

		// Vertical predict for this column block
		for i := 0; i < nRowHigh; i++ {
			leftBase := 2 * i * fullCols
			oddBase := (2*i + 1) * fullCols
			rightRow := 2*i + 2
			if rightRow >= rows {
				rightRow = 2 * i
			}
			rightBase := rightRow * fullCols

			wt53PredictBlocks(
				unsafe.Pointer(&data[leftBase+x0]),
				unsafe.Pointer(&data[rightBase+x0]),
				unsafe.Pointer(&data[oddBase+x0]),
				bw,
			)
		}

		// Vertical update for this column block
		for i := 0; i < nRowLow; i++ {
			evenBase := 2 * i * fullCols

			// dRight: odd row 2i+1 (may not exist for last even row when rows is odd)
			dRightRow := 2*i + 1
			if dRightRow >= rows {
				dRightRow = 2*i - 1
				if dRightRow < 0 {
					dRightRow = 0
				}
			}
			dRightBase := dRightRow * fullCols

			// dLeft: odd row 2i-1 (boundary: use dRight if i==0)
			dLeftRow := 2*i - 1
			if dLeftRow < 0 {
				dLeftRow = dRightRow
			}
			dLeftBase := dLeftRow * fullCols

			wt53UpdateBlocks(
				unsafe.Pointer(&data[dLeftBase+x0]),
				unsafe.Pointer(&data[dRightBase+x0]),
				unsafe.Pointer(&data[evenBase+x0]),
				bw,
			)
		}
	}

	// ── Vertical de-interleave ────────────────────────────────────────────
	// Move even rows → rows 0..nRowLow-1, odd rows → rows nRowLow..rows-1.
	// Process left half (nColLow columns) and right half separately; same logic.
	for x := 0; x < cols; x++ {
		for i := 0; i < rows; i++ {
			colTmp[i] = data[i*fullCols+x]
		}
		for i := 0; i < nRowLow; i++ {
			data[i*fullCols+x] = colTmp[2*i]
		}
		for i := 0; i < nRowHigh; i++ {
			data[(nRowLow+i)*fullCols+x] = colTmp[2*i+1]
		}
	}
}

// wt53Inverse2DSeparatedSIMD is the SIMD-accelerated inverse of
// wt53Forward2DSeparatedSIMD.  Accepts Mallat subband layout and restores
// the original pixels.
func wt53Inverse2DSeparatedSIMD(data []int32, rows, cols, fullCols int) {
	nColLow := (cols + 1) / 2
	nRowLow := (rows + 1) / 2
	nRowHigh := rows / 2
	colTmp := make([]int32, rows)
	rowTmp := make([]int32, cols)

	// ── Vertical re-interleave ────────────────────────────────────────────
	// Reverse the de-interleave: rows 0..nRowLow-1 → even rows,
	// rows nRowLow..rows-1 → odd rows (use temp to avoid overwrites).
	for x := 0; x < cols; x++ {
		for i := 0; i < nRowLow; i++ {
			colTmp[2*i] = data[i*fullCols+x]
		}
		for i := 0; i < nRowHigh; i++ {
			colTmp[2*i+1] = data[(nRowLow+i)*fullCols+x]
		}
		for i := 0; i < rows; i++ {
			data[i*fullCols+x] = colTmp[i]
		}
	}

	// ── Vertical inverse pass (blocked) ──────────────────────────────────
	const colBlock = 8
	for x0 := 0; x0 < cols; x0 += colBlock {
		xEnd := x0 + colBlock
		if xEnd > cols {
			xEnd = cols
		}
		bw := xEnd - x0

		// Undo update: even[i] -= (dLeft[i] + dRight[i] + 2) >> 2
		// (same coefficients as forward, just subtracted)
		for i := 0; i < nRowLow; i++ {
			evenBase := 2 * i * fullCols

			dRightRow := 2*i + 1
			if dRightRow >= rows {
				dRightRow = 2*i - 1
				if dRightRow < 0 {
					dRightRow = 0
				}
			}
			dRightBase := dRightRow * fullCols

			dLeftRow := 2*i - 1
			if dLeftRow < 0 {
				dLeftRow = dRightRow
			}
			dLeftBase := dLeftRow * fullCols

			// Subtract instead of add: even -= shift
			wt53InvUpdateBlocks(
				unsafe.Pointer(&data[dLeftBase+x0]),
				unsafe.Pointer(&data[dRightBase+x0]),
				unsafe.Pointer(&data[evenBase+x0]),
				bw,
			)
		}

		// Undo predict: odd[i] += (left[i] + right[i]) >> 1
		for i := 0; i < nRowHigh; i++ {
			leftBase := 2 * i * fullCols
			oddBase := (2*i + 1) * fullCols
			rightRow := 2*i + 2
			if rightRow >= rows {
				rightRow = 2 * i
			}
			rightBase := rightRow * fullCols

			wt53InvPredictBlocks(
				unsafe.Pointer(&data[leftBase+x0]),
				unsafe.Pointer(&data[rightBase+x0]),
				unsafe.Pointer(&data[oddBase+x0]),
				bw,
			)
		}
	}

	// ── Horizontal row pass ──────────────────────────────────────────────
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
