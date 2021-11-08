package mic

import (
	"fmt"
	"testing"
)

func TestDeltaZZCompression(t *testing.T) {
	in := []uint16{256, 300, 468, 1025, 457, 399, 4096, 8000, 1, 65534, 0, 65535}
	fmt.Println(in)
	var comp DeltaZZU16
	out, _ := comp.Compress(in, 4, 3, 65535)
	fmt.Println(out)
	var decomp DeltaZZU16
	inAgain := decomp.Decompress(out, 4, 3)
	fmt.Println(inAgain)
	passed := true
	for i, v := range in {
		if v != inAgain[i] {
			fmt.Printf("Value not same at %d original %d after %d\n", i, v, inAgain[i])
			passed = false
			break
		}
	}
	if passed {
		fmt.Printf("Delta ZZ Compression PASSED\n")
	} else {
		t.Errorf("Delta ZZ compression falied")
	}
}

func TestDeltaZZRleCompression(t *testing.T) {
	in := []uint16{256, 300, 468, 1025, 457, 399, 4096, 8000, 1, 65534, 0, 65535}
	fmt.Println(in)
	var comp DeltaZZU16
	outZZ, _ := comp.Compress(in, 4, 3, 65535)
	fmt.Println(outZZ)
	var rlecomp RleCompressU16
	rlecomp.Init(4, 3, (comp.upperThreshold<<1)+1)
	rleCompressed := rlecomp.Compress(outZZ)
	fmt.Println(rleCompressed)
	var rleD RleDecompressU16
	rleD.Init(rleCompressed)
	rleDecompressed := rleD.Decompress()
	fmt.Println(rleDecompressed)
	var decomp DeltaZZU16
	inAgain := decomp.Decompress(rleDecompressed, 4, 3)
	fmt.Println(inAgain)
	passed := true
	for i, v := range in {
		if v != inAgain[i] {
			fmt.Printf("Value not same at %d original %d after %d\n", i, v, inAgain[i])
			passed = false
			break
		}
	}
	if passed {
		fmt.Printf("Delta ZZ Compression PASSED\n")
	} else {
		t.Errorf("Delta ZZ compression falied")
	}
}

func TestDeltaZZRleCombinedCompression(t *testing.T) {
	in := []uint16{256, 300, 468, 1025, 457, 399, 4096, 8000, 1, 65534, 0, 65535}
	fmt.Println(in)
	var comp DeltaRleZZU16
	out, _ := comp.Compress(in, 4, 3, 65535)
	fmt.Println(out)
	var decomp DeltaRleZZU16
	inAgain := decomp.Decompress(out, 4, 3)
	fmt.Println(inAgain)
	passed := true
	for i, v := range in {
		if v != inAgain[i] {
			fmt.Printf("Value not same at %d original %d after %d\n", i, v, inAgain[i])
			passed = false
			break
		}
	}
	if passed {
		fmt.Printf("Delta ZZ Compression PASSED\n")
	} else {
		t.Errorf("Delta ZZ compression falied")
	}
}
