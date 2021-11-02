// Copyright 2018 Klaus Post. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// Based on work Copyright (c) 2013, Yann Collet, released under BSD License.

package mic

import (
	"errors"
	"fmt"
)

// Compress the input bytes. Input must be < 2GB.
// Provide a Scratch buffer to avoid memory allocations.
// Note that the output is also kept in the scratch buffer.
// If input is too hard to compress, ErrIncompressible is returned.
// If input is a single byte value repeated ErrUseRLE is returned.
func CompressU16(in []uint16, s *ScratchU16) ([]byte, error) {
	if len(in) <= 1 {
		return nil, ErrIncompressible
	}
	if len(in) > (2<<30)-1 {
		return nil, errors.New("input too big, must be < 2GB")
	}
	s, err := s.prepare(in, nil)
	if err != nil {
		return nil, err
	}

	// Create histogram, if none was provided.
	maxCount := s.maxCount
	if maxCount == 0 {
		maxCount = s.countSimple(in)
	}
	// Reset for next run.
	s.clearCount = true
	s.maxCount = 0
	if maxCount == len(in) {
		// One symbol, use RLE
		return nil, ErrUseRLE
	}
	if maxCount == 1 || maxCount < (len(in)>>15) {
		// Each symbol present maximum once or too well distributed.
		return nil, ErrIncompressible
	}
	s.optimalTableLog()
	err = s.normalizeCount()
	if err != nil {
		return nil, err
	}
	err = s.writeCount()
	if err != nil {
		return nil, err
	}

	if true {
		err = s.validateNorm()
		if err != nil {
			return nil, err
		}
	}

	err = s.buildCTable()
	if err != nil {
		return nil, err
	}
	err = s.compress(in)
	if err != nil {
		return nil, err
	}
	s.Out = s.bw.out
	// Check if we compressed.
	if len(s.Out) >= (len(in) * 2) {
		return nil, ErrIncompressible
	}
	return s.Out, nil
}

// cState contains the compression state of a stream.
type cStateU16 struct {
	bw         *bitWriter
	stateTable []uint32
	state      uint32
}

func (c *cStateU16) init2(bw *bitWriter, ct *cTableU16, tableLog uint8) {
	c.bw = bw
	c.stateTable = ct.stateTable
	c.state = 1 << tableLog
}

// encode the output symbol provided and write it to the bitstream.
func (c *cStateU16) encode(symbolTT symbolTransformU16) {
	nbBitsOut := (uint32(c.state) + symbolTT.deltaNbBits) >> 16
	if nbBitsOut > 16 {
		fmt.Printf("*** nbBitsOut %d\n", nbBitsOut)
	}
	dstState := int32(c.state>>(nbBitsOut&31)) + symbolTT.deltaFindState
	c.bw.addBits32NC(c.state, uint8(nbBitsOut))
	if dstState < 0 {
		fmt.Printf("*** Incorrect state %d BitOut %d Delta %d State %d\n", dstState, nbBitsOut, symbolTT.deltaFindState, c.state)
	}
	c.state = c.stateTable[dstState]
}

// flush will write the tablelog to the output and flush the remaining full bytes.
func (c *cStateU16) flush(tableLog uint8) {
	c.bw.flush32()
	c.bw.addBits32NC(c.state, tableLog)
	c.bw.flush()
}

// compress is the main compression loop that will encode the input from the last byte to the first.
func (s *ScratchU16) compress(src []uint16) error {
	if len(src) <= 2 {
		return errors.New("compress: src too small")
	}
	tt := s.ct.symbolTT[:65536]
	s.bw.reset(s.Out)

	// Our two states each encodes every second byte.
	// Last byte encoded (first byte decoded) will always be encoded by c1.
	var cState cStateU16

	cState.init2(&s.bw, &s.ct, s.actualTableLog)

	// Encode so remaining size is divisible by 4.
	ip := len(src)
	if (ip & 1) == 1 {
		//cState.init(&s.bw, &s.ct, s.actualTableLog, tt[src[ip-1]])
		cState.encode(tt[src[ip-1]])
		ip -= 1
	}
	if (ip & 2) != 0 {
		//cState.init(&s.bw, &s.ct, s.actualTableLog, tt[src[ip-1]])
		cState.encode(tt[src[ip-1]])
		cState.encode(tt[src[ip-2]])
		ip -= 2
	}

	// Main compression loop.
	switch {
	case !s.zeroBits && s.actualTableLog <= 8:
		// We can encode 4 symbols without requiring a flush.
		// We do not need to check if any output is 0 bits.
		for ip >= 4 {
			s.bw.flush32()
			v3, v2, v1, v0 := src[ip-4], src[ip-3], src[ip-2], src[ip-1]
			cState.encode(tt[v0])
			cState.encode(tt[v1])
			cState.encode(tt[v2])
			cState.encode(tt[v3])
			ip -= 4
		}
	case !s.zeroBits:
		// We do not need to check if any output is 0 bits.
		for ip >= 4 {
			s.bw.flush32()
			v3, v2, v1, v0 := src[ip-4], src[ip-3], src[ip-2], src[ip-1]
			cState.encode(tt[v0])
			cState.encode(tt[v1])
			s.bw.flush32()
			cState.encode(tt[v2])
			cState.encode(tt[v3])
			ip -= 4
		}
	case s.actualTableLog <= 8:
		// We can encode 4 symbols without requiring a flush
		for ip >= 4 {
			s.bw.flush32()
			v3, v2, v1, v0 := src[ip-4], src[ip-3], src[ip-2], src[ip-1]
			cState.encode(tt[v0])
			cState.encode(tt[v1])
			cState.encode(tt[v2])
			cState.encode(tt[v3])
			ip -= 4
		}
	default:
		for ip >= 4 {
			s.bw.flush32()
			v3, v2, v1, v0 := src[ip-4], src[ip-3], src[ip-2], src[ip-1]
			cState.encode(tt[v0])
			cState.encode(tt[v1])
			s.bw.flush32()
			cState.encode(tt[v2])
			cState.encode(tt[v3])
			ip -= 4
		}
	}

	// Flush final state.
	// Used to initialize state when decoding.
	cState.flush(s.actualTableLog)

	return s.bw.close()
}

// writeCount will write the normalized histogram count to header.
// This is read back by readNCount.
func (s *ScratchU16) writeCount() error {
	var (
		tableLog  = s.actualTableLog
		tableSize = 1 << tableLog
		previous0 bool
		charnum   uint32

		maxHeaderSize = ((int(s.symbolLen) * int(tableLog)) >> 3) + 3

		// Write Table Size
		bitStream = uint32(tableLog - minTablelog)
		bitCount  = uint(4)
		remaining = int32(tableSize + 1) /* +1 for extra accuracy */
		threshold = int32(tableSize)
		nbBits    = uint(tableLog + 1)
	)
	if cap(s.Out) < maxHeaderSize {
		s.Out = make([]byte, 0, (s.br.remain()*2)+maxHeaderSize)
	}
	outP := uint(0)
	out := s.Out[:maxHeaderSize]

	// stops at 1
	for remaining > 1 {
		if previous0 {
			start := charnum
			for s.norm[charnum] == 0 {
				charnum++
			}
			for charnum >= start+24 {
				start += 24
				bitStream += uint32(0xFFFF) << bitCount
				out[outP] = byte(bitStream)
				out[outP+1] = byte(bitStream >> 8)
				outP += 2
				bitStream >>= 16
			}
			for charnum >= start+3 {
				start += 3
				bitStream += 3 << bitCount
				bitCount += 2
			}
			bitStream += uint32(charnum-start) << bitCount
			bitCount += 2
			if bitCount > 16 {
				out[outP] = byte(bitStream)
				out[outP+1] = byte(bitStream >> 8)
				outP += 2
				bitStream >>= 16
				bitCount -= 16
			}
		}

		count := s.norm[charnum]
		charnum++
		max := (2*threshold - 1) - remaining
		if count < 0 {
			remaining += count
		} else {
			remaining -= count
		}
		count++ // +1 for extra accuracy
		if count >= threshold {
			count += max // [0..max[ [max..threshold[ (...) [threshold+max 2*threshold[
		}
		bitStream += uint32(count) << bitCount
		bitCount += nbBits
		if count < max {
			bitCount--
		}

		previous0 = count == 1
		if remaining < 1 {
			return errors.New("internal error: remaining<1")
		}
		for remaining < threshold {
			nbBits--
			threshold >>= 1
		}

		if bitCount > 16 {
			out[outP] = byte(bitStream)
			out[outP+1] = byte(bitStream >> 8)
			outP += 2
			bitStream >>= 16
			bitCount -= 16
		}
	}

	out[outP] = byte(bitStream)
	out[outP+1] = byte(bitStream >> 8)
	outP += (bitCount + 7) / 8

	if charnum > s.symbolLen {
		return errors.New("internal error: charnum > s.symbolLen")
	}
	s.Out = out[:outP]
	return nil
}

// String prints values as a human readable string.
func (s symbolTransformU16) String() string {
	return fmt.Sprintf("dnbits: %08x, fs:%d", s.deltaNbBits, s.deltaFindState)
}

// allocCtable will allocate tables needed for compression.
// If existing tables a re big enough, they are simply re-used.
func (s *ScratchU16) allocCtable() {
	tableSize := 1 << s.actualTableLog
	// get tableSymbol that is big enough.
	if cap(s.ct.tableSymbol) < tableSize {
		s.ct.tableSymbol = make([]uint16, tableSize)
	}
	s.ct.tableSymbol = s.ct.tableSymbol[:tableSize]

	ctSize := tableSize
	if cap(s.ct.stateTable) < ctSize {
		s.ct.stateTable = make([]uint32, ctSize)
	}
	s.ct.stateTable = s.ct.stateTable[:ctSize]

	if cap(s.ct.symbolTT) < 65536 {
		s.ct.symbolTT = make([]symbolTransformU16, 65536)
	}
	s.ct.symbolTT = s.ct.symbolTT[:65536]
}

// buildCTable will populate the compression table so it is ready to be used.
func (s *ScratchU16) buildCTable() error {
	tableSize := uint32(1 << s.actualTableLog)
	highThreshold := tableSize - 1
	var cumul [maxSymbolValue + 2]int32

	s.allocCtable()
	tableSymbol := s.ct.tableSymbol[:tableSize]
	// symbol start positions
	{
		cumul[0] = 0
		for ui, v := range s.norm[:s.symbolLen-1] {
			u := uint16(ui) // one less than reference
			if v == -1 {
				// Low proba symbol
				cumul[u+1] = cumul[u] + 1
				tableSymbol[highThreshold] = u
				highThreshold--
			} else {
				cumul[u+1] = cumul[u] + int32(v)
			}
		}
		// Encode last symbol separately to avoid overflowing u
		u := int(s.symbolLen - 1)
		v := s.norm[s.symbolLen-1]
		if v == -1 {
			// Low proba symbol
			cumul[u+1] = cumul[u] + 1
			tableSymbol[highThreshold] = uint16(u)
			highThreshold--
		} else {
			cumul[u+1] = cumul[u] + int32(v)
		}
		if uint32(cumul[s.symbolLen]) != tableSize {
			return fmt.Errorf("internal error: expected cumul[s.symbolLen] (%d) == tableSize (%d)", cumul[s.symbolLen], tableSize)
		}
		cumul[s.symbolLen] = int32(tableSize) + 1
	}
	// Spread symbols
	s.zeroBits = false
	{
		step := tableStep(tableSize)
		tableMask := tableSize - 1
		var position uint32
		// if any symbol > largeLimit, we may have 0 bits output.
		largeLimit := int32(1 << (s.actualTableLog - 1))
		for ui, v := range s.norm[:s.symbolLen] {
			symbol := uint16(ui)
			if v > largeLimit {
				s.zeroBits = true
			}
			for nbOccurrences := int32(0); nbOccurrences < v; nbOccurrences++ {
				tableSymbol[position] = symbol
				position = (position + step) & tableMask
				for position > highThreshold {
					position = (position + step) & tableMask
				} /* Low proba area */
			}
		}

		// Check if we have gone through all positions
		if position != 0 {
			return errors.New("position!=0")
		}
	}

	// Build table
	table := s.ct.stateTable
	{
		tsi := int(tableSize)
		for u, v := range tableSymbol {
			// TableU16 : sorted by symbol order; gives next state value
			table[cumul[v]] = uint32(tsi + u)
			cumul[v]++
		}
	}

	// Build Symbol Transformation Table
	{
		total := int32(0)
		symbolTT := s.ct.symbolTT[:s.symbolLen]
		tableLog := s.actualTableLog
		tl := (uint32(tableLog) << 16) - (1 << tableLog)
		for i, v := range s.norm[:s.symbolLen] {
			switch v {
			case 0:
			case -1, 1:
				symbolTT[i].deltaNbBits = tl
				symbolTT[i].deltaFindState = int32(total - 1)
				total++
			default:
				maxBitsOut := uint32(tableLog) - highBits(uint32(v-1))
				minStatePlus := uint32(v) << maxBitsOut
				symbolTT[i].deltaNbBits = (maxBitsOut << 16) - minStatePlus
				symbolTT[i].deltaFindState = int32(total - v)
				total += v
			}
		}
		if total != int32(tableSize) {
			return fmt.Errorf("total mismatch %d (got) != %d (want)", total, tableSize)
		}
	}
	return nil
}

// countSimple will create a simple histogram in s.count.
// Returns the biggest count.
// Does not update s.clearCount.
func (s *ScratchU16) countSimple(in []uint16) (max int) {
	for _, v := range in {
		s.count[v]++
	}
	m := uint32(0)
	for i, v := range s.count[:] {
		if v > m {
			m = v
		}
		if v > 0 {
			s.symbolLen = uint32(i) + 1
		}
	}
	return int(m)
}

// minTableLog provides the minimum logSize to safely represent a distribution.
func (s *ScratchU16) minTableLog() uint8 {
	minBitsSrc := highBits(uint32(s.br.remain()-1)) + 1
	minBitsSymbols := highBits(uint32(s.symbolLen-1)) + 2
	if minBitsSrc < minBitsSymbols {
		return uint8(minBitsSrc)
	}
	return uint8(minBitsSymbols)
}

// optimalTableLog calculates and sets the optimal tableLog in s.actualTableLog
func (s *ScratchU16) optimalTableLog() {
	tableLog := s.TableLog
	minBits := s.minTableLog()
	maxBitsSrc := uint8(highBits(uint32(s.br.remain()-1))) - 2
	if maxBitsSrc < tableLog {
		// Accuracy can be reduced
		tableLog = maxBitsSrc
	}
	if minBits > tableLog {
		tableLog = minBits
	}
	// Need a minimum to safely represent all symbol values
	if tableLog < minTablelog {
		tableLog = minTablelog
	}
	if tableLog > maxTableLog {
		tableLog = maxTableLog
	}
	s.actualTableLog = tableLog
}

var rtbTable = [...]uint32{0, 473195, 504333, 520860, 550000, 700000, 750000, 830000}

// normalizeCount will normalize the count of the symbols so
// the total is equal to the table size.
func (s *ScratchU16) normalizeCount() error {
	var (
		tableLog          = s.actualTableLog
		scale             = 62 - uint64(tableLog)
		step              = (1 << 62) / uint64(s.br.remain())
		vStep             = uint64(1) << (scale - 20)
		stillToDistribute = int32(1 << tableLog)
		largest           int
		largestP          int32
		lowThreshold      = (uint32)(s.br.remain() >> tableLog)
	)

	for i, cnt := range s.count[:s.symbolLen] {
		// already handled
		// if (count[s] == s.length) return 0;   /* rle special case */

		if cnt == 0 {
			s.norm[i] = 0
			continue
		}
		if cnt <= lowThreshold {
			s.norm[i] = -1
			stillToDistribute--
		} else {
			proba := (int32)((uint64(cnt) * step) >> scale)
			if proba < 8 {
				restToBeat := vStep * uint64(rtbTable[proba])
				v := uint64(cnt)*step - (uint64(proba) << scale)
				if v > restToBeat {
					proba++
				}
			}
			if proba > largestP {
				largestP = proba
				largest = i
			}
			s.norm[i] = proba
			stillToDistribute -= proba
		}
	}

	if -stillToDistribute >= (s.norm[largest] >> 1) {
		// corner case, need another normalization method
		return s.normalizeCount2()
	}
	s.norm[largest] += stillToDistribute
	return nil
}

// Secondary normalization method.
// To be used when primary method fails.
func (s *ScratchU16) normalizeCount2() error {
	const notYetAssigned = -2
	var (
		distributed  uint32
		total        = uint32(s.br.remain())
		tableLog     = s.actualTableLog
		lowThreshold = total >> tableLog
		lowOne       = (total * 3) >> (tableLog + 1)
	)
	for i, cnt := range s.count[:s.symbolLen] {
		if cnt == 0 {
			s.norm[i] = 0
			continue
		}
		if cnt <= lowThreshold {
			s.norm[i] = -1
			distributed++
			total -= cnt
			continue
		}
		if cnt <= lowOne {
			s.norm[i] = 1
			distributed++
			total -= cnt
			continue
		}
		s.norm[i] = notYetAssigned
	}
	toDistribute := (1 << tableLog) - distributed

	if (total / toDistribute) > lowOne {
		// risk of rounding to zero
		lowOne = (total * 3) / (toDistribute * 2)
		for i, cnt := range s.count[:s.symbolLen] {
			if (s.norm[i] == notYetAssigned) && (cnt <= lowOne) {
				s.norm[i] = 1
				distributed++
				total -= cnt
				continue
			}
		}
		toDistribute = (1 << tableLog) - distributed
	}
	if distributed == uint32(s.symbolLen)+1 {
		// all values are pretty poor;
		//   probably incompressible data (should have already been detected);
		//   find max, then give all remaining points to max
		var maxV int
		var maxC uint32
		for i, cnt := range s.count[:s.symbolLen] {
			if cnt > maxC {
				maxV = i
				maxC = cnt
			}
		}
		s.norm[maxV] += int32(toDistribute)
		return nil
	}

	if total == 0 {
		// all of the symbols were low enough for the lowOne or lowThreshold
		for i := uint32(0); toDistribute > 0; i = (i + 1) % (uint32(s.symbolLen)) {
			if s.norm[i] > 0 {
				toDistribute--
				s.norm[i]++
			}
		}
		return nil
	}

	var (
		vStepLog = 62 - uint64(tableLog)
		mid      = uint64((1 << (vStepLog - 1)) - 1)
		rStep    = (((1 << vStepLog) * uint64(toDistribute)) + mid) / uint64(total) // scale on remaining
		tmpTotal = mid
	)
	for i, cnt := range s.count[:s.symbolLen] {
		if s.norm[i] == notYetAssigned {
			var (
				end    = tmpTotal + uint64(cnt)*rStep
				sStart = uint32(tmpTotal >> vStepLog)
				sEnd   = uint32(end >> vStepLog)
				weight = sEnd - sStart
			)
			if weight < 1 {
				return errors.New("weight < 1")
			}
			s.norm[i] = int32(weight)
			tmpTotal = end
		}
	}
	return nil
}

// validateNorm validates the normalized histogram table.
func (s *ScratchU16) validateNorm() (err error) {
	var total int
	for _, v := range s.norm[:s.symbolLen] {
		if v >= 0 {
			total += int(v)
		} else {
			total -= int(v)
		}
	}
	defer func() {
		if err == nil {
			return
		}
		fmt.Printf("selected TableLog: %d, Symbol length: %d\n", s.actualTableLog, s.symbolLen)
		for i, v := range s.norm[:s.symbolLen] {
			fmt.Printf("%3d: %5d -> %4d \n", i, s.count[i], v)
		}
	}()
	if total != (1 << s.actualTableLog) {
		return fmt.Errorf("warning: Total == %d != %d", total, 1<<s.actualTableLog)
	}
	for i, v := range s.count[s.symbolLen:] {
		if v != 0 {
			return fmt.Errorf("warning: Found symbol out of range, %d after cut", i)
		}
	}
	return nil
}
