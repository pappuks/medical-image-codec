// Copyright 2021 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.
// Based on work Copyright 2018 Klaus Post, released user BSD License.
// Based on work Copyright (c) 2013, Yann Collet, released under BSD License.

package mic

import "fmt"

// bitWriter will write bits.
// First bit will be LSB of the first byte of output.
type bitWriterHuff struct {
	bitContainer uint64
	nBits        uint8
	out          []byte
}

func (b *bitWriterHuff) addBits16(value uint16, bits uint8) {
	for (bits + b.nBits) > 64 {
		b.flush32()
	}
	b.bitContainer |= uint64(value&bitMask16[bits&31]) << ((64 - b.nBits - bits) & 63)
	b.nBits += bits
}

// Add and flush if needed
func (b *bitWriterHuff) addBits32(value uint32, bits uint8) {
	if bits > 32 {
		fmt.Printf("*** bits > 32 %d\n", bits)
	}
	for (bits + b.nBits) > 64 {
		b.flush32()
	}
	b.bitContainer |= uint64(value&bitMask32[bits&63]) << ((64 - b.nBits - bits) & 63)
	b.nBits += bits
}

// flush32 will flush out, so there are at least 32 bits available for writing.
func (b *bitWriterHuff) flush32() {
	if b.nBits < 32 {
		return
	}
	b.out = append(b.out,
		byte(b.bitContainer>>56),
		byte(b.bitContainer>>48),
		byte(b.bitContainer>>40),
		byte(b.bitContainer>>32))
	b.nBits -= 32
	b.bitContainer <<= 32
}

// flushAlign will flush remaining full bytes and align to next byte boundary.
func (b *bitWriterHuff) flushAlign() {
	nbBytes := (b.nBits + 7) >> 3
	shift := 56
	for i := uint8(0); i < nbBytes; i++ {
		b.out = append(b.out, byte(b.bitContainer>>shift))
		shift -= 8
	}
	b.nBits = 0
	b.bitContainer = 0
}

// reset and continue writing by appending to out.
func (b *bitWriterHuff) reset(out []byte) {
	b.bitContainer = 0
	b.nBits = 0
	b.out = out
}
