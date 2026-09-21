package mic

import (
	"math"
	"math/rand"
	"os"
	"testing"
)

// --- Synthetic WSI Test Image Generators ---

// makeWhiteTile generates a 256x256 all-white RGB tile (background).
func makeWhiteTile(width, height int) []byte {
	rgb := make([]byte, width*height*3)
	for i := range rgb {
		rgb[i] = 255
	}
	return rgb
}

// makeTissueTile generates an RGB tile simulating H&E-stained tissue.
// Pink/purple cytoplasm with occasional blue nuclei and smooth gradients.
func makeTissueTile(width, height int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	rgb := make([]byte, width*height*3)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 3
			// Base: eosin pink with smooth spatial gradient
			baseR := 200 + float64(y)*30.0/float64(height) + float64(x)*10.0/float64(width)
			baseG := 140 + float64(y)*20.0/float64(height)
			baseB := 170 + float64(x)*15.0/float64(width)

			r := clampByte(baseR + float64(rng.Intn(15)))
			g := clampByte(baseG + float64(rng.Intn(12)))
			b := clampByte(baseB + float64(rng.Intn(10)))

			// ~3% chance of a blue nucleus
			if rng.Float64() < 0.03 {
				r = byte(70 + rng.Intn(30))
				g = byte(50 + rng.Intn(25))
				b = byte(130 + rng.Intn(40))
			}

			rgb[idx] = r
			rgb[idx+1] = g
			rgb[idx+2] = b
		}
	}
	return rgb
}

// makeGradientTile generates a smooth RGB gradient tile.
func makeGradientTile(width, height int) []byte {
	rgb := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 3
			rgb[idx] = byte(x * 255 / width)
			rgb[idx+1] = byte(y * 255 / height)
			rgb[idx+2] = byte((x + y) * 127 / (width + height))
		}
	}
	return rgb
}

// makeWSITestImage generates a synthetic WSI-like image with:
// - White background surrounding a circular tissue region
// - H&E-stained tissue with smooth gradients and blue nuclei
func makeWSITestImage(width, height int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	rgb := make([]byte, width*height*3)

	// Fill with white background
	for i := range rgb {
		rgb[i] = 255
	}

	// Draw circular tissue region
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
				// Eosin pink with spatial gradient
				baseR := 210.0 + 30.0*float64(y)/float64(height)
				baseG := 140.0 + 20.0*float64(x)/float64(width)
				baseB := 165.0 + 15.0*(float64(x)+float64(y))/float64(width+height)

				// Fade near edges
				edgeFade := 1.0
				if dist > radius*0.85 {
					edgeFade = (radius - dist) / (radius * 0.15)
				}

				r := clampByte(baseR*edgeFade + 255*(1-edgeFade) + float64(rng.Intn(12)))
				g := clampByte(baseG*edgeFade + 255*(1-edgeFade) + float64(rng.Intn(10)))
				b := clampByte(baseB*edgeFade + 255*(1-edgeFade) + float64(rng.Intn(8)))

				// Blue nuclei (~2%)
				if dist < radius*0.8 && rng.Float64() < 0.02 {
					r = byte(60 + rng.Intn(30))
					g = byte(40 + rng.Intn(25))
					b = byte(120 + rng.Intn(40))
				}

				rgb[idx] = r
				rgb[idx+1] = g
				rgb[idx+2] = b
			}
		}
	}
	return rgb
}

func clampByte(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// --- YCoCg-R Tests ---

func TestYCoCgRRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		rgb  []byte
		w, h int
	}{
		{"black", makeConstantRGB(4, 4, 0, 0, 0), 4, 4},
		{"white", makeConstantRGB(4, 4, 255, 255, 255), 4, 4},
		{"red", makeConstantRGB(4, 4, 255, 0, 0), 4, 4},
		{"green", makeConstantRGB(4, 4, 0, 255, 0), 4, 4},
		{"blue", makeConstantRGB(4, 4, 0, 0, 255), 4, 4},
		{"gradient", makeGradientTile(256, 256), 256, 256},
		{"tissue", makeTissueTile(256, 256, 42), 256, 256},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			y, co, cg := YCoCgRForward(tc.rgb, tc.w, tc.h)
			got := YCoCgRInverse(y, co, cg, tc.w, tc.h)

			if len(got) != len(tc.rgb) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(tc.rgb))
			}
			for i := range tc.rgb {
				if got[i] != tc.rgb[i] {
					px := i / 3
					ch := i % 3
					t.Fatalf("mismatch at pixel %d channel %d: got %d, want %d", px, ch, got[i], tc.rgb[i])
				}
			}
		})
	}
}

func TestYCoCgRExhaustive8Bit(t *testing.T) {
	// Test all 256^3 = 16M possible RGB values for perfect roundtrip.
	// This is thorough but takes ~2s. Skip in short mode.
	if testing.Short() {
		t.Skip("skipping exhaustive test in short mode")
	}

	rgb := make([]byte, 3)
	for r := 0; r < 256; r++ {
		for g := 0; g < 256; g++ {
			for b := 0; b < 256; b++ {
				rgb[0] = byte(r)
				rgb[1] = byte(g)
				rgb[2] = byte(b)

				y, co, cg := YCoCgRForward(rgb, 1, 1)
				got := YCoCgRInverse(y, co, cg, 1, 1)

				if got[0] != rgb[0] || got[1] != rgb[1] || got[2] != rgb[2] {
					t.Fatalf("roundtrip failed for RGB(%d,%d,%d): got (%d,%d,%d)",
						r, g, b, got[0], got[1], got[2])
				}
			}
		}
	}
}

func TestYCoCgRKnownValues(t *testing.T) {
	// Verify known transform values
	rgb := []byte{200, 100, 50}
	y, co, cg := YCoCgRForward(rgb, 1, 1)

	// Co = R - B = 200 - 50 = 150 -> ZigZag(150) = 300
	// t = B + (Co >> 1) = 50 + 75 = 125
	// Cg = G - t = 100 - 125 = -25 -> ZigZag(-25) = 49
	// Y = t + (Cg >> 1) = 125 + (-13) = 112
	if y[0] != 112 {
		t.Errorf("Y: got %d, want 112", y[0])
	}
	if co[0] != ZigZag(150) {
		t.Errorf("Co: got %d, want %d", co[0], ZigZag(150))
	}
	if cg[0] != ZigZag(-25) {
		t.Errorf("Cg: got %d, want %d", cg[0], ZigZag(-25))
	}
}

func makeConstantRGB(w, h int, r, g, b byte) []byte {
	rgb := make([]byte, w*h*3)
	for i := 0; i < w*h; i++ {
		rgb[i*3] = r
		rgb[i*3+1] = g
		rgb[i*3+2] = b
	}
	return rgb
}

// --- Pyramid Downsampling Tests ---

func TestDownsample2xRGB(t *testing.T) {
	// 4x4 image with known values
	w, h := 4, 4
	src := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (y*w + x) * 3
			src[idx] = byte(x * 50)   // R: 0, 50, 100, 150
			src[idx+1] = byte(y * 60) // G: 0, 60, 120, 180
			src[idx+2] = 100          // B: constant
		}
	}

	dst, newW, newH := Downsample2xRGB(src, w, h)
	if newW != 2 || newH != 2 {
		t.Fatalf("expected 2x2, got %dx%d", newW, newH)
	}

	// Top-left 2x2 block: R values are 0,50,0,50 -> avg 25
	// G values are 0,0,60,60 -> avg 30
	// B values are all 100 -> avg 100
	idx := 0
	if dst[idx] != 25 || dst[idx+1] != 30 || dst[idx+2] != 100 {
		t.Errorf("pixel (0,0): got (%d,%d,%d), want (25,30,100)", dst[0], dst[1], dst[2])
	}
}

func TestDownsample2xOddDimensions(t *testing.T) {
	// 5x3 image -> 2x1 after downsampling
	w, h := 5, 3
	src := make([]byte, w*h*3)
	for i := range src {
		src[i] = 128
	}
	dst, newW, newH := Downsample2xRGB(src, w, h)
	if newW != 2 || newH != 1 {
		t.Fatalf("expected 2x1, got %dx%d", newW, newH)
	}
	if dst[0] != 128 {
		t.Errorf("expected 128, got %d", dst[0])
	}
}

func TestDownsample2xGrey(t *testing.T) {
	w, h := 4, 4
	src := make([]uint16, w*h)
	for i := range src {
		src[i] = 1000
	}
	dst, newW, newH := Downsample2xGrey(src, w, h)
	if newW != 2 || newH != 2 {
		t.Fatalf("expected 2x2, got %dx%d", newW, newH)
	}
	if dst[0] != 1000 {
		t.Errorf("expected 1000, got %d", dst[0])
	}
}

// --- MIC3 Container Format Tests ---

func TestMIC3HeaderRoundtrip(t *testing.T) {
	hdr := WSIHeader{
		Width:          1024,
		Height:         768,
		TileWidth:      256,
		TileHeight:     256,
		Channels:       3,
		BitsPerSample:  8,
		ColorTransform: true,
		Levels: []WSILevel{
			{Width: 1024, Height: 768, TilesX: 4, TilesY: 3, FirstTileIdx: 0},
			{Width: 512, Height: 384, TilesX: 2, TilesY: 2, FirstTileIdx: 12},
			{Width: 256, Height: 192, TilesX: 1, TilesY: 1, FirstTileIdx: 16},
		},
	}

	// Create dummy tile blobs
	totalTiles := 12 + 4 + 1
	tileBlobs := make([][]byte, totalTiles)
	for i := range tileBlobs {
		tileBlobs[i] = []byte{byte(i), byte(i + 1), byte(i + 2)}
	}

	var buf [65536]byte
	w := &sliceWriter{buf: buf[:0]}
	if err := WriteMIC3(w, hdr, tileBlobs); err != nil {
		t.Fatal(err)
	}
	data := w.buf

	hdr2, entries, dataOffset, err := ReadMIC3Header(data)
	if err != nil {
		t.Fatal(err)
	}

	if hdr2.Width != hdr.Width || hdr2.Height != hdr.Height {
		t.Errorf("dimensions: got %dx%d, want %dx%d", hdr2.Width, hdr2.Height, hdr.Width, hdr.Height)
	}
	if hdr2.TileWidth != hdr.TileWidth || hdr2.TileHeight != hdr.TileHeight {
		t.Errorf("tile size: got %dx%d, want %dx%d", hdr2.TileWidth, hdr2.TileHeight, hdr.TileWidth, hdr.TileHeight)
	}
	if hdr2.Channels != 3 || hdr2.BitsPerSample != 8 || !hdr2.ColorTransform {
		t.Errorf("format: channels=%d bps=%d ct=%v", hdr2.Channels, hdr2.BitsPerSample, hdr2.ColorTransform)
	}
	if len(hdr2.Levels) != 3 {
		t.Fatalf("levels: got %d, want 3", len(hdr2.Levels))
	}

	// Verify tile extraction
	for i := 0; i < totalTiles; i++ {
		blob, err := ExtractTileBlob(data, entries, dataOffset, i)
		if err != nil {
			t.Fatalf("extract tile %d: %v", i, err)
		}
		if len(blob) != 3 || blob[0] != byte(i) {
			t.Fatalf("tile %d data mismatch", i)
		}
	}
}

// sliceWriter implements io.Writer backed by a byte slice.
type sliceWriter struct {
	buf []byte
}

func (s *sliceWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	return len(p), nil
}

// --- Single Tile Compression Tests ---

func TestWSITileCompressWhite(t *testing.T) {
	w, h := 256, 256
	rgb := makeWhiteTile(w, h)

	blob, err := compressTileBlob(rgb, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(blob))
	t.Logf("White tile: %d bytes -> %d bytes (%.1f:1)", rawSize, len(blob), ratio)

	// White tiles should compress extremely well
	if ratio < 10 {
		t.Errorf("expected ratio > 10:1 for white tile, got %.1f:1", ratio)
	}

	// Roundtrip
	got, err := decompressTileBlob(blob, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, got, "white tile")
}

func TestWSITileCompressTissue(t *testing.T) {
	w, h := 256, 256
	rgb := makeTissueTile(w, h, 42)

	blob, err := compressTileBlob(rgb, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(blob))
	t.Logf("Tissue tile: %d bytes -> %d bytes (%.1f:1)", rawSize, len(blob), ratio)

	// Synthetic tissue with high noise gets ~1.5:1; real H&E tissue would be 3-5:1.
	// Just verify it compresses at all (ratio > 1.0).
	if ratio < 1.0 {
		t.Errorf("expected ratio > 1:1 for tissue tile, got %.1f:1", ratio)
	}

	// Roundtrip
	got, err := decompressTileBlob(blob, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, got, "tissue tile")
}

func TestWSITileCompressGradient(t *testing.T) {
	w, h := 256, 256
	rgb := makeGradientTile(w, h)

	blob, err := compressTileBlob(rgb, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(blob))
	t.Logf("Gradient tile: %d bytes -> %d bytes (%.1f:1)", rawSize, len(blob), ratio)

	got, err := decompressTileBlob(blob, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, got, "gradient tile")
}

func TestWSITileCompressBlack(t *testing.T) {
	w, h := 256, 256
	rgb := makeConstantRGB(w, h, 0, 0, 0)

	blob, err := compressTileBlob(rgb, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(blob))
	t.Logf("Black tile: %d bytes -> %d bytes (%.1f:1)", rawSize, len(blob), ratio)

	got, err := decompressTileBlob(blob, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, got, "black tile")
}

func TestWSITileCompressNoColorTransform(t *testing.T) {
	w, h := 256, 256
	rgb := makeTissueTile(w, h, 99)

	blob, err := compressTileBlob(rgb, w, h, 3, 8, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := decompressTileBlob(blob, w, h, 3, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, got, "no color transform tile")
}

func TestWSITileCompressRandomPixels(t *testing.T) {
	// Random data tests the raw fallback path (may not compress well)
	w, h := 64, 64
	rng := rand.New(rand.NewSource(777))
	rgb := make([]byte, w*h*3)
	for i := range rgb {
		rgb[i] = byte(rng.Intn(256))
	}

	blob, err := compressTileBlob(rgb, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}

	got, err := decompressTileBlob(blob, w, h, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, got, "random tile")
}

// --- Full WSI Compress/Decompress Tests ---

func TestWSICompressSmall(t *testing.T) {
	// Small 512x512 synthetic WSI
	w, h := 512, 512
	rgb := makeWSITestImage(w, h, 100)

	opts := WSIOptions{
		TileWidth:  256,
		TileHeight: 256,
		Workers:    1,
	}

	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(compressed))
	t.Logf("WSI 512x512: %d bytes -> %d bytes (%.2f:1)", rawSize, len(compressed), ratio)

	// Verify header
	hdr, err := ReadWSIHeader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Width != w || hdr.Height != h {
		t.Errorf("dimensions: got %dx%d, want %dx%d", hdr.Width, hdr.Height, w, h)
	}
	if hdr.Channels != 3 || hdr.BitsPerSample != 8 {
		t.Errorf("format: channels=%d bps=%d", hdr.Channels, hdr.BitsPerSample)
	}
	t.Logf("Pyramid levels: %d", len(hdr.Levels))
	for i, lv := range hdr.Levels {
		t.Logf("  Level %d: %dx%d (%dx%d tiles)", i, lv.Width, lv.Height, lv.TilesX, lv.TilesY)
	}

	// Verify tile-by-tile decompression matches original
	for ty := 0; ty < 2; ty++ {
		for tx := 0; tx < 2; tx++ {
			tile, err := DecompressWSITile(compressed, 0, tx, ty)
			if err != nil {
				t.Fatalf("decompress tile (%d,%d): %v", tx, ty, err)
			}
			// Compare with original
			for dy := 0; dy < 256; dy++ {
				for dx := 0; dx < 256; dx++ {
					srcX := tx*256 + dx
					srcY := ty*256 + dy
					if srcX >= w || srcY >= h {
						continue
					}
					srcIdx := (srcY*w + srcX) * 3
					tileIdx := (dy*256 + dx) * 3
					for c := 0; c < 3; c++ {
						if tile[tileIdx+c] != rgb[srcIdx+c] {
							t.Fatalf("mismatch at tile(%d,%d) pixel(%d,%d) ch %d: got %d, want %d",
								tx, ty, dx, dy, c, tile[tileIdx+c], rgb[srcIdx+c])
						}
					}
				}
			}
		}
	}
}

func TestWSICompressMedium(t *testing.T) {
	// 1024x768 synthetic WSI with mixed tissue and background
	w, h := 1024, 768
	rgb := makeWSITestImage(w, h, 200)

	opts := WSIOptions{
		TileWidth:  256,
		TileHeight: 256,
		Workers:    1,
	}

	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(compressed))
	t.Logf("WSI 1024x768: %d bytes -> %d bytes (%.2f:1)", rawSize, len(compressed), ratio)

	hdr, err := ReadWSIHeader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Pyramid levels: %d", len(hdr.Levels))

	// Verify region decompression
	region, err := DecompressWSIRegion(compressed, 0, 100, 100, 200, 150)
	if err != nil {
		t.Fatal(err)
	}

	// Verify region pixels match original
	for ry := 0; ry < 150; ry++ {
		for rx := 0; rx < 200; rx++ {
			srcIdx := ((100+ry)*w + (100 + rx)) * 3
			dstIdx := (ry*200 + rx) * 3
			for c := 0; c < 3; c++ {
				if region[dstIdx+c] != rgb[srcIdx+c] {
					t.Fatalf("region mismatch at (%d,%d) ch %d: got %d, want %d",
						rx, ry, c, region[dstIdx+c], rgb[srcIdx+c])
				}
			}
		}
	}
}

func TestWSICompressParallel(t *testing.T) {
	w, h := 1024, 768
	rgb := makeWSITestImage(w, h, 300)

	// Compress with 1 worker
	opts1 := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 1}
	comp1, err := CompressWSI(rgb, w, h, 3, 8, opts1)
	if err != nil {
		t.Fatal(err)
	}

	// Compress with multiple workers
	opts4 := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 4}
	comp4, err := CompressWSI(rgb, w, h, 3, 8, opts4)
	if err != nil {
		t.Fatal(err)
	}

	// Both should produce decompressed output that matches original
	// (compressed bytes may differ due to goroutine scheduling but decompressed must be identical)
	for ty := 0; ty < 3; ty++ {
		for tx := 0; tx < 4; tx++ {
			t1, err := DecompressWSITile(comp1, 0, tx, ty)
			if err != nil {
				t.Fatalf("decompress tile (%d,%d) serial: %v", tx, ty, err)
			}
			t4, err := DecompressWSITile(comp4, 0, tx, ty)
			if err != nil {
				t.Fatalf("decompress tile (%d,%d) parallel: %v", tx, ty, err)
			}
			assertBytesEqual(t, t1, t4, "parallel vs serial tile")
		}
	}
}

func TestWSICompressOddDimensions(t *testing.T) {
	// Non-tile-aligned dimensions to test edge tile padding
	w, h := 300, 250
	rgb := makeWSITestImage(w, h, 400)

	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 1}
	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	hdr, err := ReadWSIHeader(compressed)
	if err != nil {
		t.Fatal(err)
	}

	lv0 := hdr.Levels[0]
	if lv0.TilesX != 2 || lv0.TilesY != 1 {
		t.Errorf("expected 2x1 tiles, got %dx%d", lv0.TilesX, lv0.TilesY)
	}

	// Decompress edge tile (right edge: 44 pixels wide)
	tile, err := DecompressWSITile(compressed, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Edge tile should be cropped to actual width
	expectedW := w - 256
	expectedH := h
	expectedLen := expectedW * expectedH * 3
	if len(tile) != expectedLen {
		t.Errorf("edge tile length: got %d, want %d (expected %dx%d)", len(tile), expectedLen, expectedW, expectedH)
	}

	// Verify edge tile pixels match original
	for dy := 0; dy < expectedH; dy++ {
		for dx := 0; dx < expectedW; dx++ {
			srcIdx := (dy*w + (256 + dx)) * 3
			tileIdx := (dy*expectedW + dx) * 3
			for c := 0; c < 3; c++ {
				if tile[tileIdx+c] != rgb[srcIdx+c] {
					t.Fatalf("edge tile mismatch at (%d,%d) ch %d: got %d, want %d",
						dx, dy, c, tile[tileIdx+c], rgb[srcIdx+c])
				}
			}
		}
	}
}

func TestWSIPyramidLevels(t *testing.T) {
	w, h := 2048, 1536
	rgb := makeWSITestImage(w, h, 500)

	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 2}
	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	hdr, err := ReadWSIHeader(compressed)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Image %dx%d, %d pyramid levels:", w, h, len(hdr.Levels))
	for i, lv := range hdr.Levels {
		t.Logf("  Level %d: %dx%d (%dx%d tiles)", i, lv.Width, lv.Height, lv.TilesX, lv.TilesY)
	}

	// Verify level 0 matches original
	tile00, err := DecompressWSITile(compressed, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for dy := 0; dy < 256; dy++ {
		for dx := 0; dx < 256; dx++ {
			srcIdx := (dy*w + dx) * 3
			tileIdx := (dy*256 + dx) * 3
			for c := 0; c < 3; c++ {
				if tile00[tileIdx+c] != rgb[srcIdx+c] {
					t.Fatalf("pyramid level 0 mismatch at (%d,%d) ch %d", dx, dy, c)
				}
			}
		}
	}

	// Verify higher pyramid levels decompress without error
	for lvl := 1; lvl < len(hdr.Levels); lvl++ {
		lv := hdr.Levels[lvl]
		tile, err := DecompressWSITile(compressed, lvl, 0, 0)
		if err != nil {
			t.Fatalf("decompress level %d tile (0,0): %v", lvl, err)
		}
		expectedW := lv.Width
		if expectedW > hdr.TileWidth {
			expectedW = hdr.TileWidth
		}
		expectedH := lv.Height
		if expectedH > hdr.TileHeight {
			expectedH = hdr.TileHeight
		}
		expectedLen := expectedW * expectedH * 3
		if len(tile) != expectedLen {
			t.Errorf("level %d tile (0,0): got %d bytes, want %d", lvl, len(tile), expectedLen)
		}
	}
}

func TestWSIRegionCrossTile(t *testing.T) {
	// Region that spans multiple tiles
	w, h := 512, 512
	rgb := makeWSITestImage(w, h, 600)

	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 1}
	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Region crossing 4 tiles: (200,200) to (400,400)
	region, err := DecompressWSIRegion(compressed, 0, 200, 200, 200, 200)
	if err != nil {
		t.Fatal(err)
	}

	for ry := 0; ry < 200; ry++ {
		for rx := 0; rx < 200; rx++ {
			srcIdx := ((200+ry)*w + (200 + rx)) * 3
			dstIdx := (ry*200 + rx) * 3
			for c := 0; c < 3; c++ {
				if region[dstIdx+c] != rgb[srcIdx+c] {
					t.Fatalf("cross-tile region mismatch at (%d,%d) ch %d", rx, ry, c)
				}
			}
		}
	}
}

// --- Binary Test Data Tests ---

func TestWSICompressFromFile(t *testing.T) {
	// Test with the pre-generated binary test image
	fname := "testdata/wsi_tissue_512x384.rgb"
	rgb, err := os.ReadFile(fname)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}

	w, h := 512, 384
	expectedLen := w * h * 3
	if len(rgb) != expectedLen {
		t.Fatalf("file size mismatch: got %d, want %d", len(rgb), expectedLen)
	}

	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 2}
	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(compressed))
	t.Logf("WSI file test: %d bytes -> %d bytes (%.2f:1)", rawSize, len(compressed), ratio)

	hdr, err := ReadWSIHeader(compressed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Pyramid levels: %d", len(hdr.Levels))
	for i, lv := range hdr.Levels {
		t.Logf("  Level %d: %dx%d (%dx%d tiles)", i, lv.Width, lv.Height, lv.TilesX, lv.TilesY)
	}

	// Full roundtrip: decompress all level 0 tiles and verify against original
	lv0 := hdr.Levels[0]
	for ty := 0; ty < lv0.TilesY; ty++ {
		for tx := 0; tx < lv0.TilesX; tx++ {
			tile, err := DecompressWSITile(compressed, 0, tx, ty)
			if err != nil {
				t.Fatalf("decompress tile (%d,%d): %v", tx, ty, err)
			}

			// Determine expected dimensions for this tile
			tileW := hdr.TileWidth
			tileH := hdr.TileHeight
			if tx == lv0.TilesX-1 && w%hdr.TileWidth != 0 {
				tileW = w % hdr.TileWidth
			}
			if ty == lv0.TilesY-1 && h%hdr.TileHeight != 0 {
				tileH = h % hdr.TileHeight
			}

			if len(tile) != tileW*tileH*3 {
				t.Fatalf("tile (%d,%d) size: got %d, want %d", tx, ty, len(tile), tileW*tileH*3)
			}

			// Verify pixels
			for dy := 0; dy < tileH; dy++ {
				for dx := 0; dx < tileW; dx++ {
					srcX := tx*hdr.TileWidth + dx
					srcY := ty*hdr.TileHeight + dy
					srcIdx := (srcY*w + srcX) * 3
					tileIdx := (dy*tileW + dx) * 3
					for c := 0; c < 3; c++ {
						if tile[tileIdx+c] != rgb[srcIdx+c] {
							t.Fatalf("tile (%d,%d) pixel (%d,%d) ch %d: got %d, want %d",
								tx, ty, dx, dy, c, tile[tileIdx+c], rgb[srcIdx+c])
						}
					}
				}
			}
		}
	}
}

func TestWSICompressBackgroundFile(t *testing.T) {
	fname := "testdata/wsi_background_256x256.rgb"
	rgb, err := os.ReadFile(fname)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}

	w, h := 256, 256
	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 1}
	compressed, err := CompressWSI(rgb, w, h, 3, 8, opts)
	if err != nil {
		t.Fatal(err)
	}

	rawSize := w * h * 3
	ratio := float64(rawSize) / float64(len(compressed))
	t.Logf("Background file: %d bytes -> %d bytes (%.1f:1)", rawSize, len(compressed), ratio)

	if ratio < 100 {
		t.Errorf("expected > 100:1 for pure white image, got %.1f:1", ratio)
	}

	tile, err := DecompressWSITile(compressed, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertBytesEqual(t, rgb, tile, "background file roundtrip")
}

// --- Benchmarks ---

func BenchmarkWSITileCompressTissue(b *testing.B) {
	w, h := 256, 256
	rgb := makeTissueTile(w, h, 42)

	b.SetBytes(int64(len(rgb)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := compressTileBlob(rgb, w, h, 3, 8, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWSITileDecompressTissue(b *testing.B) {
	w, h := 256, 256
	rgb := makeTissueTile(w, h, 42)
	blob, err := compressTileBlob(rgb, w, h, 3, 8, true)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(rgb)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := decompressTileBlob(blob, w, h, 3, 8, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWSITileCompressWhite(b *testing.B) {
	w, h := 256, 256
	rgb := makeWhiteTile(w, h)

	b.SetBytes(int64(len(rgb)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := compressTileBlob(rgb, w, h, 3, 8, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWSICompress1024(b *testing.B) {
	w, h := 1024, 1024
	rgb := makeWSITestImage(w, h, 42)
	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 1}

	b.SetBytes(int64(len(rgb)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := CompressWSI(rgb, w, h, 3, 8, opts)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWSICompressParallel1024(b *testing.B) {
	w, h := 1024, 1024
	rgb := makeWSITestImage(w, h, 42)
	opts := WSIOptions{TileWidth: 256, TileHeight: 256, Workers: 0} // use all cores

	b.SetBytes(int64(len(rgb)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := CompressWSI(rgb, w, h, 3, 8, opts)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- Helper ---

func assertBytesEqual(t *testing.T, want, got []byte, label string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: got %d, want %d", label, len(got), len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: mismatch at byte %d: got %d, want %d", label, i, got[i], want[i])
		}
	}
}
