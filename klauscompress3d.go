package mic

import (
	"encoding/binary"
	"fmt"
	"math/bits"

	"github.com/klauspost/compress/fse"
)

func CompressKlaus3D(in, prev []uint16, width int, height int) []byte {
	var codes []byte
	var extra bitWriter
	var bitMapCompressed []byte
	rleVal := uint16(0)
	rleLen := uint16(0)
	var freq [1 << 16]int
	const minRLEvals = 3  // If we have this many RLE codes, always emit as RLE
	const rleMaxBits = 32 // Maximum extra bits where we may emit literals. Must be <= 32
	const rleLitCost = 0
	const printDebug = true

	// Remove gaps...
	// Adds an FSE compressed bitmap with 0 for gaps with no values and 1 for filled.
	// Once the image has been decompressed this must be applied in reverse to recreate the gaps.
	// First "extra" bit will indicate if a bitmap is present or not.
	// Gap removal disabled in 3D mode.
	if false {
		var bitmap [65536]byte
		max := uint16(0)
		for off := range in {
			v := in[off]
			bitmap[v] = 1
			if v > max {
				max = v
			}
		}
		gaps := 0
		valLen := int(max) + 1
		for _, f := range bitmap[:valLen] {
			gaps += 1 - int(f)
		}
		if printDebug {
			fmt.Println("max", max, "gaps", gaps, "=", max-uint16(gaps), "values")
		}
		// If one in every 4 or more pixels are gaps.
		// TODO: Could include bitmap size for metric, but best would be try one with gap removal
		// and one without and compare sizes.
		if gaps*4 > int(max) {
			var inToOut [65536]uint16
			out := uint16(0)
			extra.addBits32NC(1, 1)
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
			bitMapCompressed, _ = fse.Compress(bitmap[:valLen], &s)
			if printDebug {
				fmt.Println("Adding Bitmap, size:", len(bitMapCompressed), "of", (valLen+7)/8, "bytes")
			}
			for off := range in {
				in[off] = inToOut[in[off]]
			}
			//fmt.Println("out:", out)
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
		// 3D
		predBack
		predBackMedian
		predBackAvg

		predLast      // must be after valid predictions.
		predFseOffset = tableSize
	)
	predictNone := func(index int) uint16 {
		return in[index]
	}
	predictBack := func(index int) uint16 {
		// Just use previous...
		return ZigZag(int16(in[index] - prev[index]))
	}
	predictBackAvg := func(index int) uint16 {
		// Average of (up + left + back + back) / 4
		pred := uint16((2*uint32(prev[index]) + uint32(in[index-width]) + uint32(in[index-1]) + 2) / 4)
		return ZigZag(int16(in[index] - pred))
	}
	predictLeft := func(index int) uint16 {
		return ZigZag(int16(in[index] - in[index-1]))
	}
	predictUp := func(index int) uint16 {
		return ZigZag(int16(in[index] - in[index-width]))
	}
	predictUpLeft := func(index int) uint16 {
		pred := uint16((uint32(in[index-width]) + uint32(in[index-1]) + 1) / 2)
		return ZigZag(int16(in[index] - pred))
	}
	predictUpLeft2 := func(index int) uint16 {
		// Decide predictor based on upper left delta to neighbors
		// If zigzag-encoded delta is <= pred2MinDelta we use both.
		const pred2MinDelta = 64

		c := in[index]
		left := in[index-1]
		up := in[index-width]
		ul := in[index-width-1]
		leftDelta := ZigZag(int16(ul - left)) // Left to pixel above it
		upDelta := ZigZag(int16(ul - up))     // Up to pixel left of it.
		// If left had big delta to pixel above, use left pixel for this

		if leftDelta > upDelta {
			if leftDelta-upDelta > pred2MinDelta {
				return ZigZag(int16(c - left))
			}
			return ZigZag(int16(c - left))
		} else if upDelta-leftDelta > pred2MinDelta {
			return ZigZag(int16(c - up))
		}
		// Both use up+left if no significant difference.
		pred := uint16((uint32(in[index-width]) + uint32(in[index-1]) + 1) / 2)
		return ZigZag(int16(in[index] - pred))
	}

	// predictMedian returns median of a, b and a+b-c
	predictMedian := func(index int) uint16 {
		a := in[index-1]
		b := in[index-width]
		c := a + b - in[index-width-1]
		pred := c
		if (a > b) != (a > c) {
			pred = a
		} else if (b < a) != (b < c) {
			pred = b
		}

		return ZigZag(int16(in[index] - pred))
	}
	predictBackMedian := func(index int) uint16 {
		// Median of up, left, back
		a := in[index-1]
		b := in[index-width]
		c := prev[index]
		pred := c
		if (a > b) != (a > c) {
			pred = a
		} else if (b < a) != (b < c) {
			pred = b
		}

		return ZigZag(int16(in[index] - pred))
	}

	globalPred := uint8(predBackMedian)
	if true {
		// Check only every subSample pixels in each direction
		const subSample = 4
		predictBits := func(b uint16) int {
			// We don't care for values 0->15, since cost is determined by FSE distribution.
			l := bits.Len16(b) - 4
			if l < 0 {
				return 0
			}
			return l
		}
		var left, up, ul2, ul, med, none, back, backm, backa int
		for y := 1; y < height; y += subSample {
			idx := y * width
			for x := 1; x < width; x += subSample {
				none += predictBits(predictNone(idx + x))
				left += predictBits(predictLeft(idx + x))
				up += predictBits(predictUp(idx + x))
				ul2 += predictBits(predictUpLeft2(idx + x))
				ul += predictBits(predictUpLeft(idx + x))
				med += predictBits(predictMedian(idx + x))
				back += predictBits(predictBack(idx + x))
				backm += predictBits(predictBackMedian(idx + x))
				backa += predictBits(predictBackAvg(idx + x))
			}
		}
		best := backm - backm>>6 // Small bonus for 'backm'.
		if ul < best {
			globalPred = predUpLeft
			best = ul
		}

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
		if back < best {
			globalPred = predBack
			best = back
		}
		if backm < best {
			globalPred = predBackMedian
			best = backm
		}
		if backa < best {
			globalPred = predBackAvg
			best = backa
		}
		// If we found a better global predictor, write it.
		if globalPred != predBackMedian {
			if printDebug {
				fmt.Println("Switching to global predictor", globalPred, "best:", best, "<", back)
			}
			codes = append(codes, predFseOffset+globalPred)
		}
	}
	// bitsFromDelta returns the number of extra bits emitted by this delta.
	bitsFromDelta := func(delta uint16) int {
		_, b := deltaCode(delta)
		return int(b)
	}

	const dynamicPredictors = true
	const dynamicBorder = 1
	currMethod := uint8(predBack)
	for y := 0; y < height; y++ {
		// Reset to back up on new line.
		currMethod = predBack

		for x := 0; x < width; x++ {
			if x == 1 && y > 0 {
				// Default to for other lines.
				currMethod = globalPred
			}

			index := (y * width) + x
			const predictAhead = 64 // Must be power of 2
			if dynamicPredictors && y >= dynamicBorder && x&(predictAhead-1) == dynamicBorder && width-x > predictAhead-dynamicBorder {
				var left, up, ul2, ul, med, back, backm, backa int
				// Estimate for next predictAhead pixels...
				// We don't bother checking 'none'.
				for i := 0; i < predictAhead; i++ {
					left += bitsFromDelta(predictLeft(index + i))
					up += bitsFromDelta(predictUp(index + i))
					ul2 += bitsFromDelta(predictUpLeft2(index + i))
					ul += bitsFromDelta(predictUpLeft(index + i))
					med += bitsFromDelta(predictMedian(index + i))
					back += bitsFromDelta(predictBack(index + i))
					backm += bitsFromDelta(predictBackMedian(index + i))
					backa += bitsFromDelta(predictBackAvg(index + i))
				}
				// Prefer current
				const penalty = 14                  // Ratio to 16 we must save.
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
				case predBack:
					back = (back*penalty)/16 - penaltyOff
					best = back
				case predBackMedian:
					backm = (backm*penalty)/16 - penaltyOff
					best = backm
				case predBackAvg:
					backa = (backa*penalty)/16 - penaltyOff
					best = backa
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
				if back < best {
					currMethod = predBack
					best = back
				}
				if backm < best {
					currMethod = predBackMedian
					best = backm
				}
				if backa < best {
					currMethod = predBackAvg
					best = backa
				}
				//if none < best {
				//	currMethod = predNone
				//	best = none
				//}

				if was != currMethod {
					codes = append(codes, predFseOffset+currMethod)
				}
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
			case predBack:
				val = predictBack(index)
			case predBackMedian:
				val = predictBackMedian(index)
			case predBackAvg:
				val = predictBackAvg(index)
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
				deltaCode, dBits := deltaCode(rleVal)
				rleCode, rBits := rleCode(rleLen)
				if rleLen >= minRLEvals || uint16(dBits+rleLitCost)*rleLen > rleMaxBits {
					codes = append(codes, rleCode)
					if rBits > 0 {
						rleLen -= llOffsetsTable[rleCode]
						extra.flush32()
						extra.addBits16Clean(rleLen, rBits)
					}
				} else {
					for i := 0; i < int(rleLen); i++ {
						codes = append(codes, deltaCode)
						if dBits > 0 {
							// rleMaxBits must be <= 32
							extra.flush32()
							extra.addBits16Clean(rleVal-llOffsetsTable[deltaCode], dBits)
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
	// TODO: Reuse scratch..
	var s fse.Scratch
	s.MaxSymbolValue = tableSize + predLast - 1
	// TODO : Maybe restrict tablelog for speed
	s.TableLog = 12
	ccodes, err := fse.Compress(codes, &s)
	if err != nil {
		// TODO: Handle rare "RLE"
		panic(err)
	}

	if printDebug {
		fmt.Println("codes:", s.Histogram()[:maxLLCode])
		fmt.Println("rle:", s.Histogram()[maxLLCode:tableSize])
		fmt.Println("pred:", s.Histogram()[tableSize:tableSize+predLast])
	}
	// Encode codes length
	var codesLen [binary.MaxVarintLen64]byte
	nCodes := binary.PutUvarint(codesLen[:], uint64(len(ccodes)))

	// Encode bitmap length
	var tmpBM [binary.MaxVarintLen64]byte
	nBM := binary.PutUvarint(tmpBM[:], uint64(len(bitMapCompressed)))

	extra.flushAlign()
	dst := make([]byte, 0, nCodes+len(ccodes)+len(extra.out)+nBM+len(bitMapCompressed))
	dst = append(dst, codesLen[:nCodes]...)
	dst = append(dst, ccodes...)
	dst = append(dst, extra.out...)
	if len(bitMapCompressed) > 0 {
		dst = append(dst, tmpBM[:nBM]...)
		dst = append(dst, bitMapCompressed...)
	}

	return dst
}
