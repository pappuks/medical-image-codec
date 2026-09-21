package mic

import (
	"testing"
)

func TestZigZagDeltaRoundtrip(t *testing.T) {
	in := make([]uint16, 256)
	for i := range in {
		in[i] = uint16(40 + (i%17)*3)
	}
	z := ZigZagDeltaU16(in)
	back := UnzigZagDeltaU16(z)
	for i := range in {
		if back[i] != in[i] {
			t.Fatalf("sample %d: got %d want %d", i, back[i], in[i])
		}
	}
	// Smooth 16-bit ramps should zigzag-delta to small values.
	var small int
	for _, v := range z {
		if v < 32 {
			small++
		}
	}
	if small < len(z)/2 {
		t.Fatalf("expected mostly small zigzag residuals, small=%d / %d", small, len(z))
	}
}

func TestZigZagDeltaEmpty(t *testing.T) {
	if len(ZigZagDeltaU16(nil)) != 0 {
		t.Fatal("empty")
	}
}
