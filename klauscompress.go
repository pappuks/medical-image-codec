package mic

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"sync"

	"github.com/klauspost/compress/fse"
)

const printSizes = false

func CompressKlaus(in []uint16, width int, height int) []byte {
	// Process in blocks of max maxSize X maxSize
	const maxSize = 192

	// Add global gap removal.
	const globalGapRemoval = true

	const singleThreaded = printSizes

	const tryWithAndWithoutGapRemoval = true

	results := make([][]byte, ((width+maxSize-1)/maxSize)*((height+maxSize-1)/maxSize))
	idx := 0
	var wg sync.WaitGroup
	var gap []byte
	if globalGapRemoval {
		// Try global gap removal.
		// Disabled for now.
		gap = RemoveGaps(in, width, height, width)
	}
	for y := 0; y < height; y += maxSize {
		endY := y + maxSize
		if endY > height {
			endY = height
		}
		// Forward input to out line...
		in := in[y*width:]
		for x := 0; x < width; x += maxSize {
			endX := x + maxSize
			if endX > width {
				endX = width
			}
			if singleThreaded {
				fmt.Printf("x:%d, y:%d, w:%d, h:%d\n", x, y, endX-x, endY-y)
				results[idx] = compressKlaus(in[x:], endX-x, endY-y, width, !tryWithAndWithoutGapRemoval)
				if tryWithAndWithoutGapRemoval {
					withGap := compressKlaus(in[x:], endX-x, endY-y, width, true)
					if len(withGap) < len(results[idx]) {
						fmt.Printf("Gap removed smaller, %d < %d\n", len(withGap), len(results[idx]))
						results[idx] = withGap
					} else {
						fmt.Printf("Keeping gapped version, %d >= %d\n", len(withGap), len(results[idx]))
					}
				}
			} else {
				wg.Add(1)
				go func(in []uint16, i, width, height, stride int) {
					results[i] = compressKlaus(in, width, height, stride, !tryWithAndWithoutGapRemoval)
					if tryWithAndWithoutGapRemoval {
						withGap := compressKlaus(in, width, height, stride, true)
						if len(withGap) < len(results[i]) {
							results[i] = withGap
						}
					}
					wg.Done()
				}(in[x:], idx, endX-x, endY-y, width)
			}
			idx++
		}
	}
	var combined = make([]byte, 0, len(in))
	if globalGapRemoval {
		var tmp [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(tmp[:], uint64(len(gap)))
		combined = append(combined, tmp[:n]...)
		combined = append(combined, gap...)
	} else {
		combined = append(combined, 0)
	}
	wg.Wait()
	// Combine in order...
	for _, b := range results {
		combined = append(combined, b...)
	}
	return combined
}

func compressKlaus(in []uint16, width, height, stride int, gap bool) []byte {
	var codes []byte
	var codesHistogram [256]int // Used for stats only.
	var extra bitWriter
	var bitMapCompressed []byte
	var fseCompressed []byte
	rleVal := uint16(0)
	rleLen := uint16(0)
	var freq [1 << 16]int
	const minRLEvals = 3  // If we have this many RLE codes, always emit as RLE
	const rleMaxBits = 32 // Maximum extra bits where we may emit literals. Must be <= 32
	const rleLitCost = 0
	const printDebug = printSizes
	const maxFSEsize = 1 << 17 // Probably a reasonable upper limit...
	const fseSizeLimit = maxFSEsize - 2

	var maxValP1 = uint16(0)

	zigZag := func(x int16) uint16 {
		ux := uint16(x) << 1
		if x < 0 {
			ux = ^ux
		}
		if maxValP1 > 1 && ux >= maxValP1 {
			// Use wraparound.
			return (maxValP1 * 2) - ux - 1
		}
		return ux
	}

	// Remove gaps...
	// Adds an FSE compressed bitmap with 0 for gaps with no values and 1 for filled.
	// Once the image has been decompressed this must be applied in reverse to recreate the gaps.
	// First "extra" bit will indicate if a bitmap is present or not.
	if gap && width*height > 256 {
		var bitmap [65536]byte
		max := uint16(0)
		for y := 0; y < height; y++ {
			in := in[y*stride : y*stride+width]
			for off := range in {
				v := in[off]
				bitmap[v] = 1
				if v > max {
					max = v
				}
			}
		}
		gaps := 0
		valLen := int(max) + 1
		for _, f := range bitmap[:valLen] {
			gaps += 1 - int(f)
		}
		if printDebug {
			fmt.Println("max", max, "gaps", gaps, "=", valLen-gaps, "values")
		}
		if (1+int(max))*2 <= math.MaxUint16 {
			// Just to avoid overflow.
			// TODO: Should be stored....
			// maxValP1 = max + 1
		}
		// If one in every 4 or more pixels are gaps.
		// TODO: Could include bitmap size for metric, but best would be try one with gap removal
		// and one without and compare sizes.
		if max > 4 && gaps*8 > int(max) {
			var inToOut [65536]uint16
			out := uint16(0)
			extra.addBits32NC(1, 1)
			var s fse.Scratch
			hist := s.Histogram()
			hist = hist[:256]

			maxValP1 = max - uint16(gaps) + 1
			if printDebug {
				fmt.Println("maxDeltaVal:", maxValP1)
			}

			for i, f := range bitmap[:valLen] {
				hist[f]++
				if f == 1 {
					inToOut[i] = out
					out++
				}
			}

			maxCnt := hist[0]
			if hist[1] > maxCnt {
				maxCnt = hist[1]
			}
			s.HistogramFinished(1, int(maxCnt))
			var err error
			bitMapCompressed, err = fse.Compress(bitmap[:valLen], &s)
			if err != nil {
				// Should not be currently possible...
				// Just abort gap removal if it should become possible in the future.
				panic(err)
			}
			if maxValP1 == 1 {
				// TODO: We know all values are now 0,
				// we can store RLE codes for all pixels and be done.
				// We could even shortcut this and not store any more.
			}
			if printDebug {
				fmt.Println("Adding Bitmap, size:", len(bitMapCompressed), "of", (valLen+7)/8, "bytes")
			}
			for y := 0; y < height; y++ {
				in := in[y*stride : y*stride+width]
				for off := range in {
					in[off] = inToOut[in[off]]
				}
			}
		} else {
			extra.addBits32NC(0, 1)
		}
	} else {
		extra.addBits32NC(0, 1)
	}

	const (
		predNone = iota
		predLeft
		predUp
		predUpLeft
		predUpLeft2
		predMedian

		predLast      // must be after valid predictions.
		predFseOffset = tableSize
	)
	predictNone := func(index int) uint16 {
		return in[index]
	}
	predictLeft := func(index int) uint16 {
		return zigZag(int16(in[index] - in[index-1]))
	}
	predictUp := func(index int) uint16 {
		return zigZag(int16(in[index] - in[index-stride]))
	}
	predictUpLeft := func(index int) uint16 {
		pred := uint16((uint32(in[index-stride]) + uint32(in[index-1]) + 1) / 2)
		return zigZag(int16(in[index] - pred))
	}
	// predictMedian returns median of a, b and a+b-c
	predictMedian := func(index int) uint16 {
		a := in[index-1]
		b := in[index-stride]
		c := a + b - in[index-stride-1]
		pred := c
		if (a > b) != (a > c) {
			pred = a
		} else if (b < a) != (b < c) {
			pred = b
		}

		return zigZag(int16(in[index] - pred))
	}

	predictUpLeft2 := func(index int) uint16 {
		// Decide predictor based on upper left delta to neighbors
		// If zigzag-encoded delta is <= pred2MinDelta we use both.
		const pred2MinDelta = 32

		c := in[index]
		left := in[index-1]
		up := in[index-stride]
		ul := in[index-stride-1]
		leftDelta := zigZag(int16(ul - left)) // Left to pixel above it
		upDelta := zigZag(int16(ul - up))     // Up to pixel left of it.
		// If left had big delta to pixel above, use left pixel for this

		if leftDelta > upDelta {
			if leftDelta-upDelta > pred2MinDelta {
				return zigZag(int16(c - left))
			}
		} else if upDelta-leftDelta > pred2MinDelta {
			return zigZag(int16(c - up))
		}
		// Both use up+left if no significant difference.

		pred := uint16((uint32(up) + uint32(left) + 1) / 2)
		return zigZag(int16(c - pred))
	}

	globalPred := uint8(predUpLeft)
	if true {
		// Check only every subSample pixels in each direction
		const subSample = 2
		predictBits := func(b uint16) int {
			// We don't care for values 0->15, since cost is determined by FSE distribution.
			_, dBits := deltaCode(b)
			return int(dBits)
		}
		var left, up, ul2, ul, med, none int
		for y := 1; y < height; y += subSample {
			idx := y * stride
			for x := 1; x < width; x += subSample {
				none += predictBits(predictNone(idx + x))
				left += predictBits(predictLeft(idx + x))
				up += predictBits(predictUp(idx + x))
				ul2 += predictBits(predictUpLeft2(idx + x))
				ul += predictBits(predictUpLeft(idx + x))
				med += predictBits(predictMedian(idx + x))
				if false && predictLeft(idx+x) > 2400 {
					fmt.Println(x, y, ":", predictLeft(idx+x), "-", in[idx+x-1], in[idx+x], "d:", int16(in[idx+x]-in[idx+x-1]))
				}
			}
		}
		best := ul - ul>>6 // Small bonus for 'ul'.
		if none < best {
			globalPred = predNone
			best = none
		}
		if up < best {
			globalPred = predUp
			best = up
		}
		if left < best {
			globalPred = predLeft
			best = left
		}
		if med < best {
			globalPred = predMedian
			best = med
		}
		if ul2 < best {
			globalPred = predUpLeft2
			best = ul2
		}
		// If we found a better global predictor, write it.
		if globalPred != predUpLeft {
			if printDebug {
				fmt.Println("Switching to global predictor", globalPred, "best:", best*subSample*subSample/8, "bytes <", ul*subSample*subSample/8, "bytes estimate.")
			}
			codes = append(codes, predFseOffset+globalPred)
		} else if printDebug {
			fmt.Println("Default global predictor", globalPred, "is best,", ul*subSample*subSample/8, "bytes estimate.")
		}
	}
	// bitsFromDelta returns the number of extra bits emitted by this delta.
	bitsFromDelta := func(delta uint16) int {
		_, b := deltaCode(delta)
		return int(b)
	}

	var s fse.Scratch
	s.MaxSymbolValue = tableSize + predLast - 1
	s.TableLog = 12
	codesTotalLen := 0

	addCodes := func(codes []byte) []byte {
		// Only store if we have any codes.
		if len(codes) > 0 {
			codesTotalLen += len(codes)
			var tmp [binary.MaxVarintLen64]byte
			for _, v := range codes {
				codesHistogram[v]++
			}
			// TODO: Allow reusing tables.
			// We keep negative size reserved for tableless codes.
			// For now we only compress with tables...
			ccodes, err := fse.Compress(codes, &s)
			if err != nil {
				// Unable to compressed, store length 0 to indicate RLE or Uncompressed...
				n := binary.PutVarint(tmp[:], 0)
				fseCompressed = append(fseCompressed, tmp[:n]...)
				if err == fse.ErrUseRLE {
					// RLE: Store length as negative.
					n := binary.PutVarint(tmp[:], int64(-len(codes)))
					fseCompressed = append(fseCompressed, tmp[:n]...)
					fseCompressed = append(fseCompressed, codes[0])
				} else {
					// Store as uncompressed. Length as positive.
					n := binary.PutVarint(tmp[:], int64(len(codes)))
					fseCompressed = append(fseCompressed, tmp[:n]...)
					fseCompressed = append(fseCompressed, codes...)
				}
			} else {
				// Store length as positive.
				n := binary.PutVarint(tmp[:], int64(len(ccodes)))
				fseCompressed = append(fseCompressed, tmp[:n]...)
				fseCompressed = append(fseCompressed, ccodes...)
			}
		}
		return codes[:0]
	}

	const dynamicPredictors = true
	const dynamicBorder = 1
	currMethod := uint8(predNone)
	for y := 0; y < height; y++ {
		if y > 0 {
			// Reset to up on new line.
			currMethod = predUp
		}
		for x := 0; x < width; x++ {
			if x == 1 {
				if y == 0 {
					// Switch to left on first line.
					currMethod = predLeft
				} else {
					// Default to Up+Left for other lines.
					currMethod = globalPred
				}
			}

			index := (y * stride) + x
			const predictAhead = 64
			const checkEvery = 32
			if dynamicPredictors && y >= dynamicBorder && x%checkEvery == dynamicBorder && width-x > predictAhead-dynamicBorder {
				var left, up, ul2, ul, med, none int
				// Estimate for next predictAhead pixels...
				for i := 0; i < predictAhead; i++ {
					none += bitsFromDelta(predictNone(index + i))
					left += bitsFromDelta(predictLeft(index + i))
					up += bitsFromDelta(predictUp(index + i))
					ul2 += bitsFromDelta(predictUpLeft2(index + i))
					ul += bitsFromDelta(predictUpLeft(index + i))
					med += bitsFromDelta(predictMedian(index + i))
				}
				// Prefer current
				const penalty = 15                  // Ratio to 16 we must save.
				const penaltyOff = predictAhead / 2 // We must save at least 0.5 bit per pixel
				best := 0
				switch currMethod {
				case predLeft:
					left = (left*penalty)/16 - penaltyOff
					best = left
				case predUp:
					up = (up*penalty)/16 - penaltyOff
					best = up
				case predUpLeft2:
					ul2 = (ul2*penalty)/16 - penaltyOff
					best = ul2
				case predUpLeft:
					ul = (ul*penalty)/16 - penaltyOff
					best = ul
				case predMedian:
					med = (med*penalty)/16 - penaltyOff
					best = med
				case predNone:
					none = (none*penalty)/16 - penaltyOff
					best = none
				}
				was := currMethod
				if ul < best {
					currMethod = predUpLeft
					best = ul
				}
				if up < best {
					currMethod = predUp
					best = up
				}

				if left < best {
					currMethod = predLeft
					best = left
				}
				if med < best {
					currMethod = predMedian
					best = med
				}
				if ul2 < best {
					currMethod = predUpLeft2
					best = ul2
				}
				if none < best {
					currMethod = predNone
					best = none
				}

				if was != currMethod {
					codes = append(codes, predFseOffset+currMethod)
				}
			}
			if len(codes) >= fseSizeLimit {
				codes = addCodes(codes)
			}

			val := uint16(0)
			switch currMethod {
			case predNone:
				val = predictNone(index)
			case predLeft:
				val = predictLeft(index)
			case predUp:
				val = predictUp(index)
			case predUpLeft2:
				val = predictUpLeft2(index)
			case predUpLeft:
				val = predictUpLeft(index)
			case predMedian:
				val = predictMedian(index)
			default:
				panic(fmt.Sprintf("unknown prediction:%d", currMethod))
			}

			freq[val]++
			if val == rleVal {
				if rleLen == 256 {
					code, _ := rleCode(256)
					codes = append(codes, code)
					rleLen = 0
				}
				rleLen++
				continue
			}
			if rleLen > 0 {
				dc, dBits := deltaCode(rleVal)
				if rleLen >= minRLEvals || uint16(dBits+rleLitCost)*rleLen > rleMaxBits {
					rc, rBits := rleCode(rleLen)
					codes = append(codes, rc)
					if rBits > 0 {
						rleLen -= llOffsetsTable[rc]
						extra.flush32()
						extra.addBits16Clean(rleLen, rBits)
					}
				} else {
					for i := 0; i < int(rleLen); i++ {
						codes = append(codes, dc)
						if dBits > 0 {
							// rleMaxBits must be <= 32
							extra.flush32()
							extra.addBits16Clean(rleVal-llOffsetsTable[dc], dBits)
						}
					}
				}
				rleLen = 0
			}
			rleVal = val

			code, b := deltaCode(val)
			codes = append(codes, code)
			if b > 0 {
				val -= llOffsetsTable[code]
				extra.flush32()
				extra.addBits32NC(uint32(rleLen), b)
			}
		}
	}
	if rleLen > 0 {
		deltaCode, dBits := deltaCode(rleVal)
		rleCode, rBits := rleCode(rleLen)
		if rleLen >= minRLEvals || uint16(dBits+rleLitCost)*rleLen > rleMaxBits {
			codes = append(codes, rleCode)
			if rBits > 0 {
				rleLen -= llOffsetsTable[rleCode]
				extra.flush32()
				extra.addBits32NC(uint32(rleLen), rBits)
			}
		} else {
			for i := 0; i < int(rleLen); i++ {
				codes = append(codes, deltaCode)
				if dBits > 0 {
					extra.flush32()
					extra.addBits16Clean(rleVal-llOffsetsTable[deltaCode], dBits)
				}
			}
		}
		rleLen = 0
	}

	if false {
		max := 0
		for i, v := range freq[:] {
			if v > 0 {
				max = i + 1
			}
		}
		total := 0
		for i, v := range freq[:max] {
			total += v
			fmt.Printf("%d: %d (%.2f)\n", i, v, 100*float64(total)/float64(width*height))
		}
	}

	codes = addCodes(codes)

	if printDebug {
		fmt.Println("codes:", codesHistogram[:maxLLCode])
		fmt.Println("rle:", codesHistogram[maxLLCode:tableSize])
		fmt.Println("pred:", codesHistogram[tableSize:tableSize+predLast])
		fmt.Printf("code size: %d, %.2f bits/code, remainder size: %d\n", len(fseCompressed), float64(len(fseCompressed)*8)/float64(codesTotalLen), len(extra.out)+int((extra.nBits+7)/8))
	}
	// Encode codes length
	// Technically this is not needed
	var codesLen [binary.MaxVarintLen64]byte
	nCodes := binary.PutUvarint(codesLen[:], uint64(len(fseCompressed)))

	// Encode bitmap length
	var tmpBM [binary.MaxVarintLen64]byte
	nBM := binary.PutUvarint(tmpBM[:], uint64(len(bitMapCompressed)))

	extra.flushAlign()
	dst := make([]byte, 0, nCodes+len(fseCompressed)+len(extra.out)+nBM+len(bitMapCompressed))
	dst = append(dst, codesLen[:nCodes]...)
	dst = append(dst, fseCompressed...)
	dst = append(dst, extra.out...)
	if len(bitMapCompressed) > 0 {
		dst = append(dst, tmpBM[:nBM]...)
		dst = append(dst, bitMapCompressed...)
	}

	return dst
}

// Up to 6 bits
const (
	maxLLCode = 35
	rleOffset = maxLLCode + 1
	tableSize = 53
)

// llBitsTable translates from ll code to number of bits.
var llBitsTable = [tableSize]byte{
	0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
	1, 1, 1, 1, 2, 2, 3, 3,
	4, 6, 7, 8, 9, 10, 11, 12,
	13, 14, 15,
	// RLE codes:
	0,                      // 256
	0, 0, 0, 0, 0, 0, 0, 0, // 1 -> 8
	1, 1, 2, 3, 3, 4, 4, 6, 7, // 9 -> 256
}

var llOffsetsTable = [tableSize]uint16{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	16, 18, 20, 22, 24, 28, 32, 40,
	48, 64, 128, 256, 512, 1024, 2048, 4096,
	8192, 16384, 32768,
	// rle codes:
	256,
	1, 2, 3, 4, 5, 6, 7, 8,
	9, 11, 13, 17, 25, 33, 49, 65, 129,
}

// deltaCode returns the code that represents the literal length requested.
func deltaCode(delta uint16) (code, bits uint8) {
	const llDeltaCode = 19
	if int(delta) < len(llCodeTable) {
		// Compiler insists on bounds check (Go 1.12)
		code = llCodeTable[delta&63]
	} else {
		code = uint8(highBit(delta)) + llDeltaCode
	}
	return code, llBitsTable[code]

}

var llCodeTable = [64]byte{0, 1, 2, 3, 4, 5, 6, 7,
	8, 9, 10, 11, 12, 13, 14, 15,
	16, 16, 17, 17, 18, 18, 19, 19,
	20, 20, 20, 20, 21, 21, 21, 21,
	22, 22, 22, 22, 22, 22, 22, 22,
	23, 23, 23, 23, 23, 23, 23, 23,
	24, 24, 24, 24, 24, 24, 24, 24,
	24, 24, 24, 24, 24, 24, 24, 24}

func highBit(val uint16) (n uint16) {
	return uint16(bits.Len16(val) - 1)
}

/*
| `Symbol`               |     35     |      36-43              |
| ---------------------- | ---------- |------------------------ |
| RLE length             |    256     | `Symbol - 35`  = 1 -> 8 |
| `Number_of_Bits`       |     0      |           0             |

| `Symbol`               |  44  |  45  |  46  |  47  |  48  |  49  |  50  |  51  |  52 |
| ---------------------- | ---- | ---- | ---- | ---- | ---- | ---- | ---- | ---- | --- |
| `Baseline`             |  9   |  11  |  13  |  17  |  25  |  33  |  49  |  65  | 129 |
| `Number_of_Bits`       |  1   |   1  |   2  |  3   |   3  |   4  |   4  |   6  |  7  |

*/

var rleTable = [64]byte{}

func init() {
	for i := range rleTable {
		for j, v := range llOffsetsTable[rleOffset:] {
			bmax := uint16(1)<<llBitsTable[rleOffset+j] - 1
			if (v + bmax) >= uint16(i+1) {
				rleTable[i] = byte(j-1) + rleOffset
				//fmt.Println("i:", i+1, "code:", rleTable[i], "bmax:", bmax, "offset:", v)
				break
			}
		}
	}
}

// rleCode returns the code that represents the rle length requested.
// The number of repeats must be <= 256.
func rleCode(repeats uint16) (code, bits uint8) {
	if repeats == 256 {
		return maxLLCode, 0
	}
	const rleDeltaCode = 51 - 6
	repeats--
	if int(repeats) < len(llCodeTable) {
		code = rleTable[repeats&63]
	} else {
		code = uint8(highBit(repeats)) + rleDeltaCode
		//fmt.Println(repeats, "->", code, "+", llBitsTable[code], "bits")
	}
	return code, llBitsTable[code]
}

func RemoveGaps(in []uint16, width int, height, stride int) []byte {
	var bitmap [65536]byte
	max := uint16(0)
	for y := 0; y < height; y++ {
		in := in[y*stride : y*stride+width]
		for off := range in {
			v := in[off]
			bitmap[v] = 1
			if v > max {
				max = v
			}
		}
	}
	gaps := 0
	valLen := int(max) + 1
	for _, f := range bitmap[:valLen] {
		gaps += 1 - int(f)
	}
	if gaps*4 < int(max) {
		return nil
	}
	if true {
		fmt.Println("RemoveGaps: max", max, "gaps", gaps, "=", max-uint16(gaps), "values")
	}
	if (1+int(max))*2 <= math.MaxUint16 {
		// Just to avoid overflow.
		// TODO: Should be stored....
		// maxValP1 = max + 1
	}

	// If one in every 4 or more pixels are gaps.
	// TODO: Could include bitmap size for metric, but best would be try one with gap removal
	// and one without and compare sizes.
	var inToOut [65536]uint16
	out := uint16(0)
	var s fse.Scratch
	hist := s.Histogram()
	hist = hist[:256]

	for i, f := range bitmap[:valLen] {
		hist[f]++
		if f == 1 {
			inToOut[i] = out
			out++
		}
	}

	maxCnt := hist[0]
	if hist[1] > maxCnt {
		maxCnt = hist[1]
	}
	s.HistogramFinished(1, int(maxCnt))
	bitMapCompressed, err := fse.Compress(bitmap[:valLen], &s)
	if err != nil {
		// Should not be currently possible...
		// Just abort gap removal if it should become possible in the future.
		panic(err)
	}

	for y := 0; y < height; y++ {
		in := in[y*stride : y*stride+width]
		for off := range in {
			in[off] = inToOut[in[off]]
		}
	}
	return bitMapCompressed
}
