// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

// Package ojph provides CGO bindings for OpenJPH in-process HTJ2K compress/decompress.
// This eliminates subprocess overhead for fair benchmarking against MIC.
//
// Requires libopenjph and libojphwrapper installed in /usr/local/lib.
// Build with: go test -tags cgo_ojph
package ojph

/*
#cgo CXXFLAGS: -O2 -I/usr/local/include
#cgo LDFLAGS: -L/usr/local/lib -lopenjph -lstdc++
#include <stdint.h>
#include <stddef.h>

// ojph_compress_u16 compresses a 16-bit grayscale image using HTJ2K lossless.
int ojph_compress_u16(const uint16_t *pixels, int width, int height,
                      int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                      size_t *out_len);

// ojph_decompress_u16 decompresses an HTJ2K lossless codestream to 16-bit pixels.
int ojph_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                        uint16_t *pixels_out, size_t pixels_buf_size,
                        int width, int height);
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// CompressU16 compresses a 16-bit grayscale image using HTJ2K lossless (in-process).
func CompressU16(pixels []uint16, width, height, bitDepth int) ([]byte, error) {
	outBufSize := len(pixels)*2 + 65536
	outBuf := make([]byte, outBufSize)
	var outLen C.size_t

	rc := C.ojph_compress_u16(
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height), C.int(bitDepth),
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
		C.size_t(outBufSize),
		&outLen,
	)
	if rc != 0 {
		return nil, fmt.Errorf("ojph_compress_u16 failed: rc=%d", rc)
	}
	return outBuf[:outLen], nil
}

// DecompressU16 decompresses an HTJ2K lossless codestream to 16-bit pixels (in-process).
func DecompressU16(compressed []byte, width, height int) ([]uint16, error) {
	pixelCount := width * height
	pixels := make([]uint16, pixelCount)
	bufSize := pixelCount * 2

	rc := C.ojph_decompress_u16(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.size_t(bufSize),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("ojph_decompress_u16 failed: rc=%d", rc)
	}
	return pixels, nil
}
