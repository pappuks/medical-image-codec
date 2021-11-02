// Copyright 2021 Kuldeep Singh

package mic

import (
	"math/bits"
)

func DeltaCompressU16(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := (uint16)((1 << (pixelDepth - 1)) - 1)   // For 16 bits this will be 0x7FFF. We have to ensure that 2 * deltaThreshold is less than delimiter
	delimiterForOverflow := (uint16)((1 << (pixelDepth)) - 1) // For 16 bits this will be 0xFFFF
	out := make([]uint16, 0, width*height*2)
	out = append(out, maxValue)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x

			divVal := 0
			prevSymbol := int32(0)
			if x > 0 {
				prevSymbol = int32(in[index-1])
				divVal += 1
			}
			if y > 0 {
				prevSymbol += int32(in[index-width])
				divVal += 1
			}

			if divVal == 2 {
				prevSymbol = prevSymbol >> 1
			}

			inputVal := in[index]

			diff := int32(int32(inputVal) - prevSymbol)

			if uint16(abs(diff)) >= deltaThreshold { // We have to ensure that diff + deltaThreshold is not equal to delimiter.
				out = append(out, delimiterForOverflow)
				out = append(out, inputVal)
			} else {
				out = append(out, uint16(int32(deltaThreshold)+diff))
			}

		}
	}

	return out, nil
}

func DeltaDecompressU16(in []uint16, width int, height int) []uint16 {
	maxValue := in[0]
	out := make([]uint16, width*height)
	pixelDepth := bits.Len16(maxValue)
	deltaThreshold := (uint16)((1 << (pixelDepth - 1)) - 1)   // For 16 bits this will be 0x7FFF. We have to ensure that 2 * deltaThreshold is less than delimiter
	delimiterForOverflow := (uint16)((1 << (pixelDepth)) - 1) // For 16 bits this will be 0xFFFF
	inputCounter := int32(1)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x
			inputVal := in[inputCounter]
			inputCounter++
			if inputVal == delimiterForOverflow {
				out[index] = in[inputCounter]
				inputCounter++
			} else {
				diff := int32(inputVal) - int32(deltaThreshold) // DeltaThreshhold is already ushort

				divVal := 0
				prevSymbol := int32(0)

				if x > 0 {
					prevSymbol = int32(out[index-1])
					divVal++
				}
				if y > 0 {
					prevSymbol += int32(out[index-width])
					divVal++
				}

				if divVal == 2 {
					prevSymbol = prevSymbol >> 1
				}

				out[index] = uint16(prevSymbol + diff)
			}
		}
	}

	return out
}

func abs(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
