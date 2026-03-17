// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	// waveletEscape is the sentinel value in the uint16 stream that signals
	// the next two uint16 words carry the high and low halves of the raw int32
	// coefficient. Coefficients in [-32767, 32767] zigzag-encode to [0, 65534]
	// and are stored directly; anything outside that range uses the escape path.
	waveletEscape = uint16(65535)

	// waveletZZLimit is the maximum absolute coefficient value that fits in a
	// single uint16 after zigzag encoding (zigzag(32767) == 65534).
	waveletZZLimit = 32767
)

// waveletCoeffsToU16 converts int32 wavelet coefficients into a uint16 stream.
// Values in [-32767, 32767] are zigzag-encoded into [0, 65534].
// Out-of-range values are escaped: [65535, high16, low16].
func waveletCoeffsToU16(coeffs []int32) []uint16 {
	out := make([]uint16, 0, len(coeffs)+len(coeffs)/8)
	for _, v := range coeffs {
		if v >= -waveletZZLimit && v <= waveletZZLimit {
			out = append(out, zigzagEncode16(v))
		} else {
			out = append(out, waveletEscape)
			u := uint32(v)
			out = append(out, uint16(u>>16), uint16(u))
		}
	}
	return out
}

// u16ToWaveletCoeffs converts a uint16 stream back to int32 wavelet coefficients.
func u16ToWaveletCoeffs(in []uint16, n int) []int32 {
	out := make([]int32, 0, n)
	i := 0
	for i < len(in) && len(out) < n {
		if in[i] != waveletEscape {
			out = append(out, zigzagDecode16(in[i]))
			i++
		} else {
			i++ // skip escape
			v := int32(uint32(in[i])<<16 | uint32(in[i+1]))
			out = append(out, v)
			i += 2
		}
	}
	return out
}

// WaveletFSECompressU16 compresses 16-bit image data using:
//
//	2D 5/3 integer wavelet transform -> ZigZag encoding (with escape) -> FSE
//
// The compressed format is:
//
//	[4 bytes] rows (little-endian uint32)
//	[4 bytes] cols (little-endian uint32)
//	[2 bytes] maxValue (little-endian uint16)
//	[1 byte]  levels (number of wavelet decomposition levels)
//	[rest]    FSE-compressed zigzag-encoded wavelet coefficients
func WaveletFSECompressU16(pixels []uint16, rows, cols int, maxValue uint16, levels int) ([]byte, error) {
	if len(pixels) != rows*cols {
		return nil, errors.New("pixel count does not match rows*cols")
	}
	if levels < 1 {
		levels = 1
	}
	if levels > 4 {
		levels = 4
	}

	// Convert uint16 to int32 for wavelet transform
	data := make([]int32, len(pixels))
	for i, v := range pixels {
		data[i] = int32(v)
	}

	// Apply multi-level 2D wavelet transform
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		if r < 2 || c < 2 {
			levels = l
			break
		}
		waveletForward2DRegion(data, r, c, cols)
		r = (r + 1) / 2
		c = (c + 1) / 2
	}

	// Encode coefficients to uint16 stream with overflow escape
	encoded := waveletCoeffsToU16(data)

	// FSE compress (4-state for better decompression throughput)
	var s ScratchU16
	fseOut, err := FSECompressU16FourState(encoded, &s)
	if err != nil {
		return nil, err
	}

	// Build output: header + FSE data
	header := make([]byte, 11)
	binary.LittleEndian.PutUint32(header[0:4], uint32(rows))
	binary.LittleEndian.PutUint32(header[4:8], uint32(cols))
	binary.LittleEndian.PutUint16(header[8:10], maxValue)
	header[10] = byte(levels)

	out := make([]byte, len(header)+len(fseOut))
	copy(out, header)
	copy(out[len(header):], fseOut)
	return out, nil
}

// WaveletFSEDecompressU16 decompresses data produced by WaveletFSECompressU16.
func WaveletFSEDecompressU16(compressed []byte) ([]uint16, int, int, error) {
	if len(compressed) < 11 {
		return nil, 0, 0, errors.New("compressed data too short")
	}

	rows := int(binary.LittleEndian.Uint32(compressed[0:4]))
	cols := int(binary.LittleEndian.Uint32(compressed[4:8]))
	_ = binary.LittleEndian.Uint16(compressed[8:10]) // maxValue (for future use)
	levels := int(compressed[10])

	// FSE decompress (4-state)
	var s ScratchU16
	encoded, err := FSEDecompressU16FourState(compressed[11:], &s)
	if err != nil {
		return nil, 0, 0, err
	}

	// Decode uint16 stream back to int32 wavelet coefficients
	data := u16ToWaveletCoeffs(encoded, rows*cols)

	// Inverse multi-level 2D wavelet transform (from coarsest to finest)
	dims := make([][2]int, levels)
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		dims[l] = [2]int{r, c}
		r = (r + 1) / 2
		c = (c + 1) / 2
	}
	for l := levels - 1; l >= 0; l-- {
		waveletInverse2DRegion(data, dims[l][0], dims[l][1], cols)
	}

	// Convert int32 back to uint16
	pixels := make([]uint16, len(data))
	for i, v := range data {
		pixels[i] = uint16(v)
	}

	return pixels, rows, cols, nil
}

// waveletForward2DRegion applies the wavelet transform to the top-left
// region of size rows x cols in a buffer with the given stride (full cols).
func waveletForward2DRegion(data []int32, rows, cols, stride int) {
	// Horizontal transform: each row
	for y := 0; y < rows; y++ {
		wt53Forward1D(data, y*stride, cols, 1)
	}
	// Vertical transform: each column
	for x := 0; x < cols; x++ {
		wt53Forward1D(data, x, rows, stride)
	}
}

// waveletInverse2DRegion applies the inverse wavelet transform to the top-left
// region of size rows x cols in a buffer with the given stride (full cols).
func waveletInverse2DRegion(data []int32, rows, cols, stride int) {
	// Inverse vertical transform: each column
	for x := 0; x < cols; x++ {
		wt53Inverse1D(data, x, rows, stride)
	}
	// Inverse horizontal transform: each row
	for y := 0; y < rows; y++ {
		wt53Inverse1D(data, y*stride, cols, 1)
	}
}

// collectSubbandOrder gathers coefficients from the Mallat layout into a flat
// slice in subband-scan order: LL (coarsest) → then for each level from
// coarsest to finest: HL, LH, HH.  This groups near-zero detail coefficients
// together, improving RLE efficiency.
//
// Parameters:
//
//	data     – rows×cols buffer in Mallat subband layout (stride = fullCols)
//	rows, cols – image dimensions
//	fullCols – row stride of the data buffer
//	levels   – number of decomposition levels applied
func collectSubbandOrder(data []int32, rows, cols, fullCols, levels int) []int32 {
	nR := make([]int, levels+1)
	nC := make([]int, levels+1)
	nR[0] = rows
	nC[0] = cols
	for l := 1; l <= levels; l++ {
		nR[l] = (nR[l-1] + 1) / 2
		nC[l] = (nC[l-1] + 1) / 2
	}

	out := make([]int32, 0, rows*cols)
	// Scan LL at the coarsest level
	for y := 0; y < nR[levels]; y++ {
		for x := 0; x < nC[levels]; x++ {
			out = append(out, data[y*fullCols+x])
		}
	}
	// Scan detail subbands level by level, coarsest first
	for l := levels; l >= 1; l-- {
		// HL_l: rows [0, nR[l]),  cols [nC[l], nC[l-1])
		for y := 0; y < nR[l]; y++ {
			for x := nC[l]; x < nC[l-1]; x++ {
				out = append(out, data[y*fullCols+x])
			}
		}
		// LH_l: rows [nR[l], nR[l-1]),  cols [0, nC[l])
		for y := nR[l]; y < nR[l-1]; y++ {
			for x := 0; x < nC[l]; x++ {
				out = append(out, data[y*fullCols+x])
			}
		}
		// HH_l: rows [nR[l], nR[l-1]),  cols [nC[l], nC[l-1])
		for y := nR[l]; y < nR[l-1]; y++ {
			for x := nC[l]; x < nC[l-1]; x++ {
				out = append(out, data[y*fullCols+x])
			}
		}
	}
	return out
}

// scatterSubbandOrder is the inverse of collectSubbandOrder: it writes a flat
// slice (in subband-scan order) back into the Mallat layout buffer.
func scatterSubbandOrder(linear []int32, data []int32, rows, cols, fullCols, levels int) {
	nR := make([]int, levels+1)
	nC := make([]int, levels+1)
	nR[0] = rows
	nC[0] = cols
	for l := 1; l <= levels; l++ {
		nR[l] = (nR[l-1] + 1) / 2
		nC[l] = (nC[l-1] + 1) / 2
	}

	pos := 0
	for y := 0; y < nR[levels]; y++ {
		for x := 0; x < nC[levels]; x++ {
			data[y*fullCols+x] = linear[pos]
			pos++
		}
	}
	for l := levels; l >= 1; l-- {
		for y := 0; y < nR[l]; y++ {
			for x := nC[l]; x < nC[l-1]; x++ {
				data[y*fullCols+x] = linear[pos]
				pos++
			}
		}
		for y := nR[l]; y < nR[l-1]; y++ {
			for x := 0; x < nC[l]; x++ {
				data[y*fullCols+x] = linear[pos]
				pos++
			}
		}
		for y := nR[l]; y < nR[l-1]; y++ {
			for x := nC[l]; x < nC[l-1]; x++ {
				data[y*fullCols+x] = linear[pos]
				pos++
			}
		}
	}
}

// WaveletV2RLEFSECompressU16 compresses 16-bit image data using the improved
// wavelet pipeline:
//
//	2D 5/3 integer wavelet (Mallat/separated layout, default 5 levels)
//	→ subband-order scan (LL first, then detail bands coarsest→finest)
//	→ ZigZag encoding with int32 escape for large coefficients
//	→ RLE → FSE
//
// The separated subband layout ensures multi-level transforms are correct
// (each subsequent level operates only on the contiguous LL region), and
// the subband scan order groups near-zero detail coefficients for better RLE.
//
// Compressed format:
//
//	[4 bytes] rows (uint32 LE)
//	[4 bytes] cols (uint32 LE)
//	[2 bytes] maxValue (uint16 LE)
//	[1 byte]  levels
//	[rest]    FSE-compressed RLE stream
func WaveletV2RLEFSECompressU16(pixels []uint16, rows, cols int, maxValue uint16, levels int) ([]byte, error) {
	if len(pixels) != rows*cols {
		return nil, errors.New("pixel count does not match rows*cols")
	}
	if levels < 1 {
		levels = 1
	}
	if levels > 8 {
		levels = 8
	}

	// Convert uint16 → int32 for the wavelet transform
	data := make([]int32, len(pixels))
	for i, v := range pixels {
		data[i] = int32(v)
	}

	// Multi-level forward transform using separated (Mallat) layout
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		if r < 2 || c < 2 {
			levels = l
			break
		}
		wt53Forward2DSeparated(data, r, c, cols)
		r = (r + 1) / 2
		c = (c + 1) / 2
	}

	// Collect coefficients in subband-scan order for better RLE
	ordered := collectSubbandOrder(data, rows, cols, cols, levels)

	// ZigZag-encode signed coefficients → uint16 stream with escape for large values
	encoded := waveletCoeffsToU16(ordered)

	// Determine max symbol for RLE init
	zzMax := uint16(0)
	for _, v := range encoded {
		if v > zzMax {
			zzMax = v
		}
	}
	pixelDepth := bits.Len16(zzMax)
	if pixelDepth < 1 {
		pixelDepth = 1
	}
	rleMaxVal := uint16((1 << pixelDepth) - 1)
	var rleC RleCompressU16
	rleC.Init(len(encoded), 1, rleMaxVal)
	rleOut := rleC.Compress(encoded)

	// FSE compress (4-state for better decompression throughput)
	var s ScratchU16
	fseOut, err := FSECompressU16FourState(rleOut, &s)
	if err != nil {
		return nil, err
	}

	header := make([]byte, 11)
	binary.LittleEndian.PutUint32(header[0:4], uint32(rows))
	binary.LittleEndian.PutUint32(header[4:8], uint32(cols))
	binary.LittleEndian.PutUint16(header[8:10], maxValue)
	header[10] = byte(levels)

	out := make([]byte, len(header)+len(fseOut))
	copy(out, header)
	copy(out[len(header):], fseOut)
	return out, nil
}

// WaveletV2RLEFSEDecompressU16 decompresses data produced by WaveletV2RLEFSECompressU16.
func WaveletV2RLEFSEDecompressU16(compressed []byte) ([]uint16, int, int, error) {
	if len(compressed) < 11 {
		return nil, 0, 0, errors.New("compressed data too short")
	}

	rows := int(binary.LittleEndian.Uint32(compressed[0:4]))
	cols := int(binary.LittleEndian.Uint32(compressed[4:8]))
	_ = binary.LittleEndian.Uint16(compressed[8:10]) // maxValue
	levels := int(compressed[10])

	// FSE decompress (4-state)
	var s ScratchU16
	fseOut, err := FSEDecompressU16FourState(compressed[11:], &s)
	if err != nil {
		return nil, 0, 0, err
	}

	// RLE decompress
	var rleD RleDecompressU16
	rleD.Init(fseOut)
	encoded := rleD.Decompress()

	// Decode ZigZag+escape uint16 stream back to int32 coefficients
	ordered := u16ToWaveletCoeffs(encoded, rows*cols)

	// Scatter back into Mallat layout
	data := make([]int32, rows*cols)
	scatterSubbandOrder(ordered, data, rows, cols, cols, levels)

	// Multi-level inverse transform (coarsest → finest)
	dims := make([][2]int, levels)
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		dims[l] = [2]int{r, c}
		r = (r + 1) / 2
		c = (c + 1) / 2
	}
	for l := levels - 1; l >= 0; l-- {
		wt53Inverse2DSeparated(data, dims[l][0], dims[l][1], cols)
	}

	// Convert int32 → uint16
	pixels := make([]uint16, len(data))
	for i, v := range data {
		pixels[i] = uint16(v)
	}
	return pixels, rows, cols, nil
}

// WaveletV2SIMDRLEFSECompressU16 is identical to WaveletV2RLEFSECompressU16
// but uses the SIMD-accelerated (AVX2 on AMD64) wavelet transform.
// The compressed stream is bit-compatible with WaveletV2RLEFSECompressU16;
// only the transform kernel differs.
func WaveletV2SIMDRLEFSECompressU16(pixels []uint16, rows, cols int, maxValue uint16, levels int) ([]byte, error) {
	if len(pixels) != rows*cols {
		return nil, errors.New("pixel count does not match rows*cols")
	}
	if levels < 1 {
		levels = 1
	}
	if levels > 8 {
		levels = 8
	}

	data := make([]int32, len(pixels))
	for i, v := range pixels {
		data[i] = int32(v)
	}

	r, c := rows, cols
	for l := 0; l < levels; l++ {
		if r < 2 || c < 2 {
			levels = l
			break
		}
		wt53Forward2DSeparatedSIMD(data, r, c, cols)
		r = (r + 1) / 2
		c = (c + 1) / 2
	}

	ordered := collectSubbandOrder(data, rows, cols, cols, levels)
	encoded := waveletCoeffsToU16(ordered)

	zzMax := uint16(0)
	for _, v := range encoded {
		if v > zzMax {
			zzMax = v
		}
	}
	pixelDepth := bits.Len16(zzMax)
	if pixelDepth < 1 {
		pixelDepth = 1
	}
	rleMaxVal := uint16((1 << pixelDepth) - 1)
	var rleC RleCompressU16
	rleC.Init(len(encoded), 1, rleMaxVal)
	rleOut := rleC.Compress(encoded)

	var s ScratchU16
	fseOut, err := FSECompressU16FourState(rleOut, &s)
	if err != nil {
		return nil, err
	}

	header := make([]byte, 11)
	binary.LittleEndian.PutUint32(header[0:4], uint32(rows))
	binary.LittleEndian.PutUint32(header[4:8], uint32(cols))
	binary.LittleEndian.PutUint16(header[8:10], maxValue)
	header[10] = byte(levels)

	out := make([]byte, len(header)+len(fseOut))
	copy(out, header)
	copy(out[len(header):], fseOut)
	return out, nil
}

// WaveletV2SIMDRLEFSEDecompressU16 decompresses data produced by either
// WaveletV2RLEFSECompressU16 or WaveletV2SIMDRLEFSECompressU16.
// Uses the SIMD-accelerated inverse wavelet transform.
func WaveletV2SIMDRLEFSEDecompressU16(compressed []byte) ([]uint16, int, int, error) {
	if len(compressed) < 11 {
		return nil, 0, 0, errors.New("compressed data too short")
	}

	rows := int(binary.LittleEndian.Uint32(compressed[0:4]))
	cols := int(binary.LittleEndian.Uint32(compressed[4:8]))
	_ = binary.LittleEndian.Uint16(compressed[8:10])
	levels := int(compressed[10])

	var s ScratchU16
	fseOut, err := FSEDecompressU16FourState(compressed[11:], &s)
	if err != nil {
		return nil, 0, 0, err
	}

	var rleD RleDecompressU16
	rleD.Init(fseOut)
	encoded := rleD.Decompress()

	ordered := u16ToWaveletCoeffs(encoded, rows*cols)

	data := make([]int32, rows*cols)
	scatterSubbandOrder(ordered, data, rows, cols, cols, levels)

	dims := make([][2]int, levels)
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		dims[l] = [2]int{r, c}
		r = (r + 1) / 2
		c = (c + 1) / 2
	}
	for l := levels - 1; l >= 0; l-- {
		wt53Inverse2DSeparatedSIMD(data, dims[l][0], dims[l][1], cols)
	}

	pixels := make([]uint16, len(data))
	for i, v := range data {
		pixels[i] = uint16(v)
	}
	return pixels, rows, cols, nil
}

// zigzagEncode16 maps a signed int32 to an unsigned uint16 using ZigZag encoding.
// Caller must ensure v is in [-32767, 32767] so the result fits in uint16.
func zigzagEncode16(v int32) uint16 {
	return uint16((v >> 31) ^ (v << 1))
}

// zigzagDecode16 reverses zigzag encoding.
func zigzagDecode16(v uint16) int32 {
	u := uint32(v)
	return int32((u >> 1) ^ -(u & 1))
}

// WaveletRLEFSECompressU16 compresses 16-bit image data using:
//
//	2D 5/3 integer wavelet transform -> ZigZag encoding (with escape) -> RLE -> FSE
func WaveletRLEFSECompressU16(pixels []uint16, rows, cols int, maxValue uint16, levels int) ([]byte, error) {
	if len(pixels) != rows*cols {
		return nil, errors.New("pixel count does not match rows*cols")
	}
	if levels < 1 {
		levels = 1
	}
	if levels > 4 {
		levels = 4
	}

	// Convert uint16 to int32
	data := make([]int32, len(pixels))
	for i, v := range pixels {
		data[i] = int32(v)
	}

	// Apply multi-level 2D wavelet transform
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		if r < 2 || c < 2 {
			levels = l
			break
		}
		waveletForward2DRegion(data, r, c, cols)
		r = (r + 1) / 2
		c = (c + 1) / 2
	}

	// Encode coefficients to uint16 stream with overflow escape
	encoded := waveletCoeffsToU16(data)

	// Find max value in encoded stream for RLE
	zzMax := uint16(0)
	for _, v := range encoded {
		if v > zzMax {
			zzMax = v
		}
	}

	// RLE compress
	pixelDepth := bits.Len16(zzMax)
	if pixelDepth < 1 {
		pixelDepth = 1
	}
	rleMaxVal := uint16((1 << pixelDepth) - 1)
	var rleC RleCompressU16
	rleC.Init(len(encoded), 1, rleMaxVal)
	rleOut := rleC.Compress(encoded)

	// FSE compress (4-state for better decompression throughput)
	var s ScratchU16
	fseOut, err := FSECompressU16FourState(rleOut, &s)
	if err != nil {
		return nil, err
	}

	// Build output: header + FSE data
	header := make([]byte, 15)
	binary.LittleEndian.PutUint32(header[0:4], uint32(rows))
	binary.LittleEndian.PutUint32(header[4:8], uint32(cols))
	binary.LittleEndian.PutUint16(header[8:10], maxValue)
	header[10] = byte(levels)
	// Store encoded length so decompressor knows how many coefficients to expect
	binary.LittleEndian.PutUint32(header[11:15], uint32(len(encoded)))

	out := make([]byte, len(header)+len(fseOut))
	copy(out, header)
	copy(out[len(header):], fseOut)
	return out, nil
}

// WaveletRLEFSEDecompressU16 decompresses data produced by WaveletRLEFSECompressU16.
func WaveletRLEFSEDecompressU16(compressed []byte) ([]uint16, int, int, error) {
	if len(compressed) < 15 {
		return nil, 0, 0, errors.New("compressed data too short")
	}

	rows := int(binary.LittleEndian.Uint32(compressed[0:4]))
	cols := int(binary.LittleEndian.Uint32(compressed[4:8]))
	_ = binary.LittleEndian.Uint16(compressed[8:10]) // maxValue
	levels := int(compressed[10])
	_ = int(binary.LittleEndian.Uint32(compressed[11:15])) // encodedLen (RLE handles its own length)

	// FSE decompress (4-state)
	var s ScratchU16
	fseOut, err := FSEDecompressU16FourState(compressed[15:], &s)
	if err != nil {
		return nil, 0, 0, err
	}

	// RLE decompress
	var rleD RleDecompressU16
	rleD.Init(fseOut)
	encoded := rleD.Decompress()

	// Decode uint16 stream back to int32 wavelet coefficients
	data := u16ToWaveletCoeffs(encoded, rows*cols)

	// Inverse multi-level 2D wavelet transform
	dims := make([][2]int, levels)
	r, c := rows, cols
	for l := 0; l < levels; l++ {
		dims[l] = [2]int{r, c}
		r = (r + 1) / 2
		c = (c + 1) / 2
	}
	for l := levels - 1; l >= 0; l-- {
		waveletInverse2DRegion(data, dims[l][0], dims[l][1], cols)
	}

	// Convert int32 back to uint16
	pixels := make([]uint16, len(data))
	for i, v := range data {
		pixels[i] = uint16(v)
	}

	return pixels, rows, cols, nil
}
