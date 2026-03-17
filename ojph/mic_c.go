// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// mic_c.go — CGO bindings for the C implementation of MIC decompression.
// This allows benchmarking the MIC pipeline in C vs Go vs HTJ2K.
package ojph

/*
#cgo CFLAGS: -O2
#cgo amd64 CFLAGS: -msse2 -mavx2
#include "mic_decompress_c.h"
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// MICDecompressTwoStateC decompresses a MIC two-state FSE stream using the C implementation.
func MICDecompressTwoStateC(compressed []byte, width, height int) ([]uint16, error) {
	pixels := make([]uint16, width*height)

	rc := C.mic_decompress_two_state(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_decompress_two_state failed: rc=%d", rc)
	}
	return pixels, nil
}

// MICDecompressTwoStateSIMD decompresses using the SIMD-optimized C implementation.
func MICDecompressTwoStateSIMD(compressed []byte, width, height int) ([]uint16, error) {
	pixels := make([]uint16, width*height)

	rc := C.mic_decompress_two_state_simd(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_decompress_two_state_simd failed: rc=%d", rc)
	}
	return pixels, nil
}

// MICDecompressFourStateC decompresses a MIC four-state FSE stream using the C implementation.
func MICDecompressFourStateC(compressed []byte, width, height int) ([]uint16, error) {
	pixels := make([]uint16, width*height)

	rc := C.mic_decompress_four_state(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_decompress_four_state failed: rc=%d", rc)
	}
	return pixels, nil
}

// MICDecompressFourStateSIMD decompresses using the SIMD-optimized C four-state implementation.
func MICDecompressFourStateSIMD(compressed []byte, width, height int) ([]uint16, error) {
	pixels := make([]uint16, width*height)

	rc := C.mic_decompress_four_state_simd(
		(*C.uint8_t)(unsafe.Pointer(&compressed[0])),
		C.size_t(len(compressed)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_decompress_four_state_simd failed: rc=%d", rc)
	}
	return pixels, nil
}
