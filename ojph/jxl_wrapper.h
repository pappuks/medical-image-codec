// jxl_wrapper.h — C API for JPEG XL lossless compress/decompress (libjxl).
// Used for in-process benchmarking against MIC.

#ifndef JXL_WRAPPER_H
#define JXL_WRAPPER_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// jxl_compress_u16 compresses a 16-bit grayscale image with JPEG XL lossless
// (modular mode, distance 0). effort is the libjxl encoder effort (1..9,
// libjxl default is 7). Returns 0 on success, non-zero on failure.
int jxl_compress_u16(const uint16_t *pixels, int width, int height,
                     int bit_depth, int effort,
                     uint8_t *out_buf, size_t out_buf_size, size_t *out_len);

// jxl_decompress_u16 decompresses a JPEG XL stream into 16-bit pixels.
// Returns 0 on success, non-zero on failure.
int jxl_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                       uint16_t *pixels_out, size_t pixels_buf_size,
                       int width, int height);

#ifdef __cplusplus
}
#endif

#endif // JXL_WRAPPER_H
