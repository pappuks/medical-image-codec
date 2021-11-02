// Copyright 2018 Klaus Post. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// Based on work Copyright (c) 2013, Yann Collet, released under BSD License.

package mic

import (
	"encoding/binary"
	"errors"
)

// bitReader reads a bitstream in reverse.
// The last set bit indicates the start of the stream and is used
// for aligning the input.
type bitReaderHuff struct {
	in       []byte
	off      uint // next byte to read is at in[off - 1]
	value    uint64
	bitsRead uint8
	fwd      uint
	inLen    uint
}

func (b *bitReaderHuff) initFwd(in []byte) error {
	if len(in) < 1 {
		return errors.New("corrupt stream: too short")
	}
	b.in = in
	b.inLen = uint(len(b.in))
	b.off = uint(len(in))
	b.fwd = 0
	b.bitsRead = 64
	b.value = 0
	if len(in) >= 8 {
		b.fillFastStartFwd()
	} else {
		b.fillFwd()
		b.fillFwd()
	}
	return nil
}

func (b *bitReaderHuff) getBitsNFillFwd(n uint8) uint16 {
	if n <= 0 {
		return 0
	}
	if (n + b.bitsRead) > 64 {
		b.fillFwd()
	}
	return b.getBitsFastFwd(n)
}

func (b *bitReaderHuff) getBits32NFillFwd(n uint8) uint32 {
	if (n + b.bitsRead) > 64 {
		b.fillFwd()
	}
	return b.getBitsFast32Fwd(n)
}

func (b *bitReaderHuff) getBits32NFillFwdFast(n uint8) uint32 {
	if (n + b.bitsRead) > 64 {
		b.fillFastFwd()
	}
	return b.getBitsFast32Fwd(n)
}

// getBitsFast requires that at least one bit is requested every time.
// There are no checks if the buffer is filled.
func (b *bitReaderHuff) getBitsFastFwd(n uint8) uint16 {
	const regMask = 64 - 1
	v := uint16(b.value>>((64-b.bitsRead-n)&regMask)) & bitMask16[n&31]
	b.bitsRead += n
	return v
}

func (b *bitReaderHuff) getBitsFast32Fwd(n uint8) uint32 {
	const regMask = 64 - 1
	v := uint32(b.value>>((64-b.bitsRead-n)&regMask)) & bitMask32[n&63]
	b.bitsRead += n
	return v
}

// fillFast() will make sure at least 32 bits are available.
// There must be at least 4 bytes available.
func (b *bitReaderHuff) fillFastFwd() {
	if b.bitsRead < 32 {
		return
	}
	// 2 bounds checks.
	v := b.in[b.fwd : b.fwd+4]
	low := (uint32(v[0]) << 24) | (uint32(v[1]) << 16) | (uint32(v[2]) << 8) | (uint32(v[3]))
	b.value = (b.value << 32) | uint64(low)
	b.bitsRead -= 32
	b.fwd += 4
}

// fill() will make sure at least 32 bits are available.
func (b *bitReaderHuff) fillFwd() {
	if b.bitsRead < 32 {
		return
	}
	if b.inLen-b.fwd > 4 {
		v := b.in[b.fwd : b.fwd+4]
		low := (uint32(v[0]) << 24) | (uint32(v[1]) << 16) | (uint32(v[2]) << 8) | (uint32(v[3]))
		b.value = (b.value << 32) | uint64(low)
		b.bitsRead -= 32
		b.fwd += 4
		return
	}
	for b.inLen > b.fwd {
		b.value = (b.value << 8) | uint64(b.in[b.fwd])
		b.bitsRead -= 8
		b.fwd++
	}
}

// fillFastStart() assumes the bitreader is empty and there is at least 8 bytes to read.
func (b *bitReaderHuff) fillFastStartFwd() {
	// Do single re-slice to avoid bounds checks.
	b.value = binary.BigEndian.Uint64(b.in[b.fwd : b.fwd+8])
	b.bitsRead = 0
	b.fwd += 8
}
