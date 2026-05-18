// Copyright 2026 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// Magic for 8-state FSE — chosen to avoid clash with the rANS 8-state
	// stream which already owns [0xFF, 0x08].
	eightStateFSEMagic0 = 0xFF
	eightStateFSEMagic1 = 0x84
)

// FSECompressU16EightState compresses in[] using eight independent FSE state
// machines, interleaved across the symbol stream by position mod 8.
//
// Going from four to eight states exposes more instruction-level parallelism:
// the per-state dependency chain (state -> dt[state] -> state') is unchanged,
// but eight independent chains let a wide out-of-order core or a SIMD gather
// kernel process eight lookups per cycle. The pure-Go reference here is the
// scalar fallback for any platform; a future AVX-512 kernel can dispatch
// against the same compressed-stream format.
//
// Output format: [0xFF][0x84][count uint32 LE][FSE header][bitstream]
func FSECompressU16EightState(in []uint16, s *ScratchU16) ([]byte, error) {
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
	if err = s.buildCTable(); err != nil {
		return nil, err
	}
	if err = s.compress8State(in); err != nil {
		return nil, err
	}
	s.Out = s.bw.out

	if len(s.Out) >= len(in)*2 {
		return nil, ErrIncompressible
	}

	hdr := make([]byte, 6)
	hdr[0] = eightStateFSEMagic0
	hdr[1] = eightStateFSEMagic1
	binary.LittleEndian.PutUint32(hdr[2:], uint32(len(in)))
	out := append(hdr, s.Out...)
	return out, nil
}

// FSEDecompressU16EightState decompresses data produced by
// FSECompressU16EightState.
func FSEDecompressU16EightState(b []byte, s *ScratchU16) ([]uint16, error) {
	if len(b) < 6 || b[0] != eightStateFSEMagic0 || b[1] != eightStateFSEMagic1 {
		return nil, errors.New("fse8state: missing magic bytes")
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
	if err = s.decompress8State(count); err != nil {
		return nil, err
	}
	return s.OutU16, nil
}

// compress8State encodes src using eight independent FSE states into s.bw.
// Symbols at positions i%8 == 0..7 are handled by states A..H respectively.
// Encoding proceeds backwards (last symbol first) so the decoder reads forward.
//
// Bit-budget: each FSE encode writes up to actualTableLog bits (<=14 in
// practice). Two encodes per flush32 gives a worst case of 2*14 = 28 bits
// before the buffer can reach 64, leaving comfortable headroom. The main
// loop therefore does four flush32 calls per 8 symbols.
func (s *ScratchU16) compress8State(src []uint16) error {
	if len(src) <= 7 {
		return errors.New("compress8State: src too small")
	}
	tt := s.ct.symbolTT[:len(s.ct.symbolTT)]
	s.bw.reset(s.Out)

	var sA, sB, sC, sD, sE, sF, sG, sH cStateU16
	sA.init(&s.bw, &s.ct, s.actualTableLog)
	sB.init(&s.bw, &s.ct, s.actualTableLog)
	sC.init(&s.bw, &s.ct, s.actualTableLog)
	sD.init(&s.bw, &s.ct, s.actualTableLog)
	sE.init(&s.bw, &s.ct, s.actualTableLog)
	sF.init(&s.bw, &s.ct, s.actualTableLog)
	sG.init(&s.bw, &s.ct, s.actualTableLog)
	sH.init(&s.bw, &s.ct, s.actualTableLog)

	ip := len(src)

	// Align ip to a multiple of 8 by encoding the tail symbols first.
	// We flush between every pair of encodes once we exceed the safe budget.
	switch ip & 7 {
	case 1:
		sA.encode(tt[src[ip-1]])
		ip--
	case 2:
		sB.encode(tt[src[ip-1]])
		sA.encode(tt[src[ip-2]])
		ip -= 2
	case 3:
		sC.encode(tt[src[ip-1]])
		sB.encode(tt[src[ip-2]])
		s.bw.flush32()
		sA.encode(tt[src[ip-3]])
		ip -= 3
	case 4:
		sD.encode(tt[src[ip-1]])
		sC.encode(tt[src[ip-2]])
		s.bw.flush32()
		sB.encode(tt[src[ip-3]])
		sA.encode(tt[src[ip-4]])
		ip -= 4
	case 5:
		sE.encode(tt[src[ip-1]])
		sD.encode(tt[src[ip-2]])
		s.bw.flush32()
		sC.encode(tt[src[ip-3]])
		sB.encode(tt[src[ip-4]])
		s.bw.flush32()
		sA.encode(tt[src[ip-5]])
		ip -= 5
	case 6:
		sF.encode(tt[src[ip-1]])
		sE.encode(tt[src[ip-2]])
		s.bw.flush32()
		sD.encode(tt[src[ip-3]])
		sC.encode(tt[src[ip-4]])
		s.bw.flush32()
		sB.encode(tt[src[ip-5]])
		sA.encode(tt[src[ip-6]])
		ip -= 6
	case 7:
		sG.encode(tt[src[ip-1]])
		sF.encode(tt[src[ip-2]])
		s.bw.flush32()
		sE.encode(tt[src[ip-3]])
		sD.encode(tt[src[ip-4]])
		s.bw.flush32()
		sC.encode(tt[src[ip-5]])
		sB.encode(tt[src[ip-6]])
		s.bw.flush32()
		sA.encode(tt[src[ip-7]])
		ip -= 7
	}

	// Main loop: 8 symbols per iteration with a flush every 2 encodes.
	// With ip%8==0: ip-1 -> H, ip-2 -> G, ..., ip-8 -> A.
	for ip >= 8 {
		s.bw.flush32()
		sH.encode(tt[src[ip-1]])
		sG.encode(tt[src[ip-2]])
		s.bw.flush32()
		sF.encode(tt[src[ip-3]])
		sE.encode(tt[src[ip-4]])
		s.bw.flush32()
		sD.encode(tt[src[ip-5]])
		sC.encode(tt[src[ip-6]])
		s.bw.flush32()
		sB.encode(tt[src[ip-7]])
		sA.encode(tt[src[ip-8]])
		ip -= 8
	}

	// Flush final states in reverse order so the decoder reads A first.
	// Each addBits32NC writes actualTableLog (<=14) bits; flush between each
	// to be safe regardless of tableLog choice.
	s.bw.flush32()
	s.bw.addBits32NC(sH.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sG.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sF.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sE.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sD.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sC.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sB.state, s.actualTableLog)
	s.bw.flush32()
	s.bw.addBits32NC(sA.state, s.actualTableLog)
	return s.bw.close()
}

// decompress8State decodes an eight-state FSE bitstream into s.OutU16.
// count is the exact number of symbols to decode.
func (s *ScratchU16) decompress8State(count int) error {
	br := &s.bits
	if err := br.init(s.brForDecomp.unread()); err != nil {
		return err
	}

	// Read initial states A..H in order (A was last written by encoder).
	// Each init consumes actualTableLog (<=14) bits from the front of the
	// stream; refill between groups so the 64-bit buffer never empties.
	var sA, sB, sC, sD, sE, sF, sG, sH decoderU16
	sA.init(br, s.decTable, s.actualTableLog)
	sB.init(br, s.decTable, s.actualTableLog)
	br.fill()
	sC.init(br, s.decTable, s.actualTableLog)
	sD.init(br, s.decTable, s.actualTableLog)
	br.fill()
	sE.init(br, s.decTable, s.actualTableLog)
	sF.init(br, s.decTable, s.actualTableLog)
	br.fill()
	sG.init(br, s.decTable, s.actualTableLog)
	sH.init(br, s.decTable, s.actualTableLog)

	tmp := s.ct.tableSymbol[:65536]
	var off uint16
	dt := s.decTable
	remaining := count

	// Main loop: 8 symbols per iteration. Bit-budget mirrors the encoder
	// (a fill between each pair of decodes keeps the 64-bit buffer fed).
	if !s.zeroBits {
		for br.off >= 16 && remaining >= 8 {
			br.fillFast()
			nA := dt[sA.state]
			nB := dt[sB.state]
			lowA := br.getBitsFast32(nA.nbBits)
			lowB := br.getBitsFast32(nB.nbBits)
			sA.state = nA.newState + lowA
			sB.state = nB.newState + lowB

			br.fillFast()
			nC := dt[sC.state]
			nD := dt[sD.state]
			lowC := br.getBitsFast32(nC.nbBits)
			lowD := br.getBitsFast32(nD.nbBits)
			sC.state = nC.newState + lowC
			sD.state = nD.newState + lowD

			br.fillFast()
			nE := dt[sE.state]
			nF := dt[sF.state]
			lowE := br.getBitsFast32(nE.nbBits)
			lowF := br.getBitsFast32(nF.nbBits)
			sE.state = nE.newState + lowE
			sF.state = nF.newState + lowF

			br.fillFast()
			nG := dt[sG.state]
			nH := dt[sH.state]
			lowG := br.getBitsFast32(nG.nbBits)
			lowH := br.getBitsFast32(nH.nbBits)
			sG.state = nG.newState + lowG
			sH.state = nH.newState + lowH

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
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	} else {
		for br.off >= 16 && remaining >= 8 {
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

			br.fillFast()
			nE := &dt[sE.state]
			nF := &dt[sF.state]
			lowE := br.getBits32(nE.nbBits)
			lowF := br.getBits32(nF.nbBits)
			sE.state = nE.newState + lowE
			sF.state = nF.newState + lowF

			br.fillFast()
			nG := &dt[sG.state]
			nH := &dt[sH.state]
			lowG := br.getBits32(nG.nbBits)
			lowH := br.getBits32(nH.nbBits)
			sG.state = nG.newState + lowG
			sH.state = nH.newState + lowH

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
					return fmt.Errorf("output size (%d) > DecompressLimit (%d)", len(s.OutU16), s.DecompressLimit)
				}
			}
		}
	}
	s.OutU16 = append(s.OutU16, tmp[:off]...)

	// Tail: drain remaining symbols in A, B, C, D, E, F, G, H order.
	decoders := [8]*decoderU16{&sA, &sB, &sC, &sD, &sE, &sF, &sG, &sH}
	for remaining > 0 {
		for _, d := range decoders {
			if remaining == 0 {
				break
			}
			br.fill()
			s.OutU16 = append(s.OutU16, d.next())
			remaining--
		}
	}

	return br.close()
}
