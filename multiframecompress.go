// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"bytes"
	"fmt"
)

// CompressSingleFrame compresses a single frame of 16-bit pixel data
// using the Delta+RLE+FSE pipeline. Returns the FSE-compressed bytes.
func CompressSingleFrame(pixels []uint16, width, height int, maxValue uint16) ([]byte, error) {
	var drc DeltaRleCompressU16
	deltaComp, err := drc.Compress(pixels, width, height, maxValue)
	if err != nil {
		return nil, fmt.Errorf("delta+RLE compress: %w", err)
	}

	var s ScratchU16
	fseComp, err := FSECompressU16(deltaComp, &s)
	if err != nil {
		return nil, fmt.Errorf("FSE compress: %w", err)
	}

	return fseComp, nil
}

// DecompressSingleFrame decompresses FSE-compressed bytes back to 16-bit pixels.
func DecompressSingleFrame(compressed []byte, width, height int) ([]uint16, error) {
	var s ScratchU16
	rleSymbols, err := FSEDecompressU16(compressed, &s)
	if err != nil {
		return nil, fmt.Errorf("FSE decompress: %w", err)
	}

	var drd DeltaRleDecompressU16
	drd.Decompress(rleSymbols, width, height)
	return drd.Out, nil
}

// compressResidualFrame compresses temporal residual data using RLE+FSE only
// (no spatial delta, since zigzag-encoded temporal residuals lack spatial correlation).
func compressResidualFrame(residuals []uint16, maxValue uint16) ([]byte, error) {
	var rle RleCompressU16
	rle.Init(len(residuals), 1, maxValue)
	rleOut := rle.Compress(residuals)

	var s ScratchU16
	fseComp, err := FSECompressU16(rleOut, &s)
	if err != nil {
		return nil, fmt.Errorf("FSE compress: %w", err)
	}

	return fseComp, nil
}

// decompressResidualFrame decompresses RLE+FSE compressed temporal residual data.
func decompressResidualFrame(compressed []byte) ([]uint16, error) {
	var s ScratchU16
	rleData, err := FSEDecompressU16(compressed, &s)
	if err != nil {
		return nil, fmt.Errorf("FSE decompress: %w", err)
	}

	var rle RleDecompressU16
	rle.Init(rleData)
	return rle.Decompress(), nil
}

// CompressMultiFrame compresses N frames into MIC2 format.
// If temporal is true, inter-frame delta prediction is applied before spatial compression.
func CompressMultiFrame(frames [][]uint16, width, height int, maxValue uint16, temporal bool) ([]byte, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames to compress")
	}

	frameBlobs := make([][]byte, len(frames))

	for i, frame := range frames {
		var blob []byte
		var err error

		if temporal && i > 0 {
			// Apply temporal delta: zigzag-encoded residual
			residuals := TemporalDeltaEncode(frame, frames[i-1])
			// Find max residual value for RLE bit depth
			var resMax uint16
			for _, v := range residuals {
				if v > resMax {
					resMax = v
				}
			}
			blob, err = compressResidualFrame(residuals, resMax)
		} else {
			blob, err = CompressSingleFrame(frame, width, height, maxValue)
		}

		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		frameBlobs[i] = blob
	}

	hdr := MIC2Header{
		Width:      width,
		Height:     height,
		FrameCount: len(frames),
		Temporal:   temporal,
	}

	var buf bytes.Buffer
	if err := WriteMIC2(&buf, hdr, frameBlobs); err != nil {
		return nil, fmt.Errorf("write MIC2: %w", err)
	}

	return buf.Bytes(), nil
}

// DecompressMultiFrame decompresses all frames from a MIC2 file.
func DecompressMultiFrame(data []byte) ([][]uint16, MIC2Header, error) {
	hdr, entries, dataOffset, err := ReadMIC2Header(data)
	if err != nil {
		return nil, MIC2Header{}, err
	}

	frames := make([][]uint16, hdr.FrameCount)
	var prevFrame []uint16

	for i := 0; i < hdr.FrameCount; i++ {
		compressed, err := ExtractFrame(data, entries, dataOffset, i)
		if err != nil {
			return nil, MIC2Header{}, err
		}

		var pixels []uint16
		if hdr.Temporal && i > 0 {
			residuals, err := decompressResidualFrame(compressed)
			if err != nil {
				return nil, MIC2Header{}, fmt.Errorf("frame %d: %w", i, err)
			}
			pixels = TemporalDeltaDecode(residuals, prevFrame)
		} else {
			pixels, err = DecompressSingleFrame(compressed, hdr.Width, hdr.Height)
			if err != nil {
				return nil, MIC2Header{}, fmt.Errorf("frame %d: %w", i, err)
			}
		}

		frames[i] = pixels
		prevFrame = frames[i]
	}

	return frames, hdr, nil
}

// DecompressFrame decompresses a single frame from a MIC2 file.
// For independent mode, any frame can be decoded directly.
// For temporal mode, frames 0..frameIdx are decoded sequentially.
func DecompressFrame(data []byte, frameIdx int) ([]uint16, MIC2Header, error) {
	hdr, entries, dataOffset, err := ReadMIC2Header(data)
	if err != nil {
		return nil, MIC2Header{}, err
	}

	if frameIdx < 0 || frameIdx >= hdr.FrameCount {
		return nil, MIC2Header{}, fmt.Errorf("frame index %d out of range [0, %d)", frameIdx, hdr.FrameCount)
	}

	if !hdr.Temporal {
		// Independent mode: decode just the requested frame
		compressed, err := ExtractFrame(data, entries, dataOffset, frameIdx)
		if err != nil {
			return nil, MIC2Header{}, err
		}
		pixels, err := DecompressSingleFrame(compressed, hdr.Width, hdr.Height)
		if err != nil {
			return nil, MIC2Header{}, fmt.Errorf("frame %d: %w", frameIdx, err)
		}
		return pixels, hdr, nil
	}

	// Temporal mode: must decode sequentially from frame 0
	var prevFrame []uint16
	for i := 0; i <= frameIdx; i++ {
		compressed, err := ExtractFrame(data, entries, dataOffset, i)
		if err != nil {
			return nil, MIC2Header{}, err
		}

		var pixels []uint16
		if i > 0 {
			residuals, err := decompressResidualFrame(compressed)
			if err != nil {
				return nil, MIC2Header{}, fmt.Errorf("frame %d: %w", i, err)
			}
			pixels = TemporalDeltaDecode(residuals, prevFrame)
		} else {
			pixels, err = DecompressSingleFrame(compressed, hdr.Width, hdr.Height)
			if err != nil {
				return nil, MIC2Header{}, fmt.Errorf("frame %d: %w", i, err)
			}
		}

		prevFrame = pixels
	}

	return prevFrame, hdr, nil
}
