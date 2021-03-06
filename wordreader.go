// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
// Based on work Copyright 2018 Klaus Post, released user BSD License.
// Based on work Copyright (c) 2013, Yann Collet, released under BSD License.

package mic

// wordReader provides a word (16bit) reader that reads
// little endian values from a uint16 stream.
// The input stream is manually advanced.
// The reader performs no bounds checks.
type wordReader struct {
	b   []uint16
	off int
}

// init will initialize the reader and set the input.
func (b *wordReader) init(in []uint16) {
	b.b = in
	b.off = 0
}

// Uint32 returns a little endian uint32 starting at current offset.
func (b wordReader) Uint32() uint32 {
	b2 := b.b[b.off:]
	b2 = b2[:2]
	v1 := uint32(b2[1])
	v0 := uint32(b2[0])
	return v0 | (v1 << 16)
}

// remain will return the number of words remaining.
func (b wordReader) remain() int {
	return len(b.b) - b.off
}
