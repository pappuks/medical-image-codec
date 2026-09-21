// charls_wrapper.cpp — C wrapper around CharLS C API for JPEG-LS compress/decompress.

#include "charls_wrapper.h"
#include <charls/charls_jpegls_encoder.h>
#include <charls/charls_jpegls_decoder.h>
#include <cstring>

extern "C" {

int charls_compress_u16(const uint16_t *pixels, int width, int height,
                        int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                        size_t *out_len) {
    charls_jpegls_encoder* encoder = charls_jpegls_encoder_create();
    if (!encoder) return -1;

    charls_frame_info info{};
    info.width = static_cast<uint32_t>(width);
    info.height = static_cast<uint32_t>(height);
    info.bits_per_sample = bit_depth;
    info.component_count = 1;

#define CHARLS_OK ((charls_jpegls_errc)0)
    if (charls_jpegls_encoder_set_frame_info(encoder, &info) != CHARLS_OK) {
        charls_jpegls_encoder_destroy(encoder);
        return -1;
    }
    if (charls_jpegls_encoder_set_near_lossless(encoder, 0) != CHARLS_OK) {
        charls_jpegls_encoder_destroy(encoder);
        return -1;
    }
    if (charls_jpegls_encoder_set_destination_buffer(encoder, out_buf, out_buf_size) != CHARLS_OK) {
        charls_jpegls_encoder_destroy(encoder);
        return -1;
    }

    size_t bytes_written = 0;
    if (charls_jpegls_encoder_encode_from_buffer(encoder, pixels,
            static_cast<size_t>(width) * height * sizeof(uint16_t), 0) != CHARLS_OK) {
        charls_jpegls_encoder_destroy(encoder);
        return -1;
    }
    if (charls_jpegls_encoder_get_bytes_written(encoder, &bytes_written) != CHARLS_OK) {
        charls_jpegls_encoder_destroy(encoder);
        return -1;
    }

    *out_len = bytes_written;
    charls_jpegls_encoder_destroy(encoder);
    return 0;
}

int charls_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                          uint16_t *pixels_out, size_t pixels_buf_size,
                          int width, int height) {
    charls_jpegls_decoder* decoder = charls_jpegls_decoder_create();
    if (!decoder) return -1;

    if (charls_jpegls_decoder_set_source_buffer(decoder, compressed, compressed_len) != CHARLS_OK) {
        charls_jpegls_decoder_destroy(decoder);
        return -1;
    }
    if (charls_jpegls_decoder_read_header(decoder) != CHARLS_OK) {
        charls_jpegls_decoder_destroy(decoder);
        return -1;
    }
    if (charls_jpegls_decoder_decode_to_buffer(decoder, pixels_out, pixels_buf_size, 0) != CHARLS_OK) {
        charls_jpegls_decoder_destroy(decoder);
        return -1;
    }

    charls_jpegls_decoder_destroy(decoder);
    return 0;
}

} // extern "C"
