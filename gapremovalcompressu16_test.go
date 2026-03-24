// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"fmt"
	"testing"
)

// TestGapRemovalRoundtrip verifies that CompressSingleFrameGapRemoval followed
// by DecompressSingleFrameGapRemoval produces a pixel-exact reconstruction for
// every test modality.
func TestGapRemovalRoundtrip(t *testing.T) {
	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)

			compressed, err := CompressSingleFrameGapRemoval(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrameGapRemoval: %v", err)
			}

			got, err := DecompressSingleFrameGapRemoval(compressed, cols, rows)
			if err != nil {
				t.Fatalf("DecompressSingleFrameGapRemoval: %v", err)
			}

			if len(got) != len(shortData) {
				t.Fatalf("output length %d != input length %d", len(got), len(shortData))
			}
			for i, v := range shortData {
				if got[i] != v {
					t.Fatalf("pixel mismatch at index %d: got %d, want %d", i, got[i], v)
				}
			}
		})
	}
}

// TestGapRemovalCompressionRatio compares the compression ratio of the standard
// Delta+RLE+FSE pipeline against the gap-removal-enhanced pipeline for all test
// modalities. For XR and similar sparse-alphabet images the gap-removal variant
// is expected to produce a smaller output.
func TestGapRemovalCompressionRatio(t *testing.T) {
	fmt.Println()
	fmt.Printf("%-6s  %12s  %10s  %10s  %10s  %6s\n",
		"Image", "Raw (bytes)", "Standard", "GapRemoval", "Change", "GR?")
	fmt.Println("------  ------------  ----------  ----------  ----------  ------")

	for _, tf := range testFiles {
		t.Run(tf.name, func(t *testing.T) {
			_, shortData, maxShort, cols, rows := SetupTests(tf)
			originalBytes := len(shortData) * 2

			// Standard Delta+RLE+FSE.
			stdComp, err := CompressSingleFrame(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrame: %v", err)
			}

			// Gap-removal-enhanced pipeline.
			grComp, err := CompressSingleFrameGapRemoval(shortData, cols, rows, maxShort)
			if err != nil {
				t.Fatalf("CompressSingleFrameGapRemoval: %v", err)
			}

			stdRatio := float64(originalBytes) / float64(len(stdComp))
			grRatio := float64(originalBytes) / float64(len(grComp))
			change := (grRatio - stdRatio) / stdRatio * 100

			// Determine whether gap removal was applied and which mode.
			var grLabel string
			if len(grComp) > 0 {
				switch grComp[0] {
				case 0x00:
					grLabel = "no"
				case 0x01:
					grLabel = "raw"
				case 0x02:
					grLabel = "bitmap"
				case 0x03:
					grLabel = "delta"
				default:
					grLabel = "?"
				}
			}

			sign := ""
			if change > 0 {
				sign = "+"
			}

			fmt.Printf("%-6s  %12d  %10.3f  %10.3f  %9s%.2f%%  %6s\n",
				tf.name, originalBytes, stdRatio, grRatio, sign, change, grLabel)
		})
	}
}
