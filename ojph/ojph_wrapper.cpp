// ojph_wrapper.cpp — C wrapper implementation for OpenJPH lossless compress/decompress.
// Links against libopenjph for in-process HTJ2K encoding/decoding.

#include "ojph_wrapper.h"

#include <cstring>
#include <cstdio>

#include "openjph/ojph_arch.h"
#include "openjph/ojph_file.h"
#include "openjph/ojph_mem.h"
#include "openjph/ojph_params.h"
#include "openjph/ojph_codestream.h"

extern "C" {

int ojph_compress_u16(const uint16_t *pixels, int width, int height,
                      int bit_depth, uint8_t *out_buf, size_t out_buf_size,
                      size_t *out_len) {
    try {
        ojph::codestream codestream;

        // Configure SIZ marker (image dimensions, bit depth).
        ojph::param_siz siz = codestream.access_siz();
        siz.set_image_extent(ojph::point((ojph::ui32)width, (ojph::ui32)height));
        siz.set_num_components(1);
        siz.set_component(0, ojph::point(1, 1), (ojph::ui32)bit_depth, false);

        // Configure COD marker (reversible/lossless).
        ojph::param_cod cod = codestream.access_cod();
        cod.set_reversible(true);
        cod.set_color_transform(false);

        // Use planar mode for single-component.
        codestream.set_planar(true);

        // Write to memory buffer.
        ojph::mem_outfile mem_out;
        mem_out.open();

        codestream.write_headers(&mem_out);

        // Push image data row by row.
        ojph::ui32 next_comp;
        ojph::line_buf *line = codestream.exchange(NULL, next_comp);

        for (int y = 0; y < height; y++) {
            // Copy one row of uint16 pixels into the line buffer as si32.
            const uint16_t *row = pixels + (size_t)y * width;
            ojph::si32 *dst = line->i32;
            for (int x = 0; x < width; x++) {
                dst[x] = (ojph::si32)row[x];
            }
            line = codestream.exchange(line, next_comp);
        }

        codestream.flush();

        // Read size before closing codestream (close may reset pointers).
        size_t compressed_size = mem_out.get_used_size();
        if (compressed_size == 0) {
            compressed_size = (size_t)mem_out.tell();
        }

        codestream.close();
        if (compressed_size > out_buf_size) {
            return -1; // output buffer too small
        }
        memcpy(out_buf, mem_out.get_data(), compressed_size);
        *out_len = compressed_size;
        mem_out.close();

        return 0;
    } catch (...) {
        return -2;
    }
}

int ojph_decompress_u16(const uint8_t *compressed, size_t compressed_len,
                        uint16_t *pixels_out, size_t pixels_buf_size,
                        int width, int height) {
    try {
        ojph::codestream codestream;

        // Read from memory buffer.
        ojph::mem_infile mem_in;
        mem_in.open(compressed, compressed_len);

        codestream.read_headers(&mem_in);

        // Verify dimensions match.
        ojph::param_siz siz = codestream.access_siz();
        ojph::ui32 recon_width = siz.get_recon_width(0);
        ojph::ui32 recon_height = siz.get_recon_height(0);

        if ((int)recon_width != width || (int)recon_height != height) {
            return -3; // dimension mismatch
        }

        // Check output buffer size.
        size_t needed = (size_t)width * height * sizeof(uint16_t);
        if (pixels_buf_size < needed) {
            return -4; // output buffer too small
        }

        codestream.set_planar(true);
        codestream.create();

        // Pull image data row by row.
        for (int y = 0; y < height; y++) {
            ojph::ui32 comp_num;
            ojph::line_buf *line = codestream.pull(comp_num);

            uint16_t *row = pixels_out + (size_t)y * width;
            ojph::si32 *src = line->i32;
            for (int x = 0; x < width; x++) {
                row[x] = (uint16_t)src[x];
            }
        }

        codestream.close();
        return 0;
    } catch (...) {
        return -5;
    }
}

} // extern "C"
