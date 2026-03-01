// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// YCoCg-R reversible color transform for lossless RGB compression.
//
// Decorrelates RGB into luminance (Y) and two chrominance planes (Co, Cg).
// The transform is perfectly reversible for integer inputs.
//
// Y range:  [0, 255] for 8-bit RGB input
// Co range: [-255, 255] -> ZigZag maps to [0, 510]
// Cg range: [-255, 255] -> ZigZag maps to [0, 510]

// YCoCgRForward converts interleaved RGB pixels to separate Y, Co, Cg planes.
// Input: []byte of length width*height*3 (RGBRGB...).
// Output: three []uint16 planes. Co and Cg are ZigZag-encoded to unsigned.
func YCoCgRForward(rgb []byte, width, height int) (y, co, cg []uint16) {
	n := width * height
	y = make([]uint16, n)
	co = make([]uint16, n)
	cg = make([]uint16, n)

	for i := 0; i < n; i++ {
		r := int(rgb[i*3])
		g := int(rgb[i*3+1])
		b := int(rgb[i*3+2])

		coVal := r - b
		t := b + (coVal >> 1) // arithmetic right shift preserves sign
		cgVal := g - t
		yVal := t + (cgVal >> 1)

		y[i] = uint16(yVal)
		co[i] = ZigZag(int16(coVal))
		cg[i] = ZigZag(int16(cgVal))
	}

	return
}

// YCoCgRInverse converts Y, Co, Cg planes back to interleaved RGB.
// Co and Cg must be ZigZag-encoded unsigned values.
func YCoCgRInverse(y, co, cg []uint16, width, height int) []byte {
	n := width * height
	rgb := make([]byte, n*3)

	for i := 0; i < n; i++ {
		yVal := int(y[i])
		coVal := int(UnZigZag(co[i]))
		cgVal := int(UnZigZag(cg[i]))

		t := yVal - (cgVal >> 1)
		g := cgVal + t
		b := t - (coVal >> 1)
		r := coVal + b

		rgb[i*3] = byte(r)
		rgb[i*3+1] = byte(g)
		rgb[i*3+2] = byte(b)
	}

	return rgb
}
