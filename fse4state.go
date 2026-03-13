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
	fourStateMagic0 = 0xFF
	fourStateMagic1 = 0x04
)

// FSECompressU16FourState compresses in[] using four independent FSE state machines.
// Symbols at positions mod 4 == 0,1,2,3 are handled by stateA,B,C,D respectively.
// The four states run in parallel on alternating symbols, breaking serial dependency
// chains so the CPU's out-of-order engine can execute all four pipelines concurrently.
// Output format: [0xFF][0x04][count uint32 LE][FSE header][bitstream]
func FSECompressU16FourState(in []uint16, s *ScratchU16) ([]byte, error) {
	if len(in) <= 3 {
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
	if err = s.buildCTable(); err != nil {
		return nil, err
	}
	if err = s.compress4State(in); err != nil {
		return nil, err
	}
	s.Out = s.bw.out

	if len(s.Out) >= len(in)*2 {
		return nil, ErrIncompressible
	}

	hdr := make([]byte, 6)
	hdr[0] = fourStateMagic0
	hdr[1] = fourStateMagic1
	binary.LittleEndian.PutUint32(hdr[2:], uint32(len(in)))
	out := append(hdr, s.Out...)
	return out, nil
}

// FSEDecompressU16FourState decompresses data produced by FSECompressU16FourState.
func FSEDecompressU16FourState(b []byte, s *ScratchU16) ([]uint16, error) {
	if len(b) < 6 || b[0] != fourStateMagic0 || b[1] != fourStateMagic1 {
		return nil, errors.New("fse4state: missing magic bytes")
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
	if err = s.buildDtable(); err != nil {
		return nil, err
	}
	if err = s.decompress4State(count); err != nil {
		return nil, err
	}
	return s.OutU16, nil
}

// compress4State encodes src using four independent FSE states into s.bw.
// Symbols at positions mod 4 == 0,1,2,3 are handled by stateA,B,C,D respectively.
// Encoding proceeds backwards so the decoder can read forward.
func (s *ScratchU16) compress4State(src []uint16) error {
	if len(src) <= 3 {
		return errors.New("compress4State: src too small")
	}
	tt := s.ct.symbolTT[:len(s.ct.symbolTT)]
	s.bw.reset(s.Out)

	var sA, sB, sC, sD cStateU16
	sA.init(&s.bw, &s.ct, s.actualTableLog)
	sB.init(&s.bw, &s.ct, s.actualTableLog)
	sC.init(&s.bw, &s.ct, s.actualTableLog)
	sD.init(&s.bw, &s.ct, s.actualTableLog)

	ip := len(src)

	// Align ip to a multiple of 4.
	// When ip%4==k, the symbol at position ip-1 has 0-based index (ip-1),
	// and (ip-1) mod 4 determines which state handles it.
	switch ip & 3 {
	case 1:
		// pos ip-1 ≡ 0 mod 4 → stateA
		sA.encode(tt[src[ip-1]])
		ip--
	case 2:
		// pos ip-1 ≡ 1 → stateB; pos ip-2 ≡ 0 → stateA
		sB.encode(tt[src[ip-1]])
		sA.encode(tt[src[ip-2]])
		ip -= 2
	case 3:
		// pos ip-1 ≡ 2 → stateC; ip-2 ≡ 1 → stateB; ip-3 ≡ 0 → stateA
		sC.encode(tt[src[ip-1]])
		sB.encode(tt[src[ip-2]])
		sA.encode(tt[src[ip-3]])
		ip -= 3
	}

	// Main loop: 4 symbols per iteration.
	// With ip%4==0: ip-1 ≡ 3→D, ip-2 ≡ 2→C, ip-3 ≡ 1→B, ip-4 ≡ 0→A
	switch {
	case !s.zeroBits && s.actualTableLog <= 8:
		// 4 × 8 bits = 32 bits max per iteration → one flush32 sufficient.
		for ip >= 4 {
			s.bw.flush32()
			sD.encode(tt[src[ip-1]])
			sC.encode(tt[src[ip-2]])
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	case !s.zeroBits:
		// Up to 4 × tableLog bits; flush every 2 symbols to stay within 64-bit buffer.
		for ip >= 4 {
			s.bw.flush32()
			sD.encode(tt[src[ip-1]])
			sC.encode(tt[src[ip-2]])
			s.bw.flush32()
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	case s.actualTableLog <= 8:
		for ip >= 4 {
			s.bw.flush32()
			sD.encode(tt[src[ip-1]])
			sC.encode(tt[src[ip-2]])
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	default:
		for ip >= 4 {
			s.bw.flush32()
			sD.encode(tt[src[ip-1]])
			sC.encode(tt[src[ip-2]])
			s.bw.flush32()
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	}

	// Flush final states. Written D→C→B→A so decoder reads A first (ANS reversal).
	s.bw.flush32()
	sD.bw.addBits32NC(sD.state, s.actualTableLog)
	s.bw.flush32()
	sC.bw.addBits32NC(sC.state, s.actualTableLog)
	s.bw.flush32()
	sB.bw.addBits32NC(sB.state, s.actualTableLog)
	s.bw.flush32()
	sA.bw.addBits32NC(sA.state, s.actualTableLog)
	return s.bw.close()
}

// decompress4State decodes a four-state FSE bitstream into s.OutU16.
// count is the exact number of symbols to decode (stored in stream header).
func (s *ScratchU16) decompress4State(count int) error {
	br := &s.bits
	if err := br.init(s.brForDecomp.unread()); err != nil {
		return err
	}

	// Read initial states: A first (last written by encoder = top of reversed stream).
	// fillFast() calls guard against bit-buffer exhaustion: 4 × tableLog bits may
	// exceed the 64-bit initial load. For tableLog=15: 4×15=60 + sentinel padding
	// can reach 68 bits. fillFast() is a no-op when bitsRead < 32, so cheap.
	var sA, sB, sC, sD decoderU16
	sA.init(br, s.decTable, s.actualTableLog)
	sB.init(br, s.decTable, s.actualTableLog)
	br.fill()
	sC.init(br, s.decTable, s.actualTableLog)
	br.fill()
	sD.init(br, s.decTable, s.actualTableLog)

	var tmp = s.ct.tableSymbol[:65536]
	var off uint16
	dt := s.decTable
	remaining := count

	// Native (assembly) fast path: process bulk symbols without bounds checks.
	// Only valid for non-zeroBits (all nbBits > 0). The assembly kernel owns its
	// own fill/refill logic and writes directly into tmp[off..].
	if !s.zeroBits && remaining >= 4 && br.off >= 8 && len(s.decTable) > 0 {
		// Limit to available tmp space to avoid wrap during native call.
		bufAvail := int(^uint16(0)-off) + 1 // = 65536 - int(off)
		canDo := remaining
		if canDo > bufAvail {
			canDo = bufAvail
		}
		canDo &^= 3 // round down to multiple of 4
		if canDo >= 4 {
			states := [4]uint32{sA.state, sB.state, sC.state, sD.state}
			n := fse4StateDecompNative(
				unsafe.Pointer(&s.decTable[0]),
				unsafe.Pointer(br),
				unsafe.Pointer(&states[0]),
				unsafe.Pointer(&tmp[off]),
				canDo,
			)
			sA.state = states[0]
			sB.state = states[1]
			sC.state = states[2]
			sD.state = states[3]
			off += uint16(n)
			remaining -= n
			if off == 0 && n > 0 {
				s.OutU16 = append(s.OutU16, tmp...)
				if len(s.OutU16) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	}

	if !s.zeroBits {
		for br.off >= 8 && remaining >= 4 {
			// Two fills cover up to 2×32 = 64 bits consumed per iteration.
			// Lookups for A and B are independent — OoO/AVX2 can issue them in parallel.
			br.fillFast()

			nA := dt[sA.state]
			nB := dt[sB.state]
			lowA := br.getBitsFast32(nA.nbBits)
			lowB := br.getBitsFast32(nB.nbBits)
			sA.state = nA.newState + lowA
			sB.state = nB.newState + lowB

			br.fillFast()

			// Lookups for C and D are also independent of each other.
			nC := dt[sC.state]
			nD := dt[sD.state]
			lowC := br.getBitsFast32(nC.nbBits)
			lowD := br.getBitsFast32(nD.nbBits)
			sC.state = nC.newState + lowC
			sD.state = nD.newState + lowD

			tmp[off+0] = nA.symbol
			tmp[off+1] = nB.symbol
			tmp[off+2] = nC.symbol
			tmp[off+3] = nD.symbol
			off += 4
			remaining -= 4

			if off == 0 {
				s.OutU16 = append(s.OutU16, tmp...)
				if len(s.OutU16) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	} else {
		for br.off >= 8 && remaining >= 4 {
			br.fillFast()

			nA := &dt[sA.state]
			nB := &dt[sB.state]
			lowA := br.getBits32(nA.nbBits)
			lowB := br.getBits32(nB.nbBits)
			sA.state = nA.newState + lowA
			sB.state = nB.newState + lowB

			br.fillFast()

			nC := &dt[sC.state]
			nD := &dt[sD.state]
			lowC := br.getBits32(nC.nbBits)
			lowD := br.getBits32(nD.nbBits)
			sC.state = nC.newState + lowC
			sD.state = nD.newState + lowD

			tmp[off+0] = nA.symbol
			tmp[off+1] = nB.symbol
			tmp[off+2] = nC.symbol
			tmp[off+3] = nD.symbol
			off += 4
			remaining -= 4

			if off == 0 {
				s.OutU16 = append(s.OutU16, tmp...)
				if len(s.OutU16) >= s.DecompressLimit {
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	}
	s.OutU16 = append(s.OutU16, tmp[:off]...)

	// Tail: drain remaining symbols in A, B, C, D order.
	for remaining > 0 {
		br.fill()
		s.OutU16 = append(s.OutU16, sA.next())
		remaining--
		if remaining == 0 {
			break
		}
		br.fill()
		s.OutU16 = append(s.OutU16, sB.next())
		remaining--
		if remaining == 0 {
			break
		}
		br.fill()
		s.OutU16 = append(s.OutU16, sC.next())
		remaining--
		if remaining == 0 {
			break
		}
		br.fill()
		s.OutU16 = append(s.OutU16, sD.next())
		remaining--
	}

	return br.close()
}
