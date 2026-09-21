// ojph_wrapper.h — C wrapper for OpenJPH lossless compress/decompress.
// Used by CGO to call OpenJPH as an in-process library (no subprocess overhead).

#ifndef OJPH_WRAPPER_H
#define OJPH_WRAPPER_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// ojph_compress_u16 compresses a 16-bit grayscale image using HTJ2K lossless.
// pixels: input pixel buffer (uint16, row-major, native endian)
// width, height: image dimensions
// bit_depth: actual bit depth (e.g., 12 for 12-bit stored in uint16)
// out_buf: caller-allocated output buffer
// out_buf_size: size of out_buf in bytes
// out_len: on success, set to actual compressed size
// Returns 0 on success, non-zero on error.
int ojph_compress_u16(const uint16_t *pixels, int width, int height,
                      int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                      size_t *out_len);

// ojph_decompress_u16 decompresses an HTJ2K lossless codestream to 16-bit pixels.
// compressed: input codestream buffer
// compressed_len: size of codestream in bytes
// pixels_out: caller-allocated output buffer for uint16 pixels
// pixels_buf_size: size of pixels_out in bytes
// width, height: expected image dimensions (for validation)
// Returns 0 on success, non-zero on error.
int ojph_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                        uint16_t *pixels_out, size_t pixels_buf_size,
                        int width, int height);

#ifdef __cplusplus
}
#endif

#endif // OJPH_WRAPPER_H
