package mic

import "math/bits"

type RleCompressU16 struct {
	Out      []uint16
	b        []uint16
	midCount uint16
	same     bool
}

func (r *RleCompressU16) Init(width int, height int, maxValue uint16) {
	pixelDepth := bits.Len16(maxValue)
	r.midCount = uint16((1 << (pixelDepth - 1)) - 1)
	r.Out = make([]uint16, 0, width*height)
	r.b = make([]uint16, 0, r.midCount+1)
	r.same = false
	r.Out = append(r.Out, maxValue)
}

func (r *RleCompressU16) Encode(symbol uint16) {
	bc := len(r.b)
	if bc < 2 {
		r.b = append(r.b, symbol)
		return
	}
	prevPlusOne := r.b[bc-2]
	prev := r.b[bc-1]

	if (prevPlusOne == prev) && (prev == symbol) {
		if !r.same && bc > 2 {
			// Flush, as the previous symbols were not similar
			r.Out = append(r.Out, r.midCount+uint16(bc-2))
			r.Out = append(r.Out, r.b[:bc-2]...)
			// Remove elements which we have already written
			r.b = r.b[bc-2:]
		}
		// Switch to same mode
		r.same = true
	} else {
		if r.same && bc > 2 {
			// We switched from same to diff, so flush all symbols from the buffer
			r.Out = append(r.Out, uint16(bc))
			r.Out = append(r.Out, r.b[0])
			// Remove elements which we have already written
			r.b = r.b[:0]
		}
		// Switch to diff mode
		r.same = false
	}

	bc = len(r.b)

	// Check for overflow of count
	if bc >= int(r.midCount-1) {
		if r.same {
			r.Out = append(r.Out, uint16(bc-2))
			r.Out = append(r.Out, r.b[0])
		} else {
			r.Out = append(r.Out, (r.midCount + uint16(bc-2)))
			r.Out = append(r.Out, r.b[:bc-2]...)
		}
		r.b = r.b[bc-2:]
	}
	// Add symbol to buffer
	r.b = append(r.b, symbol)
}

func (r *RleCompressU16) Flush() {
	bc := len(r.b)
	if bc > 0 {
		if r.same {
			r.Out = append(r.Out, uint16(bc))
			r.Out = append(r.Out, r.b[0])
		} else {
			r.Out = append(r.Out, (r.midCount + uint16(bc)))
			r.Out = append(r.Out, r.b...)
		}
	}
}
