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

// MIC2 container format for multi-frame 16-bit medical images.
//
//	Bytes 0-3:    Magic "MIC2"
//	Bytes 4-7:    Width (uint32 LE)
//	Bytes 8-11:   Height (uint32 LE)
//	Bytes 12-15:  Frame count (uint32 LE)
//	Byte  16:     Pipeline flags: bit0=spatial(1), bit1=temporal
//	Bytes 17-19:  Reserved (zero)
//	Bytes 20..:   Frame offset table: N x {offset_u32, length_u32}
//	After table:  Concatenated compressed frame blobs

const (
	mic2Magic      = "MIC2"
	mic2HeaderSize = 20
	mic2EntrySize  = 8 // 4 bytes offset + 4 bytes length

	PipelineSpatial = 0x01 // spatial delta+RLE+FSE (always set)
	PipelineTemporal = 0x02 // inter-frame temporal delta before spatial
)

// MIC2Header holds the parsed header of a MIC2 multiframe file.
type MIC2Header struct {
	Width      int
	Height     int
	FrameCount int
	Temporal   bool // true = inter-frame delta prediction
}

// MIC2FrameEntry describes one frame's compressed data location.
type MIC2FrameEntry struct {
	Offset uint32 // byte offset relative to data section start
	Length uint32 // compressed byte length
}

// WriteMIC2 writes a complete MIC2 container to w.
func WriteMIC2(w io.Writer, hdr MIC2Header, frames [][]byte) error {
	if len(frames) != hdr.FrameCount {
		return fmt.Errorf("frame count mismatch: header=%d, frames=%d", hdr.FrameCount, len(frames))
	}

	// Build header
	header := make([]byte, mic2HeaderSize)
	copy(header[0:4], mic2Magic)
	binary.LittleEndian.PutUint32(header[4:8], uint32(hdr.Width))
	binary.LittleEndian.PutUint32(header[8:12], uint32(hdr.Height))
	binary.LittleEndian.PutUint32(header[12:16], uint32(hdr.FrameCount))
	flags := byte(PipelineSpatial)
	if hdr.Temporal {
		flags |= PipelineTemporal
	}
	header[16] = flags
	// bytes 17-19 are zero (reserved)

	if _, err := w.Write(header); err != nil {
		return err
	}

	// Build and write frame offset table
	table := make([]byte, hdr.FrameCount*mic2EntrySize)
	offset := uint32(0)
	for i, frame := range frames {
		binary.LittleEndian.PutUint32(table[i*mic2EntrySize:], offset)
		binary.LittleEndian.PutUint32(table[i*mic2EntrySize+4:], uint32(len(frame)))
		offset += uint32(len(frame))
	}
	if _, err := w.Write(table); err != nil {
		return err
	}

	// Write compressed frame data
	for _, frame := range frames {
		if _, err := w.Write(frame); err != nil {
			return err
		}
	}

	return nil
}

// ReadMIC2Header parses the header and frame offset table from a MIC2 file.
// Returns the header, frame entries, and the byte offset where frame data begins.
func ReadMIC2Header(data []byte) (MIC2Header, []MIC2FrameEntry, int, error) {
	if len(data) < mic2HeaderSize {
		return MIC2Header{}, nil, 0, errors.New("MIC2: file too small")
	}

	magic := string(data[0:4])
	if magic != mic2Magic {
		return MIC2Header{}, nil, 0, fmt.Errorf("MIC2: invalid magic %q", magic)
	}

	hdr := MIC2Header{
		Width:      int(binary.LittleEndian.Uint32(data[4:8])),
		Height:     int(binary.LittleEndian.Uint32(data[8:12])),
		FrameCount: int(binary.LittleEndian.Uint32(data[12:16])),
		Temporal:   data[16]&PipelineTemporal != 0,
	}

	tableSize := hdr.FrameCount * mic2EntrySize
	dataOffset := mic2HeaderSize + tableSize
	if len(data) < dataOffset {
		return MIC2Header{}, nil, 0, errors.New("MIC2: file truncated in frame table")
	}

	entries := make([]MIC2FrameEntry, hdr.FrameCount)
	for i := 0; i < hdr.FrameCount; i++ {
		base := mic2HeaderSize + i*mic2EntrySize
		entries[i] = MIC2FrameEntry{
			Offset: binary.LittleEndian.Uint32(data[base:]),
			Length: binary.LittleEndian.Uint32(data[base+4:]),
		}
	}

	return hdr, entries, dataOffset, nil
}

// ExtractFrame returns the compressed bytes for a specific frame from a MIC2 file.
func ExtractFrame(data []byte, entries []MIC2FrameEntry, dataOffset int, frameIdx int) ([]byte, error) {
	if frameIdx < 0 || frameIdx >= len(entries) {
		return nil, fmt.Errorf("MIC2: frame index %d out of range [0, %d)", frameIdx, len(entries))
	}
	e := entries[frameIdx]
	start := dataOffset + int(e.Offset)
	end := start + int(e.Length)
	if end > len(data) {
		return nil, fmt.Errorf("MIC2: frame %d data extends beyond file", frameIdx)
	}
	return data[start:end], nil
}
