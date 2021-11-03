// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"fmt"
	"testing"
)

func TestDeltaCompression(t *testing.T) {

	in := []uint16{256, 300, 468, 1025, 457, 399, 4096, 8000, 1}

	out, _ := DeltaCompressU16(in, 3, 3, 8000)

	inAgain := DeltaDecompressU16(out, 3, 3)

	passed := true

	for i, v := range in {
		if v != inAgain[i] {
			fmt.Printf("Value not same at %d original %d after %d\n", i, v, inAgain[i])
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("Delta Compression PASSED\n")
	}

}

func TestDeltaRleCompression(t *testing.T) {

	in := []uint16{256, 300, 468, 1025, 457, 399, 4096, 8000, 1}

	var drc DeltaRleCompressU16

	out, _ := drc.Compress(in, 3, 3, 8000)

	fmt.Println(out)

	var drd DeltaRleDecompressU16

	drd.Decompress(out, 3, 3)

	passed := true

	for i, v := range in {
		if v != drd.Out[i] {
			fmt.Printf("Value not same at %d original %d after %d\n", i, v, drd.Out[i])
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("Delta Compression PASSED\n")
	} else {
		t.Errorf("Delta Rle FAILED")
	}

}
