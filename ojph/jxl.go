// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// jxl.go — CGO bindings for libjxl in-process JPEG XL lossless
// compress/decompress. Requires libjxl installed (brew install jpeg-xl).
// Build with: go test -tags cgo_ojph
package ojph

/*
#cgo CFLAGS: -O2 -I/usr/local/include -I/home/dibba/.local/include -I/opt/homebrew/include
#cgo LDFLAGS: -L/usr/local/lib -L/home/dibba/.local/lib -L/opt/homebrew/lib -ljxl -Wl,-rpath,/usr/local/lib -Wl,-rpath,/home/dibba/.local/lib -Wl,-rpath,/opt/homebrew/lib
#include <stdint.h>
#include <stddef.h>
#include "jxl_wrapper.h"
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// JXLDefaultEffort is the libjxl encoder effort used by these benchmarks.
// libjxl's own default is 7; we pin it so results are reproducible.
const JXLDefaultEffort = 7

// JXLCompressU16 compresses a 16-bit grayscale image with JPEG XL lossless
// (in-process). effort selects the libjxl encoder effort (1..9); pass
// JXLDefaultEffort for the standard setting.
func JXLCompressU16(pixels []uint16, width, height, bitDepth, effort int) ([]byte, error) {
	// Worst case JXL can expand slightly on incompressible data; give headroom.
	outBufSize := len(pixels)*2 + 65536
	outBuf := make([]byte, outBufSize)
	var outLen C.size_t

	rc := C.jxl_compress_u16(
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height), C.int(bitDepth), C.int(effort),
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
		C.size_t(outBufSize),
		&outLen,
	)
	if rc != 0 {
		return nil, fmt.Errorf("jxl_compress_u16 failed: rc=%d", int(rc))
	}
	return outBuf[:outLen], nil
}

// JXLDecompressU16 decompresses a JPEG XL stream into 16-bit pixels (in-process).
func JXLDecompressU16(compressed []byte, width, height int) ([]uint16, error) {
	pixelCount := width * height
	pixels := make([]uint16, pixelCount)
	bufSize := pixelCount * 2

	rc := C.jxl_decompress_u16(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.size_t(bufSize),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("jxl_decompress_u16 failed: rc=%d", int(rc))
	}
	return pixels, nil
}
