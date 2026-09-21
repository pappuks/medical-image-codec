// jxl_wrapper.c — C wrapper around the libjxl C API for JPEG XL lossless
// compress/decompress of 16-bit single-channel medical images.

#include "jxl_wrapper.h"
#include <jxl/encode.h>
#include <jxl/decode.h>
#include <jxl/types.h>
#include <jxl/color_encoding.h>

int jxl_compress_u16(const uint16_t *pixels, int width, int height,
                     int bit_depth, int effort,
                     uint8_t *out_buf, size_t out_buf_size, size_t *out_len) {
    JxlEncoder *enc = JxlEncoderCreate(NULL);
    if (!enc) return -1;

    JxlEncoderFrameSettings *fs = JxlEncoderFrameSettingsCreate(enc, NULL);
    if (!fs) {
        JxlEncoderDestroy(enc);
        return -1;
    }

    // Mathematically lossless: distance 0, modular mode.
    if (JxlEncoderSetFrameLossless(fs, JXL_TRUE) != JXL_ENC_SUCCESS) {
        JxlEncoderDestroy(enc);
        return -1;
    }
    if (effort >= 1 && effort <= 9) {
        if (JxlEncoderFrameSettingsSetOption(fs, JXL_ENC_FRAME_SETTING_EFFORT,
                                             (int64_t)effort) != JXL_ENC_SUCCESS) {
            JxlEncoderDestroy(enc);
            return -1;
        }
    }

    JxlBasicInfo info;
    JxlEncoderInitBasicInfo(&info);
    info.xsize = (uint32_t)width;
    info.ysize = (uint32_t)height;
    info.bits_per_sample = bit_depth;
    info.exponent_bits_per_sample = 0;
    info.num_color_channels = 1;
    info.num_extra_channels = 0;
    info.alpha_bits = 0;
    // Required so the original (non-XYB) integer samples are preserved losslessly.
    info.uses_original_profile = JXL_TRUE;
    if (JxlEncoderSetBasicInfo(enc, &info) != JXL_ENC_SUCCESS) {
        JxlEncoderDestroy(enc);
        return -1;
    }

    JxlColorEncoding color;
    JxlColorEncodingSetToSRGB(&color, JXL_TRUE /* is_gray */);
    if (JxlEncoderSetColorEncoding(enc, &color) != JXL_ENC_SUCCESS) {
        JxlEncoderDestroy(enc);
        return -1;
    }

    JxlPixelFormat fmt;
    fmt.num_channels = 1;
    fmt.data_type = JXL_TYPE_UINT16;
    fmt.endianness = JXL_NATIVE_ENDIAN;
    fmt.align = 0;

    // Interpret the UINT16 input samples as unscaled integers at the codestream
    // bit depth (bits_per_sample). Without this, libjxl rescales sub-16-bit
    // input by 65535/((1<<bits_per_sample)-1), which breaks the lossless
    // roundtrip for <16-bit medical images.
    JxlBitDepth bit_depth_in;
    bit_depth_in.type = JXL_BIT_DEPTH_FROM_CODESTREAM;
    bit_depth_in.bits_per_sample = bit_depth;
    bit_depth_in.exponent_bits_per_sample = 0;
    if (JxlEncoderSetFrameBitDepth(fs, &bit_depth_in) != JXL_ENC_SUCCESS) {
        JxlEncoderDestroy(enc);
        return -1;
    }

    size_t in_size = (size_t)width * (size_t)height * sizeof(uint16_t);
    if (JxlEncoderAddImageFrame(fs, &fmt, pixels, in_size) != JXL_ENC_SUCCESS) {
        JxlEncoderDestroy(enc);
        return -1;
    }
    JxlEncoderCloseInput(enc);

    // Drain the encoder into out_buf.
    uint8_t *next_out = out_buf;
    size_t avail_out = out_buf_size;
    JxlEncoderStatus status = JXL_ENC_NEED_MORE_OUTPUT;
    while (status == JXL_ENC_NEED_MORE_OUTPUT) {
        status = JxlEncoderProcessOutput(enc, &next_out, &avail_out);
        if (status == JXL_ENC_NEED_MORE_OUTPUT && avail_out == 0) {
            // Output buffer too small.
            JxlEncoderDestroy(enc);
            return -2;
        }
    }
    if (status != JXL_ENC_SUCCESS) {
        JxlEncoderDestroy(enc);
        return -1;
    }

    *out_len = (size_t)(next_out - out_buf);
    JxlEncoderDestroy(enc);
    return 0;
}

int jxl_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                       uint16_t *pixels_out, size_t pixels_buf_size,
                       int width, int height) {
    (void)width;
    (void)height;
    JxlDecoder *dec = JxlDecoderCreate(NULL);
    if (!dec) return -1;

    if (JxlDecoderSubscribeEvents(dec, JXL_DEC_FULL_IMAGE) != JXL_DEC_SUCCESS) {
        JxlDecoderDestroy(dec);
        return -1;
    }
    if (JxlDecoderSetInput(dec, compressed, compressed_len) != JXL_DEC_SUCCESS) {
        JxlDecoderDestroy(dec);
        return -1;
    }
    JxlDecoderCloseInput(dec);

    JxlPixelFormat fmt;
    fmt.num_channels = 1;
    fmt.data_type = JXL_TYPE_UINT16;
    fmt.endianness = JXL_NATIVE_ENDIAN;
    fmt.align = 0;

    for (;;) {
        JxlDecoderStatus status = JxlDecoderProcessInput(dec);
        if (status == JXL_DEC_ERROR) {
            JxlDecoderDestroy(dec);
            return -1;
        } else if (status == JXL_DEC_NEED_IMAGE_OUT_BUFFER) {
            if (JxlDecoderSetImageOutBuffer(dec, &fmt, pixels_out,
                                            pixels_buf_size) != JXL_DEC_SUCCESS) {
                JxlDecoderDestroy(dec);
                return -1;
            }
            // Match the encoder: emit unscaled integers at the codestream bit
            // depth rather than rescaling up to the full 16-bit range. Must be
            // called after JxlDecoderSetImageOutBuffer.
            JxlBitDepth bit_depth_out;
            bit_depth_out.type = JXL_BIT_DEPTH_FROM_CODESTREAM;
            bit_depth_out.bits_per_sample = 0;
            bit_depth_out.exponent_bits_per_sample = 0;
            if (JxlDecoderSetImageOutBitDepth(dec, &bit_depth_out) != JXL_DEC_SUCCESS) {
                JxlDecoderDestroy(dec);
                return -1;
            }
        } else if (status == JXL_DEC_FULL_IMAGE) {
            // Frame decoded into pixels_out.
            JxlDecoderDestroy(dec);
            return 0;
        } else if (status == JXL_DEC_SUCCESS) {
            // Reached end without a full image event.
            JxlDecoderDestroy(dec);
            return 0;
        } else if (status == JXL_DEC_NEED_MORE_INPUT) {
            // All input was provided via a single buffer; this is unexpected.
            JxlDecoderDestroy(dec);
            return -1;
        }
        // Other informative events (basic info, color): keep processing.
    }
}
