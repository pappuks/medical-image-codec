// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// CompressRGB compresses an 8-bit interleaved RGB image losslessly.
//
// The pipeline applies a YCoCg-R reversible color transform to decorrelate
// the R/G/B channels, then compresses each resulting plane independently
// using Delta+RLE+FSE (the same pipeline used for greyscale images).
//
// Input: RGBRGB... interleaved bytes of exactly width*height*3 length.
// Output: a self-contained blob that must be passed together with the original
// width and height to DecompressRGB.
//
// Format (same as WSI tile blobs):
//
//	[Y_len  uint32 LE]
//	[Co_len uint32 LE]
//	[Cg_len uint32 LE]
//	[Y  plane blob  ]  (planeConstantZero | planeConstant | planeCompressed | planeRaw)
//	[Co plane blob  ]
//	[Cg plane blob  ]
func CompressRGB(rgb []byte, width, height int) ([]byte, error) {
	return compressRGBTileBlob(rgb, width, height, true)
}

// DecompressRGB decompresses a blob produced by CompressRGB.
// Returns interleaved RGB bytes of width*height*3 length.
func DecompressRGB(data []byte, width, height int) ([]byte, error) {
	return decompressRGBTileBlob(data, width, height, true)
}
