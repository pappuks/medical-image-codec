// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MIC3 container format for tiled whole-slide images with pyramid levels.
//
//	HEADER (48 bytes)
//	  Bytes  0-3:   Magic "MIC3"
//	  Bytes  4-7:   Format version (uint32 LE) = 1
//	  Bytes  8-11:  Full-res width (uint32 LE)
//	  Bytes 12-15:  Full-res height (uint32 LE)
//	  Bytes 16-19:  Tile width (uint32 LE)
//	  Bytes 20-23:  Tile height (uint32 LE)
//	  Bytes 24-25:  Channels (uint16 LE): 1=grey, 3=RGB
//	  Byte  26:     Bits per sample (uint8): 8 or 16
//	  Byte  27:     Flags (bit0=spatial, bit1=color_transform)
//	  Bytes 28-29:  Pyramid level count (uint16 LE)
//	  Bytes 30-31:  Reserved
//	  Bytes 32-39:  Total tile count (uint64 LE)
//	  Bytes 40-47:  Reserved
//
//	LEVEL DESCRIPTORS (N × 20 bytes)
//	  Per level: width(u32) + height(u32) + tilesX(u32) + tilesY(u32) + firstTileIdx(u32)
//
//	TILE OFFSET TABLE (M × 16 bytes)
//	  Per tile: offset(u64) + length(u64)
//
//	DATA SECTION: concatenated compressed tile blobs

const (
	mic3Magic       = "MIC3"
	mic3Version     = 1
	mic3HeaderSize  = 48
	mic3LevelSize   = 20
	mic3TileEntSize = 16

	FlagSpatial        = 0x01 // spatial delta prediction (always set)
	FlagColorTransform = 0x02 // YCoCg-R was applied
)

// WSIHeader holds metadata for a MIC3 WSI file.
type WSIHeader struct {
	Width          int
	Height         int
	TileWidth      int
	TileHeight     int
	Channels       int  // 1 (greyscale) or 3 (RGB)
	BitsPerSample  int  // 8 or 16
	ColorTransform bool // true if YCoCg-R was applied
	Levels         []WSILevel
}

// WSILevel describes one pyramid level.
type WSILevel struct {
	Width        int
	Height       int
	TilesX       int
	TilesY       int
	FirstTileIdx int
}

// WSITileEntry describes one tile's location in the data section.
type WSITileEntry struct {
	Offset uint64
	Length uint64
}

// WSIOptions configures WSI compression.
type WSIOptions struct {
	TileWidth      int  // Default: 256
	TileHeight     int  // Default: 256
	PyramidLevels  int  // 0 = auto
	ColorTransform bool // Default: true for RGB
	Workers        int  // 0 = runtime.GOMAXPROCS
}

func (o *WSIOptions) defaults(channels int) {
	if o.TileWidth == 0 {
		o.TileWidth = 256
	}
	if o.TileHeight == 0 {
		o.TileHeight = 256
	}
	if channels == 3 && !o.ColorTransform {
		o.ColorTransform = true
	}
}

// WriteMIC3 writes a complete MIC3 container.
func WriteMIC3(w io.Writer, hdr WSIHeader, tileBlobs [][]byte) error {
	totalTiles := 0
	for _, lv := range hdr.Levels {
		totalTiles += lv.TilesX * lv.TilesY
	}
	if len(tileBlobs) != totalTiles {
		return fmt.Errorf("MIC3: tile count mismatch: header implies %d, got %d", totalTiles, len(tileBlobs))
	}

	// Write header
	header := make([]byte, mic3HeaderSize)
	copy(header[0:4], mic3Magic)
	binary.LittleEndian.PutUint32(header[4:8], mic3Version)
	binary.LittleEndian.PutUint32(header[8:12], uint32(hdr.Width))
	binary.LittleEndian.PutUint32(header[12:16], uint32(hdr.Height))
	binary.LittleEndian.PutUint32(header[16:20], uint32(hdr.TileWidth))
	binary.LittleEndian.PutUint32(header[20:24], uint32(hdr.TileHeight))
	binary.LittleEndian.PutUint16(header[24:26], uint16(hdr.Channels))
	header[26] = byte(hdr.BitsPerSample)
	flags := byte(FlagSpatial)
	if hdr.ColorTransform {
		flags |= FlagColorTransform
	}
	header[27] = flags
	binary.LittleEndian.PutUint16(header[28:30], uint16(len(hdr.Levels)))
	// 30-31 reserved
	binary.LittleEndian.PutUint64(header[32:40], uint64(totalTiles))
	// 40-47 reserved

	if _, err := w.Write(header); err != nil {
		return err
	}

	// Write level descriptors
	for _, lv := range hdr.Levels {
		ld := make([]byte, mic3LevelSize)
		binary.LittleEndian.PutUint32(ld[0:4], uint32(lv.Width))
		binary.LittleEndian.PutUint32(ld[4:8], uint32(lv.Height))
		binary.LittleEndian.PutUint32(ld[8:12], uint32(lv.TilesX))
		binary.LittleEndian.PutUint32(ld[12:16], uint32(lv.TilesY))
		binary.LittleEndian.PutUint32(ld[16:20], uint32(lv.FirstTileIdx))
		if _, err := w.Write(ld); err != nil {
			return err
		}
	}

	// Write tile offset table
	offset := uint64(0)
	for _, blob := range tileBlobs {
		entry := make([]byte, mic3TileEntSize)
		binary.LittleEndian.PutUint64(entry[0:8], offset)
		binary.LittleEndian.PutUint64(entry[8:16], uint64(len(blob)))
		if _, err := w.Write(entry); err != nil {
			return err
		}
		offset += uint64(len(blob))
	}

	// Write tile data
	for _, blob := range tileBlobs {
		if _, err := w.Write(blob); err != nil {
			return err
		}
	}

	return nil
}

// ReadMIC3Header parses the MIC3 header, level descriptors, and tile offset table.
// Returns the header, tile entries, and byte offset where tile data begins.
func ReadMIC3Header(data []byte) (WSIHeader, []WSITileEntry, int, error) {
	if len(data) < mic3HeaderSize {
		return WSIHeader{}, nil, 0, errors.New("MIC3: file too small")
	}
	if string(data[0:4]) != mic3Magic {
		return WSIHeader{}, nil, 0, fmt.Errorf("MIC3: invalid magic %q", string(data[0:4]))
	}
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != mic3Version {
		return WSIHeader{}, nil, 0, fmt.Errorf("MIC3: unsupported version %d", version)
	}

	hdr := WSIHeader{
		Width:          int(binary.LittleEndian.Uint32(data[8:12])),
		Height:         int(binary.LittleEndian.Uint32(data[12:16])),
		TileWidth:      int(binary.LittleEndian.Uint32(data[16:20])),
		TileHeight:     int(binary.LittleEndian.Uint32(data[20:24])),
		Channels:       int(binary.LittleEndian.Uint16(data[24:26])),
		BitsPerSample:  int(data[26]),
		ColorTransform: data[27]&FlagColorTransform != 0,
	}

	levelCount := int(binary.LittleEndian.Uint16(data[28:30]))
	totalTiles := int(binary.LittleEndian.Uint64(data[32:40]))

	// Read level descriptors
	lvOffset := mic3HeaderSize
	if len(data) < lvOffset+levelCount*mic3LevelSize {
		return WSIHeader{}, nil, 0, errors.New("MIC3: truncated level descriptors")
	}
	hdr.Levels = make([]WSILevel, levelCount)
	for i := 0; i < levelCount; i++ {
		base := lvOffset + i*mic3LevelSize
		hdr.Levels[i] = WSILevel{
			Width:        int(binary.LittleEndian.Uint32(data[base:])),
			Height:       int(binary.LittleEndian.Uint32(data[base+4:])),
			TilesX:       int(binary.LittleEndian.Uint32(data[base+8:])),
			TilesY:       int(binary.LittleEndian.Uint32(data[base+12:])),
			FirstTileIdx: int(binary.LittleEndian.Uint32(data[base+16:])),
		}
	}

	// Read tile offset table
	tileTableOffset := lvOffset + levelCount*mic3LevelSize
	if len(data) < tileTableOffset+totalTiles*mic3TileEntSize {
		return WSIHeader{}, nil, 0, errors.New("MIC3: truncated tile offset table")
	}
	entries := make([]WSITileEntry, totalTiles)
	for i := 0; i < totalTiles; i++ {
		base := tileTableOffset + i*mic3TileEntSize
		entries[i] = WSITileEntry{
			Offset: binary.LittleEndian.Uint64(data[base:]),
			Length: binary.LittleEndian.Uint64(data[base+8:]),
		}
	}

	dataOffset := tileTableOffset + totalTiles*mic3TileEntSize
	return hdr, entries, dataOffset, nil
}

// ExtractTileBlob returns the compressed bytes for a specific tile from MIC3 data.
func ExtractTileBlob(data []byte, entries []WSITileEntry, dataOffset int, tileIdx int) ([]byte, error) {
	if tileIdx < 0 || tileIdx >= len(entries) {
		return nil, fmt.Errorf("MIC3: tile index %d out of range [0, %d)", tileIdx, len(entries))
	}
	e := entries[tileIdx]
	start := dataOffset + int(e.Offset)
	end := start + int(e.Length)
	if end > len(data) {
		return nil, fmt.Errorf("MIC3: tile %d data extends beyond file", tileIdx)
	}
	return data[start:end], nil
}

// computeLevels builds pyramid level descriptors from image and tile dimensions.
func computeLevels(width, height, tileWidth, tileHeight, numLevels int) []WSILevel {
	levels := make([]WSILevel, numLevels)
	w, h := width, height
	tileIdx := 0

	for i := 0; i < numLevels; i++ {
		tilesX := (w + tileWidth - 1) / tileWidth
		tilesY := (h + tileHeight - 1) / tileHeight
		levels[i] = WSILevel{
			Width:        w,
			Height:       h,
			TilesX:       tilesX,
			TilesY:       tilesY,
			FirstTileIdx: tileIdx,
		}
		tileIdx += tilesX * tilesY
		w = w / 2
		h = h / 2
		if w == 0 {
			w = 1
		}
		if h == 0 {
			h = 1
		}
	}
	return levels
}

// autoLevelCount computes pyramid level count: keep halving until image fits in one tile.
func autoLevelCount(width, height, tileWidth, tileHeight int) int {
	levels := 1
	w, h := width, height
	for w > tileWidth || h > tileHeight {
		w = w / 2
		h = h / 2
		levels++
		if w <= 1 && h <= 1 {
			break
		}
	}
	return levels
}
