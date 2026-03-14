// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

// Package mic – rANS (range Asymmetric Numeral Systems) support.
//
// rANS overview
// =============
// rANS is a variant of ANS where the state update is a pure arithmetic
// operation instead of a table lookup as in tANS/FSE:
//
//   decode:  slot    = state                          (state ∈ [0, L))
//            xNext   = freq[slot] + (slot - bias[slot])
//            state'  = decTable[slot].newState + readBits(decTable[slot].nbBits)
//
//   encode:  xL      = state + L
//            k       = k0 if xL >= threshold else k0-1
//            writeBits(xL, k)
//            state'  = bias + (xL >> k) - freq
//
// where L = 1 << tableLog (the table size), and all quantities are derived
// from the normalized frequency table shared with the existing FSE pipeline.
//
// Bitstream compatibility
// =======================
// rANS uses *identical* decode-table entries (decSymbolU16) and bit-reader/
// writer conventions as tANS.  The only observable differences are:
//   1. How the decode table is built (linear sequential fill vs. spread).
//   2. How the encoder maps (state, symbol) → new_state (arithmetic vs.
//      stateTable lookup).
//   3. The magic-byte prefix: [0xFF, 0x08] for 8-state interleaved rANS.
//
// SIMD note
// =========
// Because the decode step is arithmetically identical to the tANS step
// (table lookup + bit read), the 8-state interleaved decoder inherits the
// same ILP/SIMD kernel structure as the 4-state tANS kernel.  On the encode
// side, the arithmetic state update (no stateTable pointer chase) is more
// amenable to SIMD vectorisation and pipelining.

package mic

import (
	"errors"
	"fmt"
)

// ransEncSymbolU16 holds the per-symbol data needed for rANS encoding.
//
// Encode step (state x ∈ [0, L), symbol s):
//
//	xL := x + L                          // shift into [L, 2L)
//	k  := k0 ; if xL < threshold { k-- } // determine flush count
//	bw.addBits32NC(xL, k)                // write low-k bits
//	x'  = bias + (xL>>k) - freq          // arithmetic new state ∈ [0, L)
type ransEncSymbolU16 struct {
	freq      uint32 // normalized frequency of the symbol
	bias      uint32 // cumulative frequency before the symbol (cumul[s])
	k0        uint8  // base flush count = tableLog - highBits(freq)
	threshold uint32 // freq << k0; if xL >= threshold use k0, else k0-1
}

// buildRansDecTable builds a rANS decode table into s.decTable.
//
// The table is indexed by the current state (which equals the slot in [0, L)).
// Each slot i belongs to the unique symbol s such that cumul[s] ≤ i < cumul[s]+freq[s].
//
// The decode entry for slot i is:
//
//	xNext    = freq[s] + (i − cumul[s])         ∈ [freq[s], 2·freq[s])
//	nbBits   = tableLog − highBits(xNext)
//	newState = (xNext << nbBits) − tableSize     ∈ [0, tableSize)
//
// After reading nbBits from the bitstream the decoder sets:
//
//	state = newState + readBits
func (s *ScratchU16) buildRansDecTable() error {
	tableSize := uint32(1 << s.actualTableLog)
	s.allocDtable()

	s.zeroBits = false
	largeLimit := int32(1 << (s.actualTableLog - 1))

	slot := uint32(0)

	// First pass: fill entries for all symbols with norm > 0.
	for sym := uint32(0); sym < s.symbolLen; sym++ {
		v := s.norm[sym]
		if v <= 0 {
			continue
		}
		if v >= largeLimit {
			s.zeroBits = true
		}
		freq := uint32(v)
		for j := uint32(0); j < freq; j++ {
			xNext := freq + j // ∈ [freq, 2*freq)
			nbBits := uint8(s.actualTableLog) - uint8(highBits(xNext))
			newStateBase := (xNext << nbBits) - tableSize
			if newStateBase >= tableSize {
				return fmt.Errorf("ransDecTable: slot %d newStateBase %d >= tableSize %d",
					slot, newStateBase, tableSize)
			}
			s.decTable[slot] = decSymbolU16{
				newState: newStateBase,
				symbol:   uint16(sym),
				nbBits:   nbBits,
			}
			slot++
		}
	}

	// Second pass: handle low-probability symbols (norm == -1).
	// These are treated as freq=1 and occupy a single slot each.
	for sym := uint32(0); sym < s.symbolLen; sym++ {
		if s.norm[sym] != -1 {
			continue
		}
		if slot >= tableSize {
			return errors.New("ransDecTable: too many low-prob symbols")
		}
		// freq=1 → xNext=1, nbBits=tableLog, newStateBase=0
		s.decTable[slot] = decSymbolU16{
			newState: 0,
			symbol:   uint16(sym),
			nbBits:   s.actualTableLog,
		}
		slot++
	}

	if slot != tableSize {
		return fmt.Errorf("ransDecTable: filled %d of %d slots", slot, tableSize)
	}
	return nil
}

// buildRansEncTable builds per-symbol rANS encode tables from s.norm[].
// Returns a slice indexed by symbol value (length = s.symbolLen).
func (s *ScratchU16) buildRansEncTable() ([]ransEncSymbolU16, error) {
	tt := make([]ransEncSymbolU16, s.symbolLen)
	cumul := uint32(0)

	// Normal symbols (norm > 0).
	for sym := uint32(0); sym < s.symbolLen; sym++ {
		v := s.norm[sym]
		if v <= 0 {
			continue
		}
		freq := uint32(v)
		k0 := uint8(s.actualTableLog) - uint8(highBits(freq))
		tt[sym] = ransEncSymbolU16{
			freq:      freq,
			bias:      cumul,
			k0:        k0,
			threshold: freq << k0,
		}
		cumul += freq
	}

	// Low-probability symbols (norm == -1 → treated as freq=1).
	for sym := uint32(0); sym < s.symbolLen; sym++ {
		if s.norm[sym] != -1 {
			continue
		}
		k0 := s.actualTableLog // highBits(1) = 0 → k0 = tableLog
		tt[sym] = ransEncSymbolU16{
			freq:      1,
			bias:      cumul,
			k0:        k0,
			threshold: 1 << k0,
		}
		cumul++
	}

	if cumul != uint32(1<<s.actualTableLog) {
		return nil, fmt.Errorf("buildRansEncTable: cumul %d != tableSize %d",
			cumul, 1<<s.actualTableLog)
	}
	return tt, nil
}

// ransEncodeStep encodes one symbol into the encoder state.
// x is the current encoder state ∈ [0, L), sym contains per-symbol parameters.
// Returns the new encoder state and the flush count k (for debugging).
//
//go:nosplit
func ransEncodeStep(x uint32, sym ransEncSymbolU16, tableSize uint32, bw *bitWriter) uint32 {
	xL := x + tableSize // ∈ [L, 2L)
	k := sym.k0
	if xL < sym.threshold {
		k--
	}
	// Write the low-k bits of xL to the bitstream.
	bw.addBits32NC(xL, k)
	// Arithmetic state update: new encoder state ∈ [0, L).
	return sym.bias + (xL>>k - sym.freq)
}
