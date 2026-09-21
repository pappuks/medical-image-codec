//go:build cgo_ojph

package ojph

import (
	"testing"

	mic "mic"
)

// TestPICSRawFallbackC verifies the C PICS decoder reproduces images that
// contain raw-fallback strips (e.g. sparse PET) bit-for-bit, across strip
// counts 1/2/4/8. This exercises the picsRawFlag path in mic_parallel.c.
func TestPICSRawFallbackC(t *testing.T) {
	for _, ti := range testImages {
		ti := ti
		t.Run(ti.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := loadTestImage(ti)
			if len(shortData) == 0 {
				t.Skip("could not load image")
			}
			for _, strips := range []int{1, 2, 4, 8} {
				picsComp, err := mic.CompressParallelStrips(shortData, cols, rows, maxShort, strips)
				if err != nil {
					t.Fatalf("strips=%d: compress: %v", strips, err)
				}
				got, err := MICDecompressParallelC(picsComp, cols, rows, strips)
				if err != nil {
					t.Fatalf("strips=%d: C decompress: %v", strips, err)
				}
				if len(got) != len(shortData) {
					t.Fatalf("strips=%d: len %d != %d", strips, len(got), len(shortData))
				}
				for i := range shortData {
					if got[i] != shortData[i] {
						t.Fatalf("strips=%d: pixel %d: got %d want %d", strips, i, got[i], shortData[i])
					}
				}
			}
		})
	}
}
