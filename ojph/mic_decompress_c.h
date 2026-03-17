// mic_decompress_c.h — C implementation of MIC decompression pipeline.
// FSE two-state decode → RLE decode → Delta decode, all in one pass.

#ifndef MIC_DECOMPRESS_C_H
#define MIC_DECOMPRESS_C_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// mic_decompress_two_state decompresses a MIC two-state FSE compressed stream
// to 16-bit pixels. Input format: [0xFF][0x02][count_u32_le][FSE header][bitstream]
// The FSE output is an RLE+delta encoded stream which is then decoded to pixels.
//
// compressed:     input compressed data
// compressed_len: length of compressed data
// pixels_out:     caller-allocated output buffer (width * height uint16s)
// width, height:  image dimensions
//
// Returns 0 on success, non-zero on error.
int mic_decompress_two_state(const uint8_t *compressed, size_t compressed_len,
                             uint16_t *pixels_out, int width, int height);

// SIMD-optimized version (SSE2/AVX2). Same interface as above.
// Uses two-pass architecture: RLE decode with SIMD fills, then SIMD delta decode.
int mic_decompress_two_state_simd(const uint8_t *compressed, size_t compressed_len,
                                   uint16_t *pixels_out, int width, int height);

// mic_decompress_four_state decompresses a MIC four-state FSE compressed stream.
// Input format: [0xFF][0x04][count_u32_le][FSE header][bitstream]
int mic_decompress_four_state(const uint8_t *compressed, size_t compressed_len,
                              uint16_t *pixels_out, int width, int height);

// SIMD-optimized four-state version (SSE2/AVX2 RLE+delta; scalar FSE).
int mic_decompress_four_state_simd(const uint8_t *compressed, size_t compressed_len,
                                   uint16_t *pixels_out, int width, int height);

#ifdef __cplusplus
}
#endif

#endif // MIC_DECOMPRESS_C_H
