// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

//go:build cgo_zstd

package zstd

import (
	"bytes"
	"testing"
)

// TestRoundtrip exercises Compress and both Decompress variants on a payload
// large enough to clear any short-mode threshold inside libzstd.
func TestRoundtrip(t *testing.T) {
	src := make([]byte, 1<<16)
	for i := range src {
		src[i] = byte(i ^ (i >> 7))
	}

	for _, level := range []int{1, 3, 19} {
		comp, err := Compress(src, level)
		if err != nil {
			t.Fatalf("Compress level=%d: %v", level, err)
		}
		if len(comp) == 0 || len(comp) >= len(src) {
			t.Fatalf("Compress level=%d: nonsensical size %d (src=%d)", level, len(comp), len(src))
		}

		got, err := Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress level=%d: %v", level, err)
		}
		if !bytes.Equal(got, src) {
			t.Fatalf("Decompress level=%d: roundtrip mismatch (%d vs %d bytes)", level, len(got), len(src))
		}

		buf := make([]byte, len(src))
		n, err := DecompressInto(buf, comp)
		if err != nil {
			t.Fatalf("DecompressInto level=%d: %v", level, err)
		}
		if n != len(src) || !bytes.Equal(buf, src) {
			t.Fatalf("DecompressInto level=%d: roundtrip mismatch (n=%d)", level, n)
		}
	}
}
