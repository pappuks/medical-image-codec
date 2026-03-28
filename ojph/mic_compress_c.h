// mic_compress_c.h — C encoder for MIC (Medical Image Codec).
// Pipeline: Delta encode → RLE encode → FSE (two-state or four-state) encode.
// Output is compatible with mic_decompress_c.c and the Go decompressors.

#ifndef MIC_COMPRESS_C_H
#define MIC_COMPRESS_C_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// mic_compress_four_state compresses 16-bit pixels using:
//   Delta encode → RLE encode → FSE four-state encode.
//
// pixels:   input pixel data (width × height uint16s, row-major)
// width:    image width
// height:   image height
// out:      caller-allocated output buffer
// out_cap:  size of out (must be >= 2 * width * height + 4096)
// out_len:  set to number of bytes written on success
//
// Returns 0 on success, negative on error (including when data is incompressible).
int mic_compress_four_state(const uint16_t *pixels, int width, int height,
                             uint8_t *out, size_t out_cap, size_t *out_len);

// mic_compress_two_state is the same pipeline but uses two-state FSE.
// Output magic byte is 0x02; four-state uses 0x04.
int mic_compress_two_state(const uint16_t *pixels, int width, int height,
                            uint8_t *out, size_t out_cap, size_t *out_len);

#ifdef __cplusplus
}
#endif

#endif // MIC_COMPRESS_C_H
