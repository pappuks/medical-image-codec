// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"
)

const (
	eightStateMagic0 = 0xFF
	eightStateMagic1 = 0x08
)

// RANSCompressU16EightState compresses in[] using eight independent rANS state
// machines interleaved across symbol positions 0, 1, …, 7, 8, 9, …
//
// The arithmetic state update (state = bias + (xL>>k) − freq) is more
// SIMD-friendly than the tANS stateTable pointer-chase on the encode side,
// and the 8-lane decode loop exposes more instruction-level parallelism than
// the 4-state tANS variant.
//
// Output format: [0xFF][0x08][count uint32 LE][FSE header][bitstream]
//
// The FSE header (histogram encoding) is identical to the single-state FSE
// format; only the entropy bitstream structure differs.
func RANSCompressU16EightState(in []uint16, s *ScratchU16) ([]byte, error) {
	if len(in) <= 7 {
		return nil, ErrIncompressible
	}
	s, err := s.prepare(in, nil)
	if err != nil {
		return nil, err
	}

	maxCount := s.maxCount
	if maxCount == 0 {
		maxCount = s.countSimple(in)
	}
	s.clearCount = true
	s.maxCount = 0
	if maxCount == len(in) {
		return nil, ErrUseRLE
	}
	if maxCount == 1 || maxCount < (len(in)>>15) {
		return nil, ErrIncompressible
	}
	s.optimalTableLog()
	if err = s.normalizeCount(); err != nil {
		return nil, err
	}
	if err = s.writeCount(); err != nil {
		return nil, err
	}
	if err = s.ransCompress8State(in); err != nil {
		return nil, err
	}
	s.Out = s.bw.out
	if len(s.Out) >= len(in)*2 {
		return nil, ErrIncompressible
	}

	hdr := make([]byte, 6)
	hdr[0] = eightStateMagic0
	hdr[1] = eightStateMagic1
	binary.LittleEndian.PutUint32(hdr[2:], uint32(len(in)))
	return append(hdr, s.Out...), nil
}

// RANSDecompressU16EightState decompresses data produced by RANSCompressU16EightState.
func RANSDecompressU16EightState(b []byte, s *ScratchU16) ([]uint16, error) {
	if len(b) < 6 || b[0] != eightStateMagic0 || b[1] != eightStateMagic1 {
		return nil, errors.New("rans8state: missing magic bytes")
	}
	count := int(binary.LittleEndian.Uint32(b[2:6]))
	b = b[6:]

	s, err := s.prepare(nil, b)
	if err != nil {
		return nil, err
	}
	s.OutU16 = s.OutU16[:0]
	if err = s.readNCount(); err != nil {
		return nil, err
	}
	if err = s.buildRansDecTable(); err != nil {
		return nil, err
	}
	if err = s.ransDecompress8State(count); err != nil {
		return nil, err
	}
	return s.OutU16, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Internal compress/decompress
// ──────────────────────────────────────────────────────────────────────────────

// ransCompress8State encodes src using eight independent rANS states.
// Symbols at positions i%8 == 0,1,2,3,4,5,6,7 are handled by states A–H.
// Encoding proceeds backwards so the decoder can read forward.
func (s *ScratchU16) ransCompress8State(src []uint16) error {
	if len(src) <= 7 {
		return errors.New("ransCompress8State: src too small")
	}

	tt, err := s.buildRansEncTable()
	if err != nil {
		return err
	}

	s.bw.reset(s.Out)
	tableSize := uint32(1 << s.actualTableLog)

	// Eight encoder states, all initialised to 0 (the "empty" rANS state).
	var sA, sB, sC, sD, sE, sF, sG, sH uint32

	ip := len(src)

	// Align ip to a multiple of 8 by encoding the tail symbols first.
	// Cases 1-4: at most 4×tableLog ≤ 56 bits from nBits=0 — no flush needed.
	// Cases 5-7: add flush32 calls every 2 symbols to stay within the 64-bit buffer.
	switch ip & 7 {
	case 1:
		sA = ransEncodeStep(sA, tt[src[ip-1]], tableSize, &s.bw)
		ip--
	case 2:
		sB = ransEncodeStep(sB, tt[src[ip-1]], tableSize, &s.bw)
		sA = ransEncodeStep(sA, tt[src[ip-2]], tableSize, &s.bw)
		ip -= 2
	case 3:
		sC = ransEncodeStep(sC, tt[src[ip-1]], tableSize, &s.bw)
		sB = ransEncodeStep(sB, tt[src[ip-2]], tableSize, &s.bw)
		sA = ransEncodeStep(sA, tt[src[ip-3]], tableSize, &s.bw)
		ip -= 3
	case 4:
		sD = ransEncodeStep(sD, tt[src[ip-1]], tableSize, &s.bw)
		sC = ransEncodeStep(sC, tt[src[ip-2]], tableSize, &s.bw)
		sB = ransEncodeStep(sB, tt[src[ip-3]], tableSize, &s.bw)
		sA = ransEncodeStep(sA, tt[src[ip-4]], tableSize, &s.bw)
		ip -= 4
	case 5:
		sE = ransEncodeStep(sE, tt[src[ip-1]], tableSize, &s.bw)
		sD = ransEncodeStep(sD, tt[src[ip-2]], tableSize, &s.bw)
		s.bw.flush32()
		sC = ransEncodeStep(sC, tt[src[ip-3]], tableSize, &s.bw)
		sB = ransEncodeStep(sB, tt[src[ip-4]], tableSize, &s.bw)
		s.bw.flush32()
		sA = ransEncodeStep(sA, tt[src[ip-5]], tableSize, &s.bw)
		ip -= 5
	case 6:
		sF = ransEncodeStep(sF, tt[src[ip-1]], tableSize, &s.bw)
		sE = ransEncodeStep(sE, tt[src[ip-2]], tableSize, &s.bw)
		s.bw.flush32()
		sD = ransEncodeStep(sD, tt[src[ip-3]], tableSize, &s.bw)
		sC = ransEncodeStep(sC, tt[src[ip-4]], tableSize, &s.bw)
		s.bw.flush32()
		sB = ransEncodeStep(sB, tt[src[ip-5]], tableSize, &s.bw)
		sA = ransEncodeStep(sA, tt[src[ip-6]], tableSize, &s.bw)
		ip -= 6
	case 7:
		sG = ransEncodeStep(sG, tt[src[ip-1]], tableSize, &s.bw)
		sF = ransEncodeStep(sF, tt[src[ip-2]], tableSize, &s.bw)
		s.bw.flush32()
		sE = ransEncodeStep(sE, tt[src[ip-3]], tableSize, &s.bw)
		sD = ransEncodeStep(sD, tt[src[ip-4]], tableSize, &s.bw)
		s.bw.flush32()
		sC = ransEncodeStep(sC, tt[src[ip-5]], tableSize, &s.bw)
		sB = ransEncodeStep(sB, tt[src[ip-6]], tableSize, &s.bw)
		s.bw.flush32()
		sA = ransEncodeStep(sA, tt[src[ip-7]], tableSize, &s.bw)
		ip -= 7
	}

	// Main loop: 8 symbols per iteration, 4 flush32 calls (2 symbols per flush).
	// Each ransEncodeStep writes at most tableLog bits (≤ 14 for medical images).
	// With flush32 ensuring nBits < 32 before each pair: max nBits = 31+28 = 59 < 64.
	for ip >= 8 {
		s.bw.flush32()
		sH = ransEncodeStep(sH, tt[src[ip-1]], tableSize, &s.bw)
		sG = ransEncodeStep(sG, tt[src[ip-2]], tableSize, &s.bw)
		s.bw.flush32()
		sF = ransEncodeStep(sF, tt[src[ip-3]], tableSize, &s.bw)
		sE = ransEncodeStep(sE, tt[src[ip-4]], tableSize, &s.bw)
		s.bw.flush32()
		sD = ransEncodeStep(sD, tt[src[ip-5]], tableSize, &s.bw)
		sC = ransEncodeStep(sC, tt[src[ip-6]], tableSize, &s.bw)
		s.bw.flush32()
		sB = ransEncodeStep(sB, tt[src[ip-7]], tableSize, &s.bw)
		sA = ransEncodeStep(sA, tt[src[ip-8]], tableSize, &s.bw)
		ip -= 8
	}

	// Write final states (H last → decoded first by A; encoder reversal).
	// Each final state is in [0, L), written as tableLog bits.
	// Decoder reads: A first, B, …, H last.
	tl := s.actualTableLog
	s.bw.flush32()
	s.bw.addBits32NC(sH, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sG, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sF, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sE, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sD, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sC, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sB, tl)
	s.bw.flush32()
	s.bw.addBits32NC(sA, tl)
	return s.bw.close()
}

// ransDecompress8State decodes a rANS 8-state bitstream into s.OutU16.
// count is the exact number of symbols to decode.
func (s *ScratchU16) ransDecompress8State(count int) error {
	br := &s.bits
	if err := br.init(s.brForDecomp.unread()); err != nil {
		return err
	}

	// Read initial states: A first (last written by encoder = top of
	// reversed stream).
	tl := s.actualTableLog
	var sA, sB, sC, sD, sE, sF, sG, sH uint32
	sA = br.getBits32(tl)
	sB = br.getBits32(tl)
	br.fill()
	sC = br.getBits32(tl)
	sD = br.getBits32(tl)
	br.fill()
	sE = br.getBits32(tl)
	sF = br.getBits32(tl)
	br.fill()
	sG = br.getBits32(tl)
	sH = br.getBits32(tl)

	var tmp = s.ct.tableSymbol[:65536]
	var off uint16
	dt := s.decTable
	remaining := count

	// Native (assembly) fast path for bulk decoding.
	// Only valid for the !zeroBits path (all nbBits > 0).
	// Requires br.off >= 16 so that up to 4 fillFast calls (each consuming 4 bytes)
	// can fire safely in the assembly kernel.
	if !s.zeroBits && remaining >= 8 && br.off >= 16 && len(dt) > 0 {
		bufAvail := int(^uint16(0)-off) + 1 // = 65536 − int(off)
		canDo := remaining
		if canDo > bufAvail {
			canDo = bufAvail
		}
		canDo &^= 7 // round down to multiple of 8
		if canDo >= 8 {
			states := [8]uint32{sA, sB, sC, sD, sE, sF, sG, sH}
			n := rans8StateDecompNative(
				unsafe.Pointer(&dt[0]),
				unsafe.Pointer(br),
				unsafe.Pointer(&states[0]),
				unsafe.Pointer(&tmp[off]),
				canDo,
			)
			sA, sB, sC, sD = states[0], states[1], states[2], states[3]
			sE, sF, sG, sH = states[4], states[5], states[6], states[7]
			off += uint16(n)
			remaining -= n
			if off == 0 && n > 0 {
				s.OutU16 = append(s.OutU16, tmp...)
				if len(s.OutU16) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)",
						len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	}

	// Pure-Go fast path: !zeroBits (all nbBits > 0).
	// Four fillFast per 8-symbol iteration (2 symbols per fill) ensures bitsRead
	// never exceeds 64: after fillFast bitsRead < 32, plus 2×tableLog ≤ 28 → max 59.
	// Loop condition br.off >= 16 guarantees all 4 fills have at least 4 bytes.
	if !s.zeroBits {
		for br.off >= 16 && remaining >= 8 {
			br.fillFast()
			nA := dt[sA]
			nB := dt[sB]
			lowA := br.getBitsFast32(nA.nbBits)
			lowB := br.getBitsFast32(nB.nbBits)
			sA = nA.newState + lowA
			sB = nB.newState + lowB

			br.fillFast()
			nC := dt[sC]
			nD := dt[sD]
			lowC := br.getBitsFast32(nC.nbBits)
			lowD := br.getBitsFast32(nD.nbBits)
			sC = nC.newState + lowC
			sD = nD.newState + lowD

			br.fillFast()
			nE := dt[sE]
			nF := dt[sF]
			lowE := br.getBitsFast32(nE.nbBits)
			lowF := br.getBitsFast32(nF.nbBits)
			sE = nE.newState + lowE
			sF = nF.newState + lowF

			br.fillFast()
			nG := dt[sG]
			nH := dt[sH]
			lowG := br.getBitsFast32(nG.nbBits)
			lowH := br.getBitsFast32(nH.nbBits)
			sG = nG.newState + lowG
			sH = nH.newState + lowH

			tmp[off+0] = nA.symbol
			tmp[off+1] = nB.symbol
			tmp[off+2] = nC.symbol
			tmp[off+3] = nD.symbol
			tmp[off+4] = nE.symbol
			tmp[off+5] = nF.symbol
			tmp[off+6] = nG.symbol
			tmp[off+7] = nH.symbol
			off += 8
			remaining -= 8

			if off == 0 {
				s.OutU16 = append(s.OutU16, tmp...)
				if len(s.OutU16) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)",
						len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	} else {
		// Safe path: zeroBits possible (some symbols have nbBits == 0).
		for br.off >= 16 && remaining >= 8 {
			br.fillFast()
			nA := &dt[sA]
			nB := &dt[sB]
			lowA := br.getBits32(nA.nbBits)
			lowB := br.getBits32(nB.nbBits)
			sA = nA.newState + lowA
			sB = nB.newState + lowB

			br.fillFast()
			nC := &dt[sC]
			nD := &dt[sD]
			lowC := br.getBits32(nC.nbBits)
			lowD := br.getBits32(nD.nbBits)
			sC = nC.newState + lowC
			sD = nD.newState + lowD

			br.fillFast()
			nE := &dt[sE]
			nF := &dt[sF]
			lowE := br.getBits32(nE.nbBits)
			lowF := br.getBits32(nF.nbBits)
			sE = nE.newState + lowE
			sF = nF.newState + lowF

			br.fillFast()
			nG := &dt[sG]
			nH := &dt[sH]
			lowG := br.getBits32(nG.nbBits)
			lowH := br.getBits32(nH.nbBits)
			sG = nG.newState + lowG
			sH = nH.newState + lowH

			tmp[off+0] = nA.symbol
			tmp[off+1] = nB.symbol
			tmp[off+2] = nC.symbol
			tmp[off+3] = nD.symbol
			tmp[off+4] = nE.symbol
			tmp[off+5] = nF.symbol
			tmp[off+6] = nG.symbol
			tmp[off+7] = nH.symbol
			off += 8
			remaining -= 8

			if off == 0 {
				s.OutU16 = append(s.OutU16, tmp...)
				if len(s.OutU16) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)",
						len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	}
	s.OutU16 = append(s.OutU16, tmp[:off]...)

	// Tail: drain remaining symbols in A, B, …, H order.
	states := [8]*uint32{&sA, &sB, &sC, &sD, &sE, &sF, &sG, &sH}
	lane := 0
	for remaining > 0 {
		br.fill()
		n := dt[*states[lane]]
		bits := br.getBits32(n.nbBits)
		*states[lane] = n.newState + bits
		s.OutU16 = append(s.OutU16, n.symbol)
		lane = (lane + 1) & 7
		remaining--
	}

	return br.close()
}
