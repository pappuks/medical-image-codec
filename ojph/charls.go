// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// charls.go — CGO bindings for CharLS in-process JPEG-LS compress/decompress.
// Requires libcharls installed in /usr/local/lib.
// Build with: go test -tags cgo_ojph
package ojph

/*
#cgo CXXFLAGS: -O2 -I/usr/local/include -I/home/dibba/.local/include -I/opt/homebrew/include
#cgo LDFLAGS: -L/usr/local/lib -L/home/dibba/.local/lib -L/opt/homebrew/lib -lcharls -lstdc++ -Wl,-rpath,/usr/local/lib -Wl,-rpath,/home/dibba/.local/lib -Wl,-rpath,/opt/homebrew/lib
#include <stdint.h>
#include <stddef.h>

// charls_compress_u16 compresses a 16-bit grayscale image using JPEG-LS lossless.
int charls_compress_u16(const uint16_t *pixels, int width, int height,
                        int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                        size_t *out_len);

// charls_decompress_u16 decompresses a JPEG-LS lossless stream to 16-bit pixels.
int charls_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                          uint16_t *pixels_out, size_t pixels_buf_size,
                          int width, int height);
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// CharlsCompressU16 compresses a 16-bit grayscale image using JPEG-LS lossless (in-process).
func CharlsCompressU16(pixels []uint16, width, height, bitDepth int) ([]byte, error) {
	outBufSize := len(pixels)*2 + 65536
	outBuf := make([]byte, outBufSize)
	var outLen C.size_t

	rc := C.charls_compress_u16(
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height), C.int(bitDepth),
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
		C.size_t(outBufSize),
		&outLen,
	)
	if rc != 0 {
		return nil, fmt.Errorf("charls_compress_u16 failed: rc=%d", rc)
	}
	return outBuf[:outLen], nil
}

// CharlsDecompressU16 decompresses a JPEG-LS lossless stream to 16-bit pixels (in-process).
func CharlsDecompressU16(compressed []byte, width, height int) ([]uint16, error) {
	pixelCount := width * height
	pixels := make([]uint16, pixelCount)
	bufSize := pixelCount * 2

	rc := C.charls_decompress_u16(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.size_t(bufSize),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("charls_decompress_u16 failed: rc=%d", rc)
	}
	return pixels, nil
}
