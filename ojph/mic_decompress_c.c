// mic_decompress_c.c — C implementation of the MIC decompression pipeline.
// Ports the Go FSE two-state decoder + RLE decoder + Delta decoder to C
// for fair performance comparison against OpenJPH (also C/C++).

#include "mic_decompress_c.h"
#include <stdlib.h>
#include <string.h>

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------
#define MAX_SYMBOL_VALUE 65535
#define MAX_TABLE_LOG    16
#define MIN_TABLE_LOG    5
#define TABLELOG_ABSOLUTE_MAX 17

// ---------------------------------------------------------------------------
// Decode symbol table entry
// ---------------------------------------------------------------------------
typedef struct {
    uint32_t new_state;
    uint16_t symbol;
    uint8_t  nb_bits;
} dec_symbol_t;

// ---------------------------------------------------------------------------
// Reverse bit reader (reads backwards from end of stream)
// ---------------------------------------------------------------------------
typedef struct {
    const uint8_t *in;
    size_t         off;       // next byte to read is at in[off-1]
    uint64_t       value;
    uint8_t        bits_read;
} bit_reader_t;

static inline uint32_t high_bits32(uint32_t val) {
    if (val == 0) return 0;
    return 31 - (uint32_t)__builtin_clz(val);
}

static int bit_reader_init(bit_reader_t *br, const uint8_t *data, size_t len) {
    if (len < 1) return -1;
    br->in = data;
    br->off = len;
    uint8_t v = data[len - 1];
    if (v == 0) return -1;

    br->bits_read = 64;
    br->value = 0;

    if (len >= 8) {
        // fillFastStart
        br->off -= 8;
        const uint8_t *p = br->in + br->off;
        br->value = (uint64_t)p[0] | ((uint64_t)p[1] << 8) |
                    ((uint64_t)p[2] << 16) | ((uint64_t)p[3] << 24) |
                    ((uint64_t)p[4] << 32) | ((uint64_t)p[5] << 40) |
                    ((uint64_t)p[6] << 48) | ((uint64_t)p[7] << 56);
        br->bits_read = 0;
    } else {
        // fill twice for small streams
        for (int pass = 0; pass < 2; pass++) {
            if (br->bits_read < 32) continue;
            if (br->off > 4) {
                const uint8_t *q = br->in + br->off - 4;
                uint32_t low = (uint32_t)q[0] | ((uint32_t)q[1] << 8) |
                               ((uint32_t)q[2] << 16) | ((uint32_t)q[3] << 24);
                br->value = (br->value << 32) | (uint64_t)low;
                br->bits_read -= 32;
                br->off -= 4;
            } else {
                while (br->off > 0) {
                    br->value = (br->value << 8) | (uint64_t)br->in[br->off - 1];
                    br->bits_read -= 8;
                    br->off--;
                }
            }
        }
    }
    br->bits_read += 8 - (uint8_t)high_bits32((uint32_t)v);
    return 0;
}

static inline void bit_reader_fill_fast(bit_reader_t *br) {
    if (br->bits_read < 32) return;
    const uint8_t *p = br->in + br->off - 4;
    uint32_t low = (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
                   ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
    br->value = (br->value << 32) | (uint64_t)low;
    br->bits_read -= 32;
    br->off -= 4;
}

static inline void bit_reader_fill(bit_reader_t *br) {
    if (br->bits_read < 32) return;
    if (br->off > 4) {
        const uint8_t *p = br->in + br->off - 4;
        uint32_t low = (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
                       ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
        br->value = (br->value << 32) | (uint64_t)low;
        br->bits_read -= 32;
        br->off -= 4;
    } else {
        while (br->off > 0) {
            br->value = (br->value << 8) | (uint64_t)br->in[br->off - 1];
            br->bits_read -= 8;
            br->off--;
        }
    }
}

static inline uint32_t bit_reader_get_bits_fast(bit_reader_t *br, uint8_t n) {
    uint32_t v = (uint32_t)((br->value << (br->bits_read & 63)) >> ((64 - n) & 63));
    br->bits_read += n;
    return v;
}

static inline uint32_t bit_reader_get_bits(bit_reader_t *br, uint8_t n) {
    if (n == 0 || br->bits_read >= 64) return 0;
    return bit_reader_get_bits_fast(br, n);
}

static inline int bit_reader_finished(bit_reader_t *br) {
    return br->bits_read >= 64 && br->off == 0;
}

// ---------------------------------------------------------------------------
// Byte reader (forward direction, for FSE header parsing)
// ---------------------------------------------------------------------------
typedef struct {
    const uint8_t *b;
    int            off;
    int            len;
} byte_reader_t;

static inline void byte_reader_init(byte_reader_t *br, const uint8_t *data, size_t len) {
    br->b = data;
    br->off = 0;
    br->len = (int)len;
}

static inline int byte_reader_remain(byte_reader_t *br) {
    return br->len - br->off;
}

static inline uint32_t byte_reader_uint32(byte_reader_t *br) {
    const uint8_t *p = br->b + br->off;
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

static inline void byte_reader_advance(byte_reader_t *br, int n) {
    br->off += n;
}

// ---------------------------------------------------------------------------
// readNCount — parse FSE normalized count header
// ---------------------------------------------------------------------------
static int read_ncount(byte_reader_t *brd, int32_t *norm, uint32_t *symbol_len_out,
                       uint8_t *table_log_out, int *zero_bits_out) {
    uint32_t charnum = 0;
    int previous0 = 0;

    int iend = byte_reader_remain(brd);
    if (iend < 4) return -1;

    uint32_t bit_stream = byte_reader_uint32(brd);
    uint32_t nb_bits = (bit_stream & 0xF) + MIN_TABLE_LOG;
    if (nb_bits > TABLELOG_ABSOLUTE_MAX) return -1;
    bit_stream >>= 4;
    uint32_t bit_count = 4;

    *table_log_out = (uint8_t)nb_bits;
    int32_t remaining = (int32_t)((1 << nb_bits) + 1);
    int32_t threshold = (int32_t)(1 << nb_bits);
    int32_t got_total = 0;
    nb_bits++;

    while (remaining > 1) {
        if (previous0) {
            uint32_t n0 = charnum;
            while ((bit_stream & 0xFFFF) == 0xFFFF) {
                n0 += 24;
                if (brd->off < iend - 5) {
                    byte_reader_advance(brd, 2);
                    bit_stream = byte_reader_uint32(brd) >> bit_count;
                } else {
                    bit_stream >>= 16;
                    bit_count += 16;
                }
            }
            while ((bit_stream & 3) == 3) {
                n0 += 3;
                bit_stream >>= 2;
                bit_count += 2;
            }
            n0 += bit_stream & 3;
            bit_count += 2;
            if (n0 > MAX_SYMBOL_VALUE) return -1;
            while (charnum < n0) {
                norm[charnum & 0xFFFF] = 0;
                charnum++;
            }
            if (brd->off <= iend - 7 || brd->off + (int)(bit_count >> 3) <= iend - 4) {
                byte_reader_advance(brd, (int)(bit_count >> 3));
                bit_count &= 7;
                bit_stream = byte_reader_uint32(brd) >> bit_count;
            } else {
                bit_stream >>= 2;
            }
        }

        int32_t max_val = (2 * threshold - 1) - remaining;
        int32_t count;

        if (((int32_t)bit_stream & (threshold - 1)) < max_val) {
            count = (int32_t)bit_stream & (threshold - 1);
            bit_count += nb_bits - 1;
        } else {
            count = (int32_t)bit_stream & (2 * threshold - 1);
            if (count >= threshold) count -= max_val;
            bit_count += nb_bits;
        }

        count--;
        if (count < 0) {
            remaining += count;
            got_total -= count;
        } else {
            remaining -= count;
            got_total += count;
        }
        norm[charnum & 0xFFFF] = count;
        charnum++;
        previous0 = (count == 0);
        while (remaining < threshold) {
            nb_bits--;
            threshold >>= 1;
        }
        if (brd->off <= iend - 7 || brd->off + (int)(bit_count >> 3) <= iend - 4) {
            byte_reader_advance(brd, (int)(bit_count >> 3));
            bit_count &= 7;
        } else {
            bit_count -= (uint32_t)(8 * (brd->len - 4 - brd->off));
            brd->off = brd->len - 4;
        }
        bit_stream = byte_reader_uint32(brd) >> (bit_count & 31);
    }

    *symbol_len_out = charnum;
    if (charnum <= 1 || charnum > MAX_SYMBOL_VALUE + 1) return -1;
    if (remaining != 1) return -1;
    if (got_total != (1 << *table_log_out)) return -1;

    byte_reader_advance(brd, (int)((bit_count + 7) >> 3));

    // Check for zero_bits (any symbol with prob >= half the table).
    int32_t large_limit = (int32_t)(1 << (*table_log_out - 1));
    *zero_bits_out = 0;
    for (uint32_t i = 0; i < charnum; i++) {
        if (norm[i] >= large_limit) {
            *zero_bits_out = 1;
            break;
        }
    }

    return 0;
}

// ---------------------------------------------------------------------------
// buildDtable — build the FSE decode table
// ---------------------------------------------------------------------------
static inline uint32_t table_step(uint32_t table_size) {
    return (table_size >> 1) + (table_size >> 3) + 3;
}

static int build_dtable(const int32_t *norm, uint32_t symbol_len,
                        uint8_t table_log, dec_symbol_t *dt) {
    uint32_t table_size = 1u << table_log;
    uint32_t high_threshold = table_size - 1;

    // Temporary symbolNext array (stack allocated for typical sizes)
    uint32_t symbol_next[MAX_SYMBOL_VALUE + 1];

    for (uint32_t i = 0; i < symbol_len; i++) {
        if (norm[i] == -1) {
            dt[high_threshold].symbol = (uint16_t)i;
            high_threshold--;
            symbol_next[i] = 1;
        } else {
            symbol_next[i] = (uint32_t)norm[i];
        }
    }

    // Spread symbols
    uint32_t table_mask = table_size - 1;
    uint32_t step = table_step(table_size);
    uint32_t position = 0;
    for (uint32_t ss = 0; ss < symbol_len; ss++) {
        for (int32_t i = 0; i < norm[ss]; i++) {
            dt[position].symbol = (uint16_t)ss;
            position = (position + step) & table_mask;
            while (position > high_threshold) {
                position = (position + step) & table_mask;
            }
        }
    }
    if (position != 0) return -1;

    // Build decode entries
    for (uint32_t u = 0; u < table_size; u++) {
        uint16_t symbol = dt[u].symbol;
        uint32_t next_state = symbol_next[symbol];
        symbol_next[symbol] = next_state + 1;
        uint8_t n_bits = table_log - (uint8_t)high_bits32(next_state);
        uint32_t new_state = (next_state << n_bits) - table_size;
        dt[u].nb_bits = n_bits;
        dt[u].new_state = new_state;
    }
    return 0;
}

// ---------------------------------------------------------------------------
// FSE two-state decompress
// ---------------------------------------------------------------------------
static int fse_decompress_two_state(const uint8_t *data, size_t data_len,
                                    uint16_t *out, int count,
                                    dec_symbol_t *dt, uint8_t table_log,
                                    int zero_bits) {
    bit_reader_t br;
    if (bit_reader_init(&br, data, data_len) != 0) return -1;

    // Read initial states: A first (it was written last in the reversed stream)
    uint32_t stateA = bit_reader_get_bits_fast(&br, table_log);
    uint32_t stateB = bit_reader_get_bits_fast(&br, table_log);

    int remaining = count;
    int off = 0;

    if (!zero_bits) {
        while (br.off >= 8 && remaining >= 4) {
            bit_reader_fill_fast(&br);

            dec_symbol_t nA0 = dt[stateA];
            dec_symbol_t nB0 = dt[stateB];
            uint32_t lowA0 = bit_reader_get_bits_fast(&br, nA0.nb_bits);
            uint32_t lowB0 = bit_reader_get_bits_fast(&br, nB0.nb_bits);
            stateA = nA0.new_state + lowA0;
            stateB = nB0.new_state + lowB0;

            bit_reader_fill_fast(&br);

            dec_symbol_t nA1 = dt[stateA];
            dec_symbol_t nB1 = dt[stateB];
            uint32_t lowA1 = bit_reader_get_bits_fast(&br, nA1.nb_bits);
            uint32_t lowB1 = bit_reader_get_bits_fast(&br, nB1.nb_bits);
            stateA = nA1.new_state + lowA1;
            stateB = nB1.new_state + lowB1;

            out[off + 0] = nA0.symbol;
            out[off + 1] = nB0.symbol;
            out[off + 2] = nA1.symbol;
            out[off + 3] = nB1.symbol;
            off += 4;
            remaining -= 4;
        }
    } else {
        while (br.off >= 8 && remaining >= 4) {
            bit_reader_fill_fast(&br);

            dec_symbol_t nA0 = dt[stateA];
            dec_symbol_t nB0 = dt[stateB];
            uint32_t lowA0 = bit_reader_get_bits(&br, nA0.nb_bits);
            uint32_t lowB0 = bit_reader_get_bits(&br, nB0.nb_bits);
            stateA = nA0.new_state + lowA0;
            stateB = nB0.new_state + lowB0;

            bit_reader_fill_fast(&br);

            dec_symbol_t nA1 = dt[stateA];
            dec_symbol_t nB1 = dt[stateB];
            uint32_t lowA1 = bit_reader_get_bits(&br, nA1.nb_bits);
            uint32_t lowB1 = bit_reader_get_bits(&br, nB1.nb_bits);
            stateA = nA1.new_state + lowA1;
            stateB = nB1.new_state + lowB1;

            out[off + 0] = nA0.symbol;
            out[off + 1] = nB0.symbol;
            out[off + 2] = nA1.symbol;
            out[off + 3] = nB1.symbol;
            off += 4;
            remaining -= 4;
        }
    }

    // Tail: alternate A, B
    while (remaining > 0) {
        bit_reader_fill(&br);
        dec_symbol_t nA = dt[stateA];
        uint32_t lowA = bit_reader_get_bits(&br, nA.nb_bits);
        stateA = nA.new_state + lowA;
        out[off++] = nA.symbol;
        remaining--;
        if (remaining == 0) break;

        bit_reader_fill(&br);
        dec_symbol_t nB = dt[stateB];
        uint32_t lowB = bit_reader_get_bits(&br, nB.nb_bits);
        stateB = nB.new_state + lowB;
        out[off++] = nB.symbol;
        remaining--;
    }

    return 0;
}

// ---------------------------------------------------------------------------
// RLE + Delta decompress (fused)
//
// The RLE stream format:
//   [maxValue] [outlen_hi16] [outlen_lo16] [rle_data...]
//
// RLE protocol:
//   count <= midCount: "same" run — next word is the repeated value, count times
//   count > midCount:  "diff" run — next (count - midCount) words are distinct
// ---------------------------------------------------------------------------
static void rle_delta_decompress(const uint16_t *rle_in, int rle_len,
                                 uint16_t *pixels, int width, int height) {
    // rle_in[0] = delimiterForOverflow (stored by RleCompressU16.Init)
    // This is used to compute midCount for the RLE protocol.
    uint16_t delim_value = rle_in[0];
    int bit_depth = 0;
    {
        uint16_t v = delim_value;
        while (v > 0) { bit_depth++; v >>= 1; }
    }
    if (bit_depth == 0) bit_depth = 1;

    uint16_t mid_count = (uint16_t)((1 << (bit_depth - 1)) - 1);
    // The delimiter for overflow detection in delta decode is rle_in[0] itself.
    uint16_t delimiter = delim_value;

    int ri = 1; // start after the delimiter value (no outlen in DeltaRle format)

    // RLE state
    uint16_t c = 0;
    uint16_t recurring_value = 0;

    // Inline RLE DecodeNext2 as a macro for speed
    #define RLE_NEXT() ({                                \
        uint16_t _val;                                   \
        if (c > 0 && c < mid_count) {                    \
            c--;                                         \
            _val = recurring_value;                      \
        } else {                                         \
            if (c == 0 || c == mid_count) {               \
                c = rle_in[ri++];                        \
                if (c <= mid_count) {                     \
                    recurring_value = rle_in[ri++];      \
                    c--;                                 \
                    _val = recurring_value;               \
                } else {                                 \
                    _val = rle_in[ri++];                  \
                    c--;                                 \
                }                                        \
            } else {                                     \
                _val = rle_in[ri++];                      \
                c--;                                     \
            }                                            \
        }                                                \
        _val;                                            \
    })

    // First decoded RLE symbol is the image maxValue (consumed, not output).
    uint16_t image_max_value = RLE_NEXT();

    // Compute delta threshold from image maxValue.
    int img_depth = 0;
    {
        uint16_t v = image_max_value;
        while (v > 0) { img_depth++; v >>= 1; }
    }
    if (img_depth == 0) img_depth = 1;
    uint16_t delta_threshold = (uint16_t)((1 << (img_depth - 1)) - 1);
    // Update delimiter from image maxValue (should match delim_value but be safe)
    delimiter = (uint16_t)((1 << img_depth) - 1);

    // Delta decode: top-left corner
    {
        uint16_t input_val = RLE_NEXT();
        if (input_val == delimiter) {
            pixels[0] = RLE_NEXT();
        } else {
            pixels[0] = (uint16_t)((int32_t)input_val - (int32_t)delta_threshold);
        }
    }

    // First row (y=0, x>0): only left neighbor
    for (int x = 1; x < width; x++) {
        uint16_t input_val = RLE_NEXT();
        if (input_val == delimiter) {
            pixels[x] = RLE_NEXT();
        } else {
            int32_t diff = (int32_t)input_val - (int32_t)delta_threshold;
            pixels[x] = (uint16_t)((int32_t)pixels[x - 1] + diff);
        }
    }

    // Remaining rows
    for (int y = 1; y < height; y++) {
        int row_start = y * width;

        // x=0: only top neighbor
        {
            uint16_t input_val = RLE_NEXT();
            if (input_val == delimiter) {
                pixels[row_start] = RLE_NEXT();
            } else {
                int32_t diff = (int32_t)input_val - (int32_t)delta_threshold;
                pixels[row_start] = (uint16_t)((int32_t)pixels[row_start - width] + diff);
            }
        }

        // Interior pixels: avg(left, top) prediction
        for (int x = 1; x < width; x++) {
            int idx = row_start + x;
            uint16_t input_val = RLE_NEXT();
            if (input_val == delimiter) {
                pixels[idx] = RLE_NEXT();
            } else {
                int32_t diff = (int32_t)input_val - (int32_t)delta_threshold;
                int32_t pred = ((int32_t)pixels[idx - 1] + (int32_t)pixels[idx - width]) >> 1;
                pixels[idx] = (uint16_t)(pred + diff);
            }
        }
    }

    #undef RLE_NEXT
}

// ---------------------------------------------------------------------------
// Public API: full MIC two-state decompress
// ---------------------------------------------------------------------------
int mic_decompress_two_state(const uint8_t *compressed, size_t compressed_len,
                             uint16_t *pixels_out, int width, int height) {
    // Parse header: [0xFF][0x02][count_u32_le]
    if (compressed_len < 6) return -1;
    if (compressed[0] != 0xFF || compressed[1] != 0x02) return -1;

    uint32_t symbol_count = (uint32_t)compressed[2] |
                            ((uint32_t)compressed[3] << 8) |
                            ((uint32_t)compressed[4] << 16) |
                            ((uint32_t)compressed[5] << 24);

    const uint8_t *payload = compressed + 6;
    size_t payload_len = compressed_len - 6;

    // Parse FSE header (normalized counts)
    int32_t norm[MAX_SYMBOL_VALUE + 1];
    memset(norm, 0, sizeof(norm));
    uint32_t symbol_len = 0;
    uint8_t table_log = 0;
    int zero_bits = 0;

    byte_reader_t brd;
    byte_reader_init(&brd, payload, payload_len);

    if (read_ncount(&brd, norm, &symbol_len, &table_log, &zero_bits) != 0) {
        return -2;
    }

    // Build decode table
    uint32_t table_size = 1u << table_log;
    dec_symbol_t *dt = (dec_symbol_t *)malloc(table_size * sizeof(dec_symbol_t));
    if (!dt) return -3;

    if (build_dtable(norm, symbol_len, table_log, dt) != 0) {
        free(dt);
        return -4;
    }

    // Allocate RLE output buffer
    uint16_t *rle_out = (uint16_t *)malloc(symbol_count * sizeof(uint16_t));
    if (!rle_out) {
        free(dt);
        return -5;
    }

    // FSE decode
    const uint8_t *bitstream_data = payload + brd.off;
    size_t bitstream_len = payload_len - (size_t)brd.off;

    int rc = fse_decompress_two_state(bitstream_data, bitstream_len,
                                      rle_out, (int)symbol_count,
                                      dt, table_log, zero_bits);
    free(dt);
    if (rc != 0) {
        free(rle_out);
        return -6;
    }

    // RLE + Delta decode
    rle_delta_decompress(rle_out, (int)symbol_count, pixels_out, width, height);

    free(rle_out);
    return 0;
}
