// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_zstd

// Package zstd provides CGO bindings to libzstd for in-process Zstandard
// compress/decompress. Mirrors the pattern used in the ojph/ package and
// exists so the paper's Delta+Zstandard baseline can be benchmarked without
// the subprocess/CLI overhead of comparison_test.go:BenchmarkDeltaZstdDecompress.
//
// Build with: go test -tags cgo_zstd ./zstd/
package zstd

/*
#cgo darwin CFLAGS: -O3 -I/opt/homebrew/opt/zstd/include -I/opt/homebrew/include -I/usr/local/include
#cgo darwin LDFLAGS: -L/opt/homebrew/opt/zstd/lib -L/opt/homebrew/lib -L/usr/local/lib -lzstd
#cgo linux  CFLAGS: -O3 -march=native -I/usr/local/include -I/home/dibba/.local/include
#cgo linux  LDFLAGS: -L/usr/local/lib -L/home/dibba/.local/lib -lzstd -Wl,-rpath,/usr/local/lib -Wl,-rpath,/home/dibba/.local/lib

#include <stddef.h>
#include <stdint.h>
#include <zstd.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Compress compresses src with libzstd at the given compression level.
// Level matches the libzstd convention: 1..22 with 19 being the level used
// in the paper's Delta+Zstandard column (see comparison_test.go).
func Compress(src []byte, level int) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}
	bound := C.ZSTD_compressBound(C.size_t(len(src)))
	if C.ZSTD_isError(bound) != 0 {
		return nil, fmt.Errorf("zstd: compressBound failed for %d bytes", len(src))
	}
	dst := make([]byte, int(bound))
	written := C.ZSTD_compress(
		unsafe.Pointer(&dst[0]), bound,
		unsafe.Pointer(&src[0]), C.size_t(len(src)),
		C.int(level),
	)
	if C.ZSTD_isError(written) != 0 {
		return nil, fmt.Errorf("zstd: ZSTD_compress failed (level=%d, src=%d bytes)", level, len(src))
	}
	return dst[:int(written)], nil
}

// Decompress decompresses a single zstd frame into a freshly allocated
// buffer. The decompressed size is read from the frame header. Returns
// an error if the frame does not carry an explicit decompressed size
// (paper benchmark always compresses with ZSTD_compress, which embeds
// the size, so this is fine).
func Decompress(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}
	size := C.ZSTD_getFrameContentSize(unsafe.Pointer(&src[0]), C.size_t(len(src)))
	if size == C.ZSTD_CONTENTSIZE_UNKNOWN {
		return nil, fmt.Errorf("zstd: frame does not carry decompressed size")
	}
	if size == C.ZSTD_CONTENTSIZE_ERROR {
		return nil, fmt.Errorf("zstd: ZSTD_getFrameContentSize returned error")
	}
	dst := make([]byte, int(size))
	written := C.ZSTD_decompress(
		unsafe.Pointer(&dst[0]), C.size_t(size),
		unsafe.Pointer(&src[0]), C.size_t(len(src)),
	)
	if C.ZSTD_isError(written) != 0 {
		return nil, fmt.Errorf("zstd: ZSTD_decompress failed (compressed=%d bytes)", len(src))
	}
	return dst[:int(written)], nil
}

// DecompressInto decompresses a single zstd frame into the caller-provided
// buffer dst. dst must be at least the size of the decompressed payload.
// This avoids the per-iteration allocation in Decompress and is the
// variant used by the throughput benchmarks.
func DecompressInto(dst, src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(dst) == 0 {
		return 0, fmt.Errorf("zstd: dst buffer is empty")
	}
	written := C.ZSTD_decompress(
		unsafe.Pointer(&dst[0]), C.size_t(len(dst)),
		unsafe.Pointer(&src[0]), C.size_t(len(src)),
	)
	if C.ZSTD_isError(written) != 0 {
		return 0, fmt.Errorf("zstd: ZSTD_decompress failed (compressed=%d bytes)", len(src))
	}
	return int(written), nil
}
