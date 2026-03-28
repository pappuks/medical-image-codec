// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_ojph

// mic_c.go — CGO bindings for the C implementation of MIC decompression.
// This allows benchmarking the MIC pipeline in C vs Go vs HTJ2K.
package ojph

/*
#cgo CFLAGS: -O3
#cgo amd64 CFLAGS: -march=native
#cgo LDFLAGS: -lpthread
#include "mic_decompress_c.h"
#include "mic_compress_c.h"
#include "mic_parallel.h"
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

// MICDecompressParallelC decompresses a PICS blob (produced by
// mic.CompressParallelStrips) using C pthreads.  Each horizontal strip is
// decompressed concurrently by mic_decompress_four_state_simd (AMD64) or
// mic_decompress_four_state (ARM64 / other).
//
// maxThreads controls the pthread pool size; 0 = one thread per strip.
func MICDecompressParallelC(pics []byte, width, height, maxThreads int) ([]uint16, error) {
	pixels := make([]uint16, width*height)

	rc := C.mic_decompress_parallel(
		(*C.uint8_t)(unsafe.Pointer(&pics[0])),
		C.size_t(len(pics)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
		C.int(maxThreads),
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_decompress_parallel failed: rc=%d", rc)
	}
	return pixels, nil
}

// MICDecompressParallelScalarC is the same as MICDecompressParallelC but uses
// the scalar four-state inner decoder even on AMD64.  Useful for isolating the
// thread-level speedup from the SIMD speedup in benchmarks.
func MICDecompressParallelScalarC(pics []byte, width, height, maxThreads int) ([]uint16, error) {
	pixels := make([]uint16, width*height)

	rc := C.mic_decompress_parallel_scalar(
		(*C.uint8_t)(unsafe.Pointer(&pics[0])),
		C.size_t(len(pics)),
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
		C.int(maxThreads),
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_decompress_parallel_scalar failed: rc=%d", rc)
	}
	return pixels, nil
}

// MICCompressFourStateC compresses 16-bit pixels using the full C pipeline:
// Delta encode → RLE encode → FSE four-state encode.
// The output is compatible with MICDecompressFourStateC and MICDecompressFourStateSIMD.
func MICCompressFourStateC(pixels []uint16, width, height int) ([]byte, error) {
	outCap := len(pixels)*2*2 + 4096
	out := make([]byte, outCap)
	var outLen C.size_t

	rc := C.mic_compress_four_state(
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
		C.size_t(outCap),
		&outLen,
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_compress_four_state failed: rc=%d", rc)
	}
	return out[:outLen], nil
}

// MICCompressTwoStateC compresses 16-bit pixels using the C two-state pipeline.
// The output is compatible with MICDecompressTwoStateC and MICDecompressTwoStateSIMD.
func MICCompressTwoStateC(pixels []uint16, width, height int) ([]byte, error) {
	outCap := len(pixels)*2*2 + 4096
	out := make([]byte, outCap)
	var outLen C.size_t

	rc := C.mic_compress_two_state(
		(*C.uint16_t)(unsafe.Pointer(&pixels[0])),
		C.int(width), C.int(height),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
		C.size_t(outCap),
		&outLen,
	)
	if rc != 0 {
		return nil, fmt.Errorf("mic_compress_two_state failed: rc=%d", rc)
	}
	return out[:outLen], nil
}
