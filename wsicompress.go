// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
)

// Per-plane encoding modes in a tile blob.
const (
	planeConstantZero = 0 // all pixels are 0
	planeConstant     = 1 // all pixels are the same value (uint16 LE follows)
	planeCompressed   = 2 // CompressSingleFrame output follows
	planeRaw          = 3 // raw uint16 LE fallback (compression failed)
)

// CompressWSI compresses a full-resolution image into MIC3 format.
// pixels is row-major: for RGB it's interleaved RGBRGB..., for greyscale it's
// raw bytes (1 byte per pixel for 8-bit, 2 bytes LE per pixel for 16-bit).
func CompressWSI(pixels []byte, width, height, channels, bitsPerSample int, opts WSIOptions) ([]byte, error) {
	opts.defaults(channels)

	numLevels := opts.PyramidLevels
	if numLevels <= 0 {
		numLevels = autoLevelCount(width, height, opts.TileWidth, opts.TileHeight)
	}

	levels := computeLevels(width, height, opts.TileWidth, opts.TileHeight, numLevels)

	// Build pyramid images (level 0 is the input, subsequent levels are downsampled)
	type levelImage struct {
		data          []byte
		width, height int
	}
	pyramid := make([]levelImage, numLevels)
	pyramid[0] = levelImage{data: pixels, width: width, height: height}

	for i := 1; i < numLevels; i++ {
		prev := pyramid[i-1]
		if channels == 3 {
			d, w, h := Downsample2xRGB(prev.data, prev.width, prev.height)
			if d == nil {
				// Image too small to downsample further
				numLevels = i
				levels = levels[:numLevels]
				pyramid = pyramid[:numLevels]
				break
			}
			pyramid[i] = levelImage{data: d, width: w, height: h}
		} else {
			// Greyscale: convert bytes to uint16, downsample, convert back
			u16 := bytesToUint16Slice(prev.data, bitsPerSample)
			d, w, h := Downsample2xGrey(u16, prev.width, prev.height)
			if d == nil {
				numLevels = i
				levels = levels[:numLevels]
				pyramid = pyramid[:numLevels]
				break
			}
			pyramid[i] = levelImage{data: uint16ToBytes(d, bitsPerSample), width: w, height: h}
		}
		levels[i].Width = pyramid[i].width
		levels[i].Height = pyramid[i].height
		levels[i].TilesX = (pyramid[i].width + opts.TileWidth - 1) / opts.TileWidth
		levels[i].TilesY = (pyramid[i].height + opts.TileHeight - 1) / opts.TileHeight
	}

	// Recompute firstTileIdx after possible truncation
	idx := 0
	for i := range levels {
		levels[i].FirstTileIdx = idx
		idx += levels[i].TilesX * levels[i].TilesY
	}

	// Collect all tiles across all levels
	type tileJob struct {
		globalIdx int
		pixels    []byte
		width     int
		height    int
	}

	totalTiles := idx
	jobs := make([]tileJob, 0, totalTiles)

	for lvl := 0; lvl < numLevels; lvl++ {
		lv := levels[lvl]
		img := pyramid[lvl]
		for ty := 0; ty < lv.TilesY; ty++ {
			for tx := 0; tx < lv.TilesX; tx++ {
				tile := extractTileRGB(img.data, img.width, img.height, opts.TileWidth, opts.TileHeight, tx, ty, channels, bitsPerSample)
				gIdx := lv.FirstTileIdx + ty*lv.TilesX + tx
				jobs = append(jobs, tileJob{
					globalIdx: gIdx,
					pixels:    tile,
					width:     opts.TileWidth,
					height:    opts.TileHeight,
				})
			}
		}
	}

	// Compress tiles (parallel if workers > 1)
	tileBlobs := make([][]byte, totalTiles)
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	if workers <= 1 || len(jobs) <= 1 {
		// Sequential
		for _, job := range jobs {
			blob, err := compressTileBlob(job.pixels, job.width, job.height, channels, bitsPerSample, opts.ColorTransform)
			if err != nil {
				return nil, fmt.Errorf("tile %d: %w", job.globalIdx, err)
			}
			tileBlobs[job.globalIdx] = blob
		}
	} else {
		// Parallel
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		errs := make([]error, totalTiles)

		for _, job := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func(j tileJob) {
				defer func() { <-sem; wg.Done() }()
				blob, err := compressTileBlob(j.pixels, j.width, j.height, channels, bitsPerSample, opts.ColorTransform)
				if err != nil {
					errs[j.globalIdx] = err
					return
				}
				tileBlobs[j.globalIdx] = blob
			}(job)
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				return nil, fmt.Errorf("tile %d: %w", i, err)
			}
		}
	}

	// Write MIC3
	hdr := WSIHeader{
		Width:          width,
		Height:         height,
		TileWidth:      opts.TileWidth,
		TileHeight:     opts.TileHeight,
		Channels:       channels,
		BitsPerSample:  bitsPerSample,
		ColorTransform: opts.ColorTransform,
		Levels:         levels,
	}

	var buf bytes.Buffer
	if err := WriteMIC3(&buf, hdr, tileBlobs); err != nil {
		return nil, fmt.Errorf("write MIC3: %w", err)
	}
	return buf.Bytes(), nil
}

// DecompressWSITile decompresses a single tile at the given pyramid level.
// Returns channel-interleaved pixel data.
func DecompressWSITile(data []byte, level, tileX, tileY int) ([]byte, error) {
	hdr, entries, dataOffset, err := ReadMIC3Header(data)
	if err != nil {
		return nil, err
	}
	if level < 0 || level >= len(hdr.Levels) {
		return nil, fmt.Errorf("MIC3: level %d out of range [0, %d)", level, len(hdr.Levels))
	}

	lv := hdr.Levels[level]
	if tileX < 0 || tileX >= lv.TilesX || tileY < 0 || tileY >= lv.TilesY {
		return nil, fmt.Errorf("MIC3: tile (%d,%d) out of range for level %d (%dx%d tiles)", tileX, tileY, level, lv.TilesX, lv.TilesY)
	}

	globalIdx := lv.FirstTileIdx + tileY*lv.TilesX + tileX
	blob, err := ExtractTileBlob(data, entries, dataOffset, globalIdx)
	if err != nil {
		return nil, err
	}

	tile, err := decompressTileBlob(blob, hdr.TileWidth, hdr.TileHeight, hdr.Channels, hdr.BitsPerSample, hdr.ColorTransform)
	if err != nil {
		return nil, fmt.Errorf("tile (%d,%d) level %d: %w", tileX, tileY, level, err)
	}

	// Crop edge tile if needed
	actualW := hdr.TileWidth
	actualH := hdr.TileHeight
	edgeX := lv.Width - tileX*hdr.TileWidth
	edgeY := lv.Height - tileY*hdr.TileHeight
	if edgeX < actualW {
		actualW = edgeX
	}
	if edgeY < actualH {
		actualH = edgeY
	}

	if actualW == hdr.TileWidth && actualH == hdr.TileHeight {
		return tile, nil
	}

	return cropTile(tile, hdr.TileWidth, hdr.TileHeight, actualW, actualH, hdr.Channels, hdr.BitsPerSample), nil
}

// DecompressWSIRegion decompresses a rectangular region at a specific pyramid level.
func DecompressWSIRegion(data []byte, level, x, y, w, h int) ([]byte, error) {
	hdr, entries, dataOffset, err := ReadMIC3Header(data)
	if err != nil {
		return nil, err
	}
	if level < 0 || level >= len(hdr.Levels) {
		return nil, fmt.Errorf("MIC3: level %d out of range [0, %d)", level, len(hdr.Levels))
	}

	lv := hdr.Levels[level]

	// Clamp region to level bounds
	if x+w > lv.Width {
		w = lv.Width - x
	}
	if y+h > lv.Height {
		h = lv.Height - y
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("MIC3: empty region")
	}

	bytesPerPixel := hdr.Channels
	if hdr.BitsPerSample == 16 {
		bytesPerPixel *= 2
	}

	// Determine which tiles overlap the region
	startTX := x / hdr.TileWidth
	startTY := y / hdr.TileHeight
	endTX := (x + w - 1) / hdr.TileWidth
	endTY := (y + h - 1) / hdr.TileHeight

	result := make([]byte, w*h*bytesPerPixel)

	for ty := startTY; ty <= endTY; ty++ {
		for tx := startTX; tx <= endTX; tx++ {
			globalIdx := lv.FirstTileIdx + ty*lv.TilesX + tx
			blob, err := ExtractTileBlob(data, entries, dataOffset, globalIdx)
			if err != nil {
				return nil, err
			}
			tile, err := decompressTileBlob(blob, hdr.TileWidth, hdr.TileHeight, hdr.Channels, hdr.BitsPerSample, hdr.ColorTransform)
			if err != nil {
				return nil, err
			}

			// Compute overlap between this tile and the requested region
			tileStartX := tx * hdr.TileWidth
			tileStartY := ty * hdr.TileHeight
			tileW := hdr.TileWidth
			tileH := hdr.TileHeight
			// Crop to actual image dimensions for edge tiles
			if tileStartX+tileW > lv.Width {
				tileW = lv.Width - tileStartX
			}
			if tileStartY+tileH > lv.Height {
				tileH = lv.Height - tileStartY
			}

			// Overlap rectangle in image coordinates
			ox0 := max(x, tileStartX)
			oy0 := max(y, tileStartY)
			ox1 := min(x+w, tileStartX+tileW)
			oy1 := min(y+h, tileStartY+tileH)

			for ry := oy0; ry < oy1; ry++ {
				srcOff := ((ry-tileStartY)*hdr.TileWidth + (ox0 - tileStartX)) * bytesPerPixel
				dstOff := ((ry-y)*w + (ox0 - x)) * bytesPerPixel
				copyLen := (ox1 - ox0) * bytesPerPixel
				copy(result[dstOff:dstOff+copyLen], tile[srcOff:srcOff+copyLen])
			}
		}
	}

	return result, nil
}

// ReadWSIHeader parses only the MIC3 header without decompressing tiles.
func ReadWSIHeader(data []byte) (*WSIHeader, error) {
	hdr, _, _, err := ReadMIC3Header(data)
	if err != nil {
		return nil, err
	}
	return &hdr, nil
}

// --- Tile blob encoding/decoding ---

// compressTileBlob compresses a single tile's pixel data into a tile blob.
// For RGB: applies YCoCg-R color transform, then compresses Y/Co/Cg planes.
// For greyscale: compresses the single plane.
func compressTileBlob(tilePixels []byte, tileWidth, tileHeight, channels, bitsPerSample int, colorTransform bool) ([]byte, error) {
	if channels == 3 && bitsPerSample == 8 {
		return compressRGBTileBlob(tilePixels, tileWidth, tileHeight, colorTransform)
	}
	return compressGreyTileBlob(tilePixels, tileWidth, tileHeight, bitsPerSample)
}

func compressRGBTileBlob(rgb []byte, width, height int, colorTransform bool) ([]byte, error) {
	var yPlane, coPlane, cgPlane []uint16

	if colorTransform {
		yPlane, coPlane, cgPlane = YCoCgRForward(rgb, width, height)
	} else {
		// Planar separation without color transform
		n := width * height
		yPlane = make([]uint16, n)  // R channel
		coPlane = make([]uint16, n) // G channel
		cgPlane = make([]uint16, n) // B channel
		for i := 0; i < n; i++ {
			yPlane[i] = uint16(rgb[i*3])
			coPlane[i] = uint16(rgb[i*3+1])
			cgPlane[i] = uint16(rgb[i*3+2])
		}
	}

	yBlob, err := compressWSIPlane(yPlane, width, height)
	if err != nil {
		return nil, fmt.Errorf("Y plane: %w", err)
	}
	coBlob, err := compressWSIPlane(coPlane, width, height)
	if err != nil {
		return nil, fmt.Errorf("Co plane: %w", err)
	}
	cgBlob, err := compressWSIPlane(cgPlane, width, height)
	if err != nil {
		return nil, fmt.Errorf("Cg plane: %w", err)
	}

	// Pack: [Y_len(u32)][Co_len(u32)][Cg_len(u32)][Y_data][Co_data][Cg_data]
	totalLen := 12 + len(yBlob) + len(coBlob) + len(cgBlob)
	out := make([]byte, totalLen)
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(yBlob)))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(coBlob)))
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(cgBlob)))
	off := 12
	copy(out[off:], yBlob)
	off += len(yBlob)
	copy(out[off:], coBlob)
	off += len(coBlob)
	copy(out[off:], cgBlob)

	return out, nil
}

func compressGreyTileBlob(pixelBytes []byte, width, height, bitsPerSample int) ([]byte, error) {
	plane := bytesToUint16Slice(pixelBytes, bitsPerSample)
	return compressWSIPlane(plane, width, height)
}

// compressWSIPlane compresses a single plane of uint16 data.
// Handles constant planes specially for efficiency.
func compressWSIPlane(plane []uint16, width, height int) ([]byte, error) {
	// Check for constant plane
	isConstant := true
	val := plane[0]
	maxVal := val
	for _, v := range plane[1:] {
		if v != val {
			isConstant = false
		}
		if v > maxVal {
			maxVal = v
		}
	}

	if isConstant {
		if val == 0 {
			return []byte{planeConstantZero}, nil
		}
		out := make([]byte, 3)
		out[0] = planeConstant
		binary.LittleEndian.PutUint16(out[1:], val)
		return out, nil
	}

	// Ensure maxVal is large enough for reasonable RLE midCount
	if maxVal < 255 {
		maxVal = 255
	}

	compressed, err := CompressSingleFrame(plane, width, height, maxVal)
	if err != nil {
		// Fallback: check if it's a known error that we can handle
		if errors.Is(err, ErrUseRLE) || errors.Is(err, ErrIncompressible) {
			// Store raw data as fallback
			raw := make([]byte, 1+len(plane)*2)
			raw[0] = planeRaw
			for i, v := range plane {
				binary.LittleEndian.PutUint16(raw[1+i*2:], v)
			}
			return raw, nil
		}
		return nil, err
	}

	out := make([]byte, 1+len(compressed))
	out[0] = planeCompressed
	copy(out[1:], compressed)
	return out, nil
}

// decompressTileBlob decompresses a tile blob back to pixel data.
func decompressTileBlob(blob []byte, tileWidth, tileHeight, channels, bitsPerSample int, colorTransform bool) ([]byte, error) {
	if channels == 3 && bitsPerSample == 8 {
		return decompressRGBTileBlob(blob, tileWidth, tileHeight, colorTransform)
	}
	return decompressGreyTileBlob(blob, tileWidth, tileHeight, bitsPerSample)
}

func decompressRGBTileBlob(blob []byte, width, height int, colorTransform bool) ([]byte, error) {
	if len(blob) < 12 {
		return nil, errors.New("MIC3: RGB tile blob too small")
	}

	yLen := int(binary.LittleEndian.Uint32(blob[0:4]))
	coLen := int(binary.LittleEndian.Uint32(blob[4:8]))
	cgLen := int(binary.LittleEndian.Uint32(blob[8:12]))
	off := 12

	if off+yLen+coLen+cgLen > len(blob) {
		return nil, errors.New("MIC3: RGB tile blob truncated")
	}

	n := width * height
	yPlane, err := decompressWSIPlane(blob[off:off+yLen], width, height, n)
	if err != nil {
		return nil, fmt.Errorf("Y plane: %w", err)
	}
	off += yLen

	coPlane, err := decompressWSIPlane(blob[off:off+coLen], width, height, n)
	if err != nil {
		return nil, fmt.Errorf("Co plane: %w", err)
	}
	off += coLen

	cgPlane, err := decompressWSIPlane(blob[off:off+cgLen], width, height, n)
	if err != nil {
		return nil, fmt.Errorf("Cg plane: %w", err)
	}

	if colorTransform {
		return YCoCgRInverse(yPlane, coPlane, cgPlane, width, height), nil
	}

	// Interleave planes back to RGB
	rgb := make([]byte, n*3)
	for i := 0; i < n; i++ {
		rgb[i*3] = byte(yPlane[i])
		rgb[i*3+1] = byte(coPlane[i])
		rgb[i*3+2] = byte(cgPlane[i])
	}
	return rgb, nil
}

func decompressGreyTileBlob(blob []byte, width, height, bitsPerSample int) ([]byte, error) {
	n := width * height
	plane, err := decompressWSIPlane(blob, width, height, n)
	if err != nil {
		return nil, err
	}
	return uint16ToBytes(plane, bitsPerSample), nil
}

// decompressWSIPlane decompresses a single plane from its blob.
func decompressWSIPlane(data []byte, width, height, n int) ([]uint16, error) {
	if len(data) == 0 {
		return nil, errors.New("empty plane data")
	}

	mode := data[0]
	switch mode {
	case planeConstantZero:
		return make([]uint16, n), nil

	case planeConstant:
		if len(data) < 3 {
			return nil, errors.New("constant plane data truncated")
		}
		val := binary.LittleEndian.Uint16(data[1:3])
		out := make([]uint16, n)
		for i := range out {
			out[i] = val
		}
		return out, nil

	case planeCompressed:
		return DecompressSingleFrame(data[1:], width, height)

	case planeRaw:
		if len(data) < 1+n*2 {
			return nil, errors.New("raw plane data truncated")
		}
		out := make([]uint16, n)
		for i := range out {
			out[i] = binary.LittleEndian.Uint16(data[1+i*2:])
		}
		return out, nil

	default:
		return nil, fmt.Errorf("unknown plane mode %d", mode)
	}
}

// --- Helper functions ---

// extractTileRGB extracts a tile from an image, zero-padding edge tiles.
func extractTileRGB(img []byte, imgWidth, imgHeight, tileWidth, tileHeight, tileX, tileY, channels, bitsPerSample int) []byte {
	bytesPerPixel := channels
	if bitsPerSample == 16 {
		bytesPerPixel *= 2
	}
	tile := make([]byte, tileWidth*tileHeight*bytesPerPixel)

	startX := tileX * tileWidth
	startY := tileY * tileHeight

	for ty := 0; ty < tileHeight; ty++ {
		srcY := startY + ty
		if srcY >= imgHeight {
			break
		}
		for tx := 0; tx < tileWidth; tx++ {
			srcX := startX + tx
			if srcX >= imgWidth {
				break
			}
			srcIdx := (srcY*imgWidth + srcX) * bytesPerPixel
			dstIdx := (ty*tileWidth + tx) * bytesPerPixel
			copy(tile[dstIdx:dstIdx+bytesPerPixel], img[srcIdx:srcIdx+bytesPerPixel])
		}
	}
	return tile
}

// cropTile removes padding from a tile, returning only the valid region.
func cropTile(tile []byte, tileWidth, tileHeight, actualWidth, actualHeight, channels, bitsPerSample int) []byte {
	bytesPerPixel := channels
	if bitsPerSample == 16 {
		bytesPerPixel *= 2
	}
	out := make([]byte, actualWidth*actualHeight*bytesPerPixel)
	for y := 0; y < actualHeight; y++ {
		srcOff := y * tileWidth * bytesPerPixel
		dstOff := y * actualWidth * bytesPerPixel
		copy(out[dstOff:dstOff+actualWidth*bytesPerPixel], tile[srcOff:srcOff+actualWidth*bytesPerPixel])
	}
	return out
}

// bytesToUint16Slice converts raw bytes to []uint16 based on bits per sample.
func bytesToUint16Slice(data []byte, bitsPerSample int) []uint16 {
	if bitsPerSample <= 8 {
		out := make([]uint16, len(data))
		for i, v := range data {
			out[i] = uint16(v)
		}
		return out
	}
	// 16-bit: little-endian pairs
	out := make([]uint16, len(data)/2)
	for i := range out {
		out[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return out
}

// uint16ToBytes converts []uint16 back to raw bytes.
func uint16ToBytes(data []uint16, bitsPerSample int) []byte {
	if bitsPerSample <= 8 {
		out := make([]byte, len(data))
		for i, v := range data {
			out[i] = byte(v)
		}
		return out
	}
	out := make([]byte, len(data)*2)
	for i, v := range data {
		binary.LittleEndian.PutUint16(out[i*2:], v)
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
