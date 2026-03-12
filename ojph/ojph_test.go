package ojph

import (
	"testing"
)

func TestRoundtrip(t *testing.T) {
	width, height := 64, 64
	pixels := make([]uint16, width*height)
	for i := range pixels {
		pixels[i] = uint16(i % 4096)
	}

	compressed, err := CompressU16(pixels, width, height, 12)
	if err != nil {
		t.Fatalf("CompressU16 failed: %v", err)
	}
	t.Logf("Compressed %d bytes -> %d bytes (%.2fx)", len(pixels)*2, len(compressed),
		float64(len(pixels)*2)/float64(len(compressed)))

	decompressed, err := DecompressU16(compressed, width, height)
	if err != nil {
		t.Fatalf("DecompressU16 failed: %v", err)
	}

	for i, v := range decompressed {
		if v != pixels[i] {
			t.Fatalf("Mismatch at pixel %d: got %d, want %d", i, v, pixels[i])
		}
	}
	t.Log("Roundtrip OK: all pixels match")
}
