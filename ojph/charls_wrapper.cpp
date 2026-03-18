// charls_wrapper.cpp — C wrapper around CharLS C++ API for JPEG-LS compress/decompress.

#include "charls_wrapper.h"
#include <charls/charls.h>
#include <cstring>
#include <vector>

extern "C" {

int charls_compress_u16(const uint16_t *pixels, int width, int height,
                        int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                        size_t *out_len) {
    try {
        charls::jpegls_encoder encoder;

        charls::frame_info info{};
        info.width = static_cast<uint32_t>(width);
        info.height = static_cast<uint32_t>(height);
        info.bits_per_sample = bit_depth;
        info.component_count = 1;

        encoder.frame_info(info);
        encoder.near_lossless(0); // lossless

        encoder.destination(out_buf, out_buf_size);

        size_t bytes_written = encoder.encode(
            pixels,
            static_cast<size_t>(width) * height * sizeof(uint16_t),
            0 /* stride: 0 = auto */
        );

        *out_len = bytes_written;
        return 0;
    } catch (...) {
        return -1;
    }
}

int charls_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                          uint16_t *pixels_out, size_t pixels_buf_size,
                          int width, int height) {
    try {
        charls::jpegls_decoder decoder(compressed, compressed_len, true);

        decoder.decode(pixels_out, pixels_buf_size, 0);
        return 0;
    } catch (...) {
        return -1;
    }
}

} // extern "C"
