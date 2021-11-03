// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
package mic

import (
	"fmt"
	"testing"
)

func TestRleCompressionU16(t *testing.T) {
	in := []uint16{256, 256, 256, 1025, 457, 457, 457, 8000, 1}

	var rle RleCompressU16
	rle.Init(3, 3, 8000)

	for _, v := range in {
		rle.Encode(v)
	}

	rle.Flush()

	fmt.Println(rle.Out)

	var rleD RleDecompressU16

	rleD.Init(rle.Out)

	passed := true

	for i := 0; i < len(in); i++ {
		dec := rleD.DecodeNext()
		if dec != in[i] {
			fmt.Printf("\n***Error at %d value orig %d value decomp %d\n", i, in[i], dec)
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("\nPASSED RLE %d\n", len(rle.Out))
	} else {
		t.Errorf("RLE FAILED")
	}

}

func TestRleCompressionU16_2(t *testing.T) {
	in := []uint16{256, 256, 256, 1025, 457, 457, 457, 8000, 1}

	var rle RleCompressU16
	rle.Init(3, 3, 8000)

	for _, v := range in {
		rle.Encode(v)
	}

	rle.Flush()

	fmt.Println(rle.Out)

	var rleD RleDecompressU16

	rleD.Init(rle.Out)

	passed := true

	for i := 0; i < len(in); i++ {
		dec := rleD.DecodeNext2()
		if dec != in[i] {
			fmt.Printf("\n***Error at %d value orig %d value decomp %d\n", i, in[i], dec)
			passed = false
			break
		}
	}

	if passed {
		fmt.Printf("\nPASSED RLE %d\n", len(rle.Out))
	} else {
		t.Errorf("RLE FAILED")
	}

}
