// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// Downsample2xRGB reduces an RGB image by half in each dimension using a 2x2 box filter.
// Each channel is averaged independently. Odd trailing pixels are dropped.
// Returns the downsampled image and its dimensions.
func Downsample2xRGB(src []byte, width, height int) ([]byte, int, int) {
	newW := width / 2
	newH := height / 2
	if newW == 0 || newH == 0 {
		return nil, 0, 0
	}
	dst := make([]byte, newW*newH*3)

	for y := 0; y < newH; y++ {
		srcY := y * 2
		for x := 0; x < newW; x++ {
			srcX := x * 2
			for c := 0; c < 3; c++ {
				v00 := int(src[(srcY*width+srcX)*3+c])
				v10 := int(src[(srcY*width+srcX+1)*3+c])
				v01 := int(src[((srcY+1)*width+srcX)*3+c])
				v11 := int(src[((srcY+1)*width+srcX+1)*3+c])
				dst[(y*newW+x)*3+c] = byte((v00 + v10 + v01 + v11 + 2) / 4)
			}
		}
	}
	return dst, newW, newH
}

// Downsample2xGrey reduces a greyscale uint16 image by half using a 2x2 box filter.
func Downsample2xGrey(src []uint16, width, height int) ([]uint16, int, int) {
	newW := width / 2
	newH := height / 2
	if newW == 0 || newH == 0 {
		return nil, 0, 0
	}
	dst := make([]uint16, newW*newH)

	for y := 0; y < newH; y++ {
		srcY := y * 2
		for x := 0; x < newW; x++ {
			srcX := x * 2
			v00 := uint32(src[srcY*width+srcX])
			v10 := uint32(src[srcY*width+srcX+1])
			v01 := uint32(src[(srcY+1)*width+srcX])
			v11 := uint32(src[(srcY+1)*width+srcX+1])
			dst[y*newW+x] = uint16((v00 + v10 + v01 + v11 + 2) / 4)
		}
	}
	return dst, newW, newH
}
