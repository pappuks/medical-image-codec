// Copyright 2024 Kuldeep Singh
// This source code is licensed under a MIT-style
// license that can be found in the LICENSE file.

package mic

// TemporalDeltaEncode computes inter-frame differences: current[i] - prev[i].
// Signed differences are mapped to uint16 via ZigZag encoding which concentrates
// small differences near zero for efficient entropy coding.
// prev must be nil for frame 0 (returns current unchanged).
func TemporalDeltaEncode(current, prev []uint16) []uint16 {
	if prev == nil {
		out := make([]uint16, len(current))
		copy(out, current)
		return out
	}
	out := make([]uint16, len(current))
	for i := range current {
		diff := int16(int32(current[i]) - int32(prev[i]))
		out[i] = ZigZag(diff)
	}
	return out
}

// TemporalDeltaDecode reverses TemporalDeltaEncode: reconstructs current = prev + residual.
// prev must be nil for frame 0 (returns residual unchanged).
func TemporalDeltaDecode(residual, prev []uint16) []uint16 {
	if prev == nil {
		out := make([]uint16, len(residual))
		copy(out, residual)
		return out
	}
	out := make([]uint16, len(residual))
	for i := range residual {
		diff := int32(UnZigZag(residual[i]))
		out[i] = uint16(int32(prev[i]) + diff)
	}
	return out
}
