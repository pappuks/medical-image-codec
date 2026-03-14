// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	twoStateMagic0 = 0xFF
	twoStateMagic1 = 0x02
)

// FSECompressU16TwoState compresses in[] using two independent FSE state machines.
// The two states run in parallel on alternating symbols, breaking serial dependency
// chains so the CPU's out-of-order engine can execute both pipelines concurrently.
// Output format: [0xFF][0x02][count uint32 LE][FSE header][bitstream]
func FSECompressU16TwoState(in []uint16, s *ScratchU16) ([]byte, error) {
	if len(in) <= 1 {
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
	if err = s.compress2State(in); err != nil {
		return nil, err
	}
	s.Out = s.bw.out

	if len(s.Out) >= len(in)*2 {
		return nil, ErrIncompressible
	}

	// Prepend magic + count.
	hdr := make([]byte, 6)
	hdr[0] = twoStateMagic0
	hdr[1] = twoStateMagic1
	binary.LittleEndian.PutUint32(hdr[2:], uint32(len(in)))
	out := append(hdr, s.Out...)
	return out, nil
}

// FSEDecompressU16TwoState decompresses data produced by FSECompressU16TwoState.
func FSEDecompressU16TwoState(b []byte, s *ScratchU16) ([]uint16, error) {
	if len(b) < 6 || b[0] != twoStateMagic0 || b[1] != twoStateMagic1 {
		return nil, errors.New("fse2state: missing magic bytes")
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
	if err = s.decompress2State(count); err != nil {
		return nil, err
	}
	return s.OutU16, nil
}

// FSEDecompressU16Auto auto-detects the stream format based on the magic prefix:
//   [0xFF, 0x08] → eight-state rANS decoder
//   [0xFF, 0x04] → four-state decoder
//   [0xFF, 0x02] → two-state decoder
//   otherwise   → single-state decoder
func FSEDecompressU16Auto(b []byte, s *ScratchU16) ([]uint16, error) {
	if len(b) >= 2 && b[0] == eightStateMagic0 && b[1] == eightStateMagic1 {
		return RANSDecompressU16EightState(b, s)
	}
	if len(b) >= 2 && b[0] == fourStateMagic0 && b[1] == fourStateMagic1 {
		return FSEDecompressU16FourState(b, s)
	}
	if len(b) >= 2 && b[0] == twoStateMagic0 && b[1] == twoStateMagic1 {
		return FSEDecompressU16TwoState(b, s)
	}
	return FSEDecompressU16(b, s)
}

// compress2State encodes src using two independent FSE states into s.bw.
// Symbols at even positions (0, 2, 4, ...) are handled by stateA;
// symbols at odd positions (1, 3, 5, ...) are handled by stateB.
// Encoding proceeds backwards so the decoder can read forward.
func (s *ScratchU16) compress2State(src []uint16) error {
	if len(src) <= 2 {
		return errors.New("compress2State: src too small")
	}
	tt := s.ct.symbolTT[:len(s.ct.symbolTT)]
	s.bw.reset(s.Out)

	var sA, sB cStateU16
	sA.init(&s.bw, &s.ct, s.actualTableLog)
	sB.init(&s.bw, &s.ct, s.actualTableLog)

	ip := len(src)

	// Align so remaining count is divisible by 4.
	// stateA handles even original indices; stateB handles odd.
	if ip&1 == 1 {
		// src[ip-1] has an even 0-based index (since ip-1 is even when ip is odd)
		sA.encode(tt[src[ip-1]])
		ip--
	}
	if ip&2 != 0 {
		// src[ip-1] is at an odd index -> stateB; src[ip-2] at even -> stateA
		sB.encode(tt[src[ip-1]])
		sA.encode(tt[src[ip-2]])
		ip -= 2
	}

	// Main loop: 4 symbols per iteration, matching single-state flush discipline.
	switch {
	case !s.zeroBits && s.actualTableLog <= 8:
		// 4×8 = 32 bits max per iteration, one flush32 is sufficient.
		for ip >= 4 {
			s.bw.flush32()
			sB.encode(tt[src[ip-1]])
			sA.encode(tt[src[ip-2]])
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	case !s.zeroBits:
		// 2×tableLog bits per half — need two flush32 per 4 symbols.
		for ip >= 4 {
			s.bw.flush32()
			sB.encode(tt[src[ip-1]])
			sA.encode(tt[src[ip-2]])
			s.bw.flush32()
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	case s.actualTableLog <= 8:
		for ip >= 4 {
			s.bw.flush32()
			sB.encode(tt[src[ip-1]])
			sA.encode(tt[src[ip-2]])
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	default:
		for ip >= 4 {
			s.bw.flush32()
			sB.encode(tt[src[ip-1]])
			sA.encode(tt[src[ip-2]])
			s.bw.flush32()
			sB.encode(tt[src[ip-3]])
			sA.encode(tt[src[ip-4]])
			ip -= 4
		}
	}

	// Flush both states. Decoder reads stateA first (last written before sentinel).
	s.bw.flush32()
	sB.bw.addBits32NC(sB.state, s.actualTableLog)
	s.bw.flush32()
	sA.bw.addBits32NC(sA.state, s.actualTableLog)
	return s.bw.close()
}

// decompress2State decodes a two-state FSE bitstream into s.OutU16.
// count is the exact number of symbols to decode (stored in stream header).
func (s *ScratchU16) decompress2State(count int) error {
	br := &s.bits
	if err := br.init(s.brForDecomp.unread()); err != nil {
		return err
	}

	// Read initial states: stateA first (last written = at top of reversed stream).
	var sA, sB decoderU16
	sA.init(br, s.decTable, s.actualTableLog)
	sB.init(br, s.decTable, s.actualTableLog)

	var tmp = s.ct.tableSymbol[:65536]
	var off uint16
	dt := s.decTable

	remaining := count

	if !s.zeroBits {
		for br.off >= 8 && remaining >= 4 {
			br.fillFast()

			nA0 := dt[sA.state]
			nB0 := dt[sB.state]
			lowA0 := br.getBitsFast32(nA0.nbBits)
			lowB0 := br.getBitsFast32(nB0.nbBits)
			sA.state = nA0.newState + lowA0
			sB.state = nB0.newState + lowB0

			br.fillFast()

			nA1 := dt[sA.state]
			nB1 := dt[sB.state]
			lowA1 := br.getBitsFast32(nA1.nbBits)
			lowB1 := br.getBitsFast32(nB1.nbBits)
			sA.state = nA1.newState + lowA1
			sB.state = nB1.newState + lowB1

			tmp[off+0] = nA0.symbol
			tmp[off+1] = nB0.symbol
			tmp[off+2] = nA1.symbol
			tmp[off+3] = nB1.symbol
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

			nA0 := &dt[sA.state]
			nB0 := &dt[sB.state]
			lowA0 := br.getBits32(nA0.nbBits)
			lowB0 := br.getBits32(nB0.nbBits)
			sA.state = nA0.newState + lowA0
			sB.state = nB0.newState + lowB0

			br.fillFast()

			nA1 := &dt[sA.state]
			nB1 := &dt[sB.state]
			lowA1 := br.getBits32(nA1.nbBits)
			lowB1 := br.getBits32(nB1.nbBits)
			sA.state = nA1.newState + lowA1
			sB.state = nB1.newState + lowB1

			tmp[off+0] = nA0.symbol
			tmp[off+1] = nB0.symbol
			tmp[off+2] = nA1.symbol
			tmp[off+3] = nB1.symbol
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

	// Tail: drain remaining symbols alternating A then B.
	// We use the exact count to avoid over-reading.
	for remaining > 0 {
		// stateA symbol
		br.fill()
		s.OutU16 = append(s.OutU16, sA.next())
		remaining--
		if remaining == 0 {
			break
		}
		// stateB symbol
		br.fill()
		s.OutU16 = append(s.OutU16, sB.next())
		remaining--
	}

	return br.close()
}
