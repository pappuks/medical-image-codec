package mic

import "math/bits"

type DeltaRleZZU16 struct {
	pixelDepth           uint8
	upperThreshold       uint16
	delimiterForOverflow uint16
	Out                  []uint16
	decomp               RleDecompressU16
	comp                 RleCompressU16
	prevVal              uint16
}

func (c *DeltaRleZZU16) Compress(in []uint16, width int, height int, maxValue uint16) ([]uint16, error) {
	c.pixelDepth = uint8(bits.Len16(maxValue))
	c.upperThreshold = (uint16)((1 << (c.pixelDepth - 1)) - 1)
	c.delimiterForOverflow = (uint16)((1 << (c.pixelDepth)) - 1)
	c.comp.Init(width, height, c.delimiterForOverflow)
	c.comp.Encode(maxValue)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := (y * width) + x
			prevSymbol := int32(0)
			if x > 0 {
				prevSymbol = int32(in[index-1])
			}

			inputVal := in[index]

			diff := int32(int32(inputVal) - prevSymbol)

			if uint16(abs(diff)) >= c.upperThreshold { // We have to ensure that diff + deltaThreshold is not equal to delimiter.
				c.comp.Encode(c.delimiterForOverflow)
				c.comp.Encode(inputVal)
			} else {
				c.comp.Encode(ZigZag(int16(diff)))
			}

		}
	}

	c.comp.Flush()

	return c.comp.Out[:], nil
}

func (d *DeltaRleZZU16) Decompress(in []uint16, width int, height int) []uint16 {
	d.decomp.Init(in)
	maxValue := d.decomp.DecodeNext2()
	d.Out = make([]uint16, width*height)
	d.pixelDepth = uint8(bits.Len16(maxValue))
	d.upperThreshold = (uint16)((1 << (d.pixelDepth - 1)) - 1)
	d.delimiterForOverflow = (uint16)((1 << (d.pixelDepth)) - 1)

	for y := 0; y < height; y++ {
		d.DecodeNextSymbol(0, y, width, height)
		for x := 1; x < width; x++ {
			d.DecodeNextSymbolNC(x, y, width, height)
		}
	}

	return d.Out
}

func (d *DeltaRleZZU16) DecodeNextSymbolNC(x int, y int, width int, height int) {
	index := (y * width) + x
	inputVal := d.decomp.DecodeNext2()
	if inputVal == d.delimiterForOverflow {
		d.prevVal = d.decomp.DecodeNext2()
		d.Out[index] = d.prevVal
	} else {
		diff := int32(UnZigZag(inputVal))
		d.prevVal = uint16(int32(d.prevVal) + diff)
		d.Out[index] = d.prevVal
	}
}

func (d *DeltaRleZZU16) DecodeNextSymbol(x int, y int, width int, height int) {
	index := (y * width) + x
	inputVal := d.decomp.DecodeNext2()
	if inputVal == d.delimiterForOverflow {
		d.prevVal = d.decomp.DecodeNext2()
		d.Out[index] = d.prevVal
	} else {
		diff := int32(UnZigZag(inputVal))
		prevSymbol := int32(0)

		if x > 0 {
			prevSymbol = int32(d.prevVal)
		}

		d.prevVal = uint16(prevSymbol + diff)
		d.Out[index] = d.prevVal
	}
}
