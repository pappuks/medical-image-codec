// +build ignore

// Generates synthetic WSI test images for testdata/.
// Run: go run testdata/gen_wsi_testdata.go
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
)

func main() {
	// Generate a small 512x384 synthetic H&E tissue image
	w, h := 512, 384
	rgb := makeWSIImage(w, h, 42)

	fname := "testdata/wsi_tissue_512x384.rgb"
	if err := os.WriteFile(fname, rgb, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d bytes, %dx%d RGB)\n", fname, len(rgb), w, h)

	// Generate a 256x256 background tile
	bg := make([]byte, 256*256*3)
	for i := range bg {
		bg[i] = 255
	}
	fname2 := "testdata/wsi_background_256x256.rgb"
	if err := os.WriteFile(fname2, bg, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d bytes)\n", fname2, len(bg))
}

func makeWSIImage(width, height int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	rgb := make([]byte, width*height*3)

	for i := range rgb {
		rgb[i] = 255
	}

	cx := float64(width) / 2
	cy := float64(height) / 2
	radius := float64(min(width, height)) / 3.0

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist < radius {
				idx := (y*width + x) * 3
				baseR := 210.0 + 30.0*float64(y)/float64(height)
				baseG := 140.0 + 20.0*float64(x)/float64(width)
				baseB := 165.0 + 15.0*(float64(x)+float64(y))/float64(width+height)

				edgeFade := 1.0
				if dist > radius*0.85 {
					edgeFade = (radius - dist) / (radius * 0.15)
				}

				rgb[idx] = clamp(baseR*edgeFade + 255*(1-edgeFade) + float64(rng.Intn(12)))
				rgb[idx+1] = clamp(baseG*edgeFade + 255*(1-edgeFade) + float64(rng.Intn(10)))
				rgb[idx+2] = clamp(baseB*edgeFade + 255*(1-edgeFade) + float64(rng.Intn(8)))

				if dist < radius*0.8 && rng.Float64() < 0.02 {
					rgb[idx] = byte(60 + rng.Intn(30))
					rgb[idx+1] = byte(40 + rng.Intn(25))
					rgb[idx+2] = byte(120 + rng.Intn(40))
				}
			}
		}
	}
	return rgb
}

func clamp(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
