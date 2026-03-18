// charls_wrapper.h — C API for CharLS JPEG-LS compress/decompress.
// Used for in-process benchmarking against MIC.

#ifndef CHARLS_WRAPPER_H
#define CHARLS_WRAPPER_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// charls_compress_u16 compresses a 16-bit grayscale image using JPEG-LS lossless.
// Returns 0 on success, non-zero on failure.
int charls_compress_u16(const uint16_t *pixels, int width, int height,
                        int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                        size_t *out_len);

// charls_decompress_u16 decompresses a JPEG-LS lossless stream to 16-bit pixels.
// Returns 0 on success, non-zero on failure.
int charls_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                          uint16_t *pixels_out, size_t pixels_buf_size,
                          int width, int height);

#ifdef __cplusplus
}
#endif

#endif // CHARLS_WRAPPER_H
