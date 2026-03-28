// mic_compress_c.c — C encoder for MIC (Medical Image Codec).
// Implements Delta → RLE → FSE (two-state and four-state) in C.
// Output is byte-compatible with the Go encoder and decompressible by
// both the Go and C decompressors in mic_decompress_c.c.

#include "mic_compress_c.h"
#include <stdlib.h>
#include <string.h>

// ---------------------------------------------------------------------------
// Constants (must match mic_decompress_c.c)
// ---------------------------------------------------------------------------
#define MAX_SYM       65535u
#define MIN_TLOG      5
#define MAX_TLOG      16
#define DEFAULT_TLOG  11

// ---------------------------------------------------------------------------
// Bit writer — writes bits LSB-first into a byte buffer (forward direction).
// Compatible with the Go bitWriter used in FSE compression.
// ---------------------------------------------------------------------------
typedef struct {
    uint64_t bits;   // bit buffer
    uint8_t  n;      // bits used in buffer (0..64)
    uint8_t *out;    // output byte buffer (caller-owned)
    size_t   cap;    // buffer capacity
    size_t   len;    // bytes written so far
} bw_t;

static inline void bw_init(bw_t *bw, uint8_t *buf, size_t cap) {
    bw->bits = 0; bw->n = 0;
    bw->out = buf; bw->cap = cap; bw->len = 0;
}

// Add up to 32 bits (n may be 0).
static inline void bw_add32(bw_t *bw, uint32_t v, uint8_t n) {
    uint32_t mask = (n >= 32) ? ~0u : ((1u << n) - 1u);
    bw->bits |= (uint64_t)(v & mask) << (bw->n & 63);
    bw->n += n;
}

// Flush 4 bytes when ≥ 32 bits are buffered.
static inline void bw_flush32(bw_t *bw) {
    if (bw->n < 32) return;
    bw->out[bw->len]   = (uint8_t)bw->bits;
    bw->out[bw->len+1] = (uint8_t)(bw->bits >> 8);
    bw->out[bw->len+2] = (uint8_t)(bw->bits >> 16);
    bw->out[bw->len+3] = (uint8_t)(bw->bits >> 24);
    bw->len += 4;
    bw->n   -= 32;
    bw->bits >>= 32;
}

// Flush all complete bytes (equivalent to Go's flush()).
static inline void bw_flush(bw_t *bw) {
    uint8_t nb = bw->n >> 3;
    for (uint8_t i = 0; i < nb; i++)
        bw->out[bw->len++] = (uint8_t)(bw->bits >> (i * 8));
    bw->bits >>= (nb << 3);
    bw->n &= 7;
}

// Add sentinel 1-bit and flush all remaining bits (byte-aligned).
// Equivalent to Go's close() = addBits16Clean(1,1) + flushAlign().
static void bw_close(bw_t *bw) {
    bw_add32(bw, 1, 1);
    uint8_t nb = (bw->n + 7) >> 3;
    for (uint8_t i = 0; i < nb; i++)
        bw->out[bw->len++] = (uint8_t)(bw->bits >> (i * 8));
    bw->n = 0; bw->bits = 0;
}

// ---------------------------------------------------------------------------
// Symbol transform table entry (for FSE compression).
// ---------------------------------------------------------------------------
typedef struct {
    int32_t  dfind;   // deltaFindState
    uint32_t dnbits;  // deltaNbBits
} sym_tt_t;

// Encode one symbol: update state and write bits.
static inline void enc_sym(uint32_t *state, const sym_tt_t *tt,
                             const uint32_t *st_table, bw_t *bw) {
    uint32_t nb_out = (*state + tt->dnbits) >> 16;
    int32_t  dst    = (int32_t)(*state >> (nb_out & 31)) + tt->dfind;
    bw_add32(bw, *state, (uint8_t)nb_out);
    *state = st_table[dst];
}

// ---------------------------------------------------------------------------
// Helper: floor(log2(v)) for v > 0, 0 for v == 0.
// ---------------------------------------------------------------------------
static inline uint32_t high_bits(uint32_t v) {
    if (!v) return 0;
    return 31u - (uint32_t)__builtin_clz(v);
}

// ---------------------------------------------------------------------------
// Histogram: count symbol frequencies in a uint16 array.
// Dual-buffer approach to reduce store-to-load forwarding stalls.
// ---------------------------------------------------------------------------
static void histogram(const uint16_t *in, int n,
                       uint32_t *cnt,       // caller: size MAX_SYM+1
                       uint32_t *sym_len_out,
                       uint32_t *max_cnt_out) {
    uint32_t *c2 = (uint32_t *)calloc(MAX_SYM + 1, sizeof(uint32_t));
    if (!c2) { *sym_len_out = 0; *max_cnt_out = 0; return; }
    memset(cnt, 0, (MAX_SYM + 1) * sizeof(uint32_t));

    int i = 0;
    for (; i + 1 < n; i += 2) { cnt[in[i]]++; c2[in[i+1]]++; }
    if (i < n) cnt[in[i]]++;

    uint32_t ml = 0, sl = 0;
    for (uint32_t j = MAX_SYM + 1; j > 0; j--) {
        uint32_t m = cnt[j-1] + c2[j-1];
        cnt[j-1] = m;
        if (m) { if (!sl) sl = j; if (m > ml) ml = m; }
    }
    free(c2);
    *sym_len_out = sl;
    *max_cnt_out = ml;
}

// ---------------------------------------------------------------------------
// Table log selection (adaptive, matching Go's optimalTableLog).
// ---------------------------------------------------------------------------
static uint8_t select_tlog(int n, uint32_t sym_len) {
    if (n <= 1 || sym_len <= 1) return MIN_TLOG;
    uint8_t tl = DEFAULT_TLOG;

    uint8_t min_src = (uint8_t)(high_bits((uint32_t)(n-1)) + 1);
    uint8_t min_sym = (uint8_t)(high_bits(sym_len - 1) + 2);
    uint8_t min_b   = (min_src < min_sym) ? min_src : min_sym;

    uint8_t max_src = (uint8_t)high_bits((uint32_t)(n-1));
    if (max_src >= 2) max_src -= 2; else max_src = 0;

    if (max_src < tl) tl = max_src;
    if (min_b > tl)   tl = min_b;

    // Adaptive boost for high-density symbol sets (matches Go logic).
    uint32_t density = sym_len ? (uint32_t)n / sym_len : 0;
    if      (sym_len > 512 && density > 16  && tl < 13) tl = 13;
    else if (density > 64  && sym_len > 256 && tl < 12) tl = 12;
    else if (density > 32  && sym_len > 128 && tl < 12) tl = 12;

    if (max_src < tl) tl = max_src;
    if (tl < MIN_TLOG) tl = MIN_TLOG;
    if (tl > MAX_TLOG) tl = MAX_TLOG;
    return tl;
}

// ---------------------------------------------------------------------------
// Count normalization (primary). Returns 0 on success, 1 if fallback needed.
// Matches Go's normalizeCount().
// ---------------------------------------------------------------------------
static const uint32_t rtb[] = {0, 473195, 504333, 520860, 550000, 700000, 750000, 830000};

static int norm_count(const uint32_t *cnt, int32_t *norm,
                       uint32_t sym_len, uint8_t tlog, int n) {
    uint64_t scale  = 62 - (uint64_t)tlog;
    uint64_t step   = ((uint64_t)1 << 62) / (uint64_t)n;
    uint64_t vstep  = (uint64_t)1 << (scale - 20);
    int32_t  remain = (int32_t)(1 << tlog);
    uint32_t low_t  = (uint32_t)n >> tlog;
    int32_t  lar = 0, lar_p = 0;

    for (uint32_t i = 0; i < sym_len; i++) {
        uint32_t c = cnt[i];
        if (!c) { norm[i] = 0; continue; }
        if (c <= low_t) { norm[i] = -1; remain--; continue; }
        int32_t p = (int32_t)(((uint64_t)c * step) >> scale);
        if (p < 8) {
            uint64_t rb = vstep * (uint64_t)rtb[p];
            uint64_t v  = (uint64_t)c * step - ((uint64_t)p << scale);
            if (v > rb) p++;
        }
        if (p > lar_p) { lar_p = p; lar = (int32_t)i; }
        norm[i] = p;
        remain -= p;
    }
    if (-remain >= (norm[lar] >> 1)) return 1;
    norm[lar] += remain;
    return 0;
}

// Fallback normalization. Matches Go's normalizeCount2().
static void norm_count2(const uint32_t *cnt, int32_t *norm,
                          uint32_t sym_len, uint8_t tlog, int n) {
#define NOT_YET (-2)
    uint32_t distributed = 0;
    uint32_t total = (uint32_t)n;
    uint32_t low_t = total >> tlog;
    uint32_t low1  = (total * 3) >> (tlog + 1);

    for (uint32_t i = 0; i < sym_len; i++) {
        uint32_t c = cnt[i];
        if (!c)       { norm[i] = 0;  continue; }
        if (c <= low_t) { norm[i] = -1; distributed++; total -= c; continue; }
        if (c <= low1)  { norm[i] =  1; distributed++; total -= c; continue; }
        norm[i] = NOT_YET;
    }

    uint32_t todo = (1u << tlog) - distributed;
    if (total && todo && (total / todo) > low1) {
        low1 = (total * 3) / (todo * 2);
        for (uint32_t i = 0; i < sym_len; i++) {
            if (norm[i] == NOT_YET && cnt[i] <= low1) {
                norm[i] = 1; distributed++; total -= cnt[i];
            }
        }
        todo = (1u << tlog) - distributed;
    }
    if (distributed == sym_len + 1) {
        uint32_t mv = 0, mc = 0;
        for (uint32_t i = 0; i < sym_len; i++) if (cnt[i] > mc) { mv = i; mc = cnt[i]; }
        norm[mv] += (int32_t)todo;
        return;
    }
    if (!total) {
        for (uint32_t i = 0; todo > 0; i = (i+1) % sym_len)
            if (norm[i] > 0) { todo--; norm[i]++; }
        return;
    }
    uint64_t vsl  = 62 - (uint64_t)tlog;
    uint64_t mid  = ((uint64_t)1 << (vsl - 1)) - 1;
    uint64_t rstep = (((uint64_t)1 << vsl) * todo + mid) / (uint64_t)total;
    uint64_t tmp  = mid;
    for (uint32_t i = 0; i < sym_len; i++) {
        if (norm[i] == NOT_YET) {
            uint64_t end = tmp + (uint64_t)cnt[i] * rstep;
            norm[i] = (int32_t)((end >> vsl) - (tmp >> vsl));
            tmp = end;
        }
    }
#undef NOT_YET
}

// ---------------------------------------------------------------------------
// Write normalized count header to out[*pos..].
// Format is identical to the FSE header read by read_ncount() in the C decompressor.
// Returns 0 on success, -1 on buffer overflow.
// ---------------------------------------------------------------------------
static int write_ncount(const int32_t *norm, uint32_t sym_len, uint8_t tlog,
                          uint8_t *out, size_t cap, size_t *pos) {
    uint32_t table_size = 1u << tlog;
    // Conservative upper bound for the header size.
    size_t max_hdr = ((size_t)sym_len * tlog >> 3) + 16;
    if (*pos + max_hdr + 2 > cap) return -1;

    uint8_t *p = out + *pos;
    size_t   pp = 0;

    uint32_t bstream  = (uint32_t)(tlog - MIN_TLOG); // 4 bits: tableLog-minTableLog
    uint32_t bcount   = 4;
    int32_t  remaining = (int32_t)(table_size + 1);   // +1 extra accuracy
    int32_t  threshold = (int32_t)table_size;
    uint32_t nbits    = (uint32_t)(tlog + 1);
    int      prev0    = 0;
    uint32_t charnum  = 0;

    while (remaining > 1) {
        if (prev0) {
            // Encode run of zeros: how many symbols between last non-zero and this one.
            uint32_t start = charnum;
            while (charnum < sym_len && norm[charnum] == 0) charnum++;
            // Groups of 24: emit 0xFFFF marker.
            while (charnum >= start + 24) {
                start += 24;
                bstream += 0xFFFFu << bcount;
                p[pp++] = (uint8_t)bstream;
                p[pp++] = (uint8_t)(bstream >> 8);
                bstream >>= 16;
            }
            // Groups of 3.
            while (charnum >= start + 3) {
                start += 3;
                bstream += 3u << bcount;
                bcount += 2;
            }
            // Remainder 0..2.
            bstream += (charnum - start) << bcount;
            bcount += 2;
            if (bcount > 16) {
                p[pp++] = (uint8_t)bstream;
                p[pp++] = (uint8_t)(bstream >> 8);
                bstream >>= 16;
                bcount -= 16;
            }
        }

        if (charnum >= sym_len) break;

        int32_t count = norm[charnum++];
        int32_t max_v = (2 * threshold - 1) - remaining;

        if (count < 0) remaining += count;
        else           remaining -= count;

        count++; // +1 for extra accuracy (reversed by decoder with count--)
        if (count >= threshold) count += max_v;

        bstream += (uint32_t)count << bcount;
        bcount  += nbits;
        if (count < max_v) bcount--;

        prev0 = (count == 1); // norm==0 encodes as count==1 after the +1

        if (remaining < 1) break;
        while (remaining < threshold) { nbits--; threshold >>= 1; }

        if (bcount > 16) {
            p[pp++] = (uint8_t)bstream;
            p[pp++] = (uint8_t)(bstream >> 8);
            bstream >>= 16;
            bcount -= 16;
        }
    }

    // Flush remaining bits (write 2 bytes, advance by ceil(bcount/8)).
    p[pp]   = (uint8_t)bstream;
    p[pp+1] = (uint8_t)(bstream >> 8); // may write one extra harmless byte
    pp += (bcount + 7) / 8;

    *pos += pp;
    return 0;
}

// ---------------------------------------------------------------------------
// Build FSE compression tables (symbolTT + stateTable).
// Matches Go's buildCTable().
// ---------------------------------------------------------------------------
static int build_ctable(const int32_t *norm, uint32_t sym_len, uint8_t tlog,
                          sym_tt_t *sym_tt,    // caller: size sym_len
                          uint32_t *st_table,  // caller: size 1<<tlog
                          int *zero_bits_out) {
    uint32_t table_size = 1u << tlog;
    uint32_t high_thr   = table_size - 1;

    uint16_t *table_sym = (uint16_t *)malloc(table_size * sizeof(uint16_t));
    if (!table_sym) return -1;

    // Step 1: compute cumulative counts and place low-prob (-1) symbols at top.
    int32_t cumul[MAX_SYM + 2];
    cumul[0] = 0;
    for (uint32_t i = 0; i + 1 < sym_len; i++) {
        if (norm[i] == -1) {
            cumul[i+1] = cumul[i] + 1;
            table_sym[high_thr--] = (uint16_t)i;
        } else {
            cumul[i+1] = cumul[i] + (int32_t)norm[i];
        }
    }
    {
        uint32_t last = sym_len - 1;
        if (norm[last] == -1) {
            cumul[last+1] = cumul[last] + 1;
            table_sym[high_thr--] = (uint16_t)last;
        } else {
            cumul[last+1] = cumul[last] + (int32_t)norm[last];
        }
        if ((uint32_t)cumul[sym_len] != table_size) { free(table_sym); return -2; }
        cumul[sym_len] = (int32_t)table_size + 1; // sentinel
    }

    // Step 2: spread symbols using the standard FSE step.
    *zero_bits_out = 0;
    uint32_t step  = (table_size >> 1) + (table_size >> 3) + 3;
    uint32_t tmask = table_size - 1;
    uint32_t pos   = 0;
    int32_t  ll    = (int32_t)(1 << (tlog - 1));
    for (uint32_t i = 0; i < sym_len; i++) {
        if (norm[i] > ll) *zero_bits_out = 1;
        for (int32_t j = 0; j < norm[i]; j++) {
            table_sym[pos] = (uint16_t)i;
            pos = (pos + step) & tmask;
            while (pos > high_thr) pos = (pos + step) & tmask;
        }
    }
    if (pos != 0) { free(table_sym); return -3; }

    // Step 3: build stateTable from tableSymbol.
    // Use a copy of cumul since it gets incremented.
    int32_t cumul2[MAX_SYM + 2];
    memcpy(cumul2, cumul, (sym_len + 1) * sizeof(int32_t));
    for (uint32_t u = 0; u < table_size; u++) {
        uint16_t v = table_sym[u];
        st_table[cumul2[v]] = table_size + u;
        cumul2[v]++;
    }
    free(table_sym);

    // Step 4: build symbolTT (deltaFindState + deltaNbBits).
    int32_t  total = 0;
    uint32_t tl_base = ((uint32_t)tlog << 16) - (1u << tlog);
    for (uint32_t i = 0; i < sym_len; i++) {
        int32_t v = norm[i];
        sym_tt[i].dfind = 0; sym_tt[i].dnbits = 0;
        switch (v) {
        case 0: break;
        case -1:
        case 1:
            sym_tt[i].dnbits = tl_base;
            sym_tt[i].dfind  = total - 1;
            total++;
            break;
        default: {
            uint32_t max_out  = (uint32_t)tlog - high_bits((uint32_t)(v-1));
            uint32_t min_plus = (uint32_t)v << max_out;
            sym_tt[i].dnbits  = (max_out << 16) - min_plus;
            sym_tt[i].dfind   = total - v;
            total += v;
            break;
        }
        }
    }
    if ((uint32_t)total != table_size) return -4;
    return 0;
}

// ---------------------------------------------------------------------------
// FSE two-state compression loop. Matches Go's compress2State().
// Encodes src backwards using two interleaved states (A and B).
// ---------------------------------------------------------------------------
static int compress2state(const uint16_t *src, int src_len,
                            const sym_tt_t *tt, const uint32_t *st_table,
                            uint8_t tlog, int zero_bits, bw_t *bw) {
    uint32_t init_st = 1u << tlog;
    uint32_t sA = init_st, sB = init_st;
    int ip = src_len;

    // Align so remaining count is divisible by 4.
    if (ip & 1) { enc_sym(&sA, &tt[src[ip-1]], st_table, bw); ip--; }
    if (ip & 2) {
        enc_sym(&sB, &tt[src[ip-1]], st_table, bw);
        enc_sym(&sA, &tt[src[ip-2]], st_table, bw);
        ip -= 2;
    }

    // Main loop: 4 symbols per iteration.
    if (!zero_bits && tlog <= 8) {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-2]], st_table, bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    } else if (!zero_bits) {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-2]], st_table, bw);
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    } else if (tlog <= 8) {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-2]], st_table, bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    } else {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-2]], st_table, bw);
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    }

    // Flush final states: B then A (decoder reads A first from reversed stream).
    bw_flush32(bw);
    bw_add32(bw, sB, tlog);
    bw_flush32(bw);
    bw_add32(bw, sA, tlog);
    bw_close(bw);
    return 0;
}

// ---------------------------------------------------------------------------
// FSE four-state compression loop. Matches Go's compress4State().
// Encodes src backwards using four interleaved states (A, B, C, D).
// ---------------------------------------------------------------------------
static int compress4state(const uint16_t *src, int src_len,
                            const sym_tt_t *tt, const uint32_t *st_table,
                            uint8_t tlog, int zero_bits, bw_t *bw) {
    uint32_t init_st = 1u << tlog;
    uint32_t sA = init_st, sB = init_st, sC = init_st, sD = init_st;
    int ip = src_len;

    // Align ip to a multiple of 4.
    // pos (ip-1) mod 4 determines which state handles it.
    switch (ip & 3) {
    case 1:
        enc_sym(&sA, &tt[src[ip-1]], st_table, bw);
        ip--;
        break;
    case 2:
        enc_sym(&sB, &tt[src[ip-1]], st_table, bw);
        enc_sym(&sA, &tt[src[ip-2]], st_table, bw);
        ip -= 2;
        break;
    case 3:
        enc_sym(&sC, &tt[src[ip-1]], st_table, bw);
        enc_sym(&sB, &tt[src[ip-2]], st_table, bw);
        enc_sym(&sA, &tt[src[ip-3]], st_table, bw);
        ip -= 3;
        break;
    }

    // Main loop: 4 symbols per iteration.
    // With ip%4==0: ip-1≡3→D, ip-2≡2→C, ip-3≡1→B, ip-4≡0→A
    if (!zero_bits && tlog <= 8) {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sD, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sC, &tt[src[ip-2]], st_table, bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    } else if (!zero_bits) {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sD, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sC, &tt[src[ip-2]], st_table, bw);
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    } else if (tlog <= 8) {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sD, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sC, &tt[src[ip-2]], st_table, bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    } else {
        for (; ip >= 4; ip -= 4) {
            bw_flush32(bw);
            enc_sym(&sD, &tt[src[ip-1]], st_table, bw);
            enc_sym(&sC, &tt[src[ip-2]], st_table, bw);
            bw_flush32(bw);
            enc_sym(&sB, &tt[src[ip-3]], st_table, bw);
            enc_sym(&sA, &tt[src[ip-4]], st_table, bw);
        }
    }

    // Flush final states D→C→B→A. Decoder reads A first (reversed stream).
    bw_flush32(bw); bw_add32(bw, sD, tlog);
    bw_flush32(bw); bw_add32(bw, sC, tlog);
    bw_flush32(bw); bw_add32(bw, sB, tlog);
    bw_flush32(bw); bw_add32(bw, sA, tlog);
    bw_close(bw);
    return 0;
}

// ---------------------------------------------------------------------------
// RLE encoder state. Matches Go's RleCompressU16.
// ---------------------------------------------------------------------------
typedef struct {
    uint16_t *out;      // output uint16 array
    int       out_len;
    int       out_cap;
    uint16_t *buf;      // lookahead buffer (max size: mid_count+2)
    int       buf_len;
    int       buf_cap;
    int       same;     // 1 = same mode, 0 = diff mode
    uint16_t  mid_count;
} rle_enc_t;

// Initialize RLE encoder. delim = delimiterForOverflow = first element of output.
static int rle_init(rle_enc_t *r, uint16_t delim, int w, int h) {
    int depth = 0;
    for (uint16_t v = delim; v > 0; v >>= 1) depth++;
    if (!depth) depth = 1;
    r->mid_count = (uint16_t)((1 << (depth - 1)) - 1);

    r->out_cap = w * h * 2 + 32;
    r->out = (uint16_t *)malloc((size_t)r->out_cap * sizeof(uint16_t));
    if (!r->out) return -1;

    r->buf_cap = (int)r->mid_count + 4;
    r->buf = (uint16_t *)malloc((size_t)r->buf_cap * sizeof(uint16_t));
    if (!r->buf) { free(r->out); r->out = NULL; return -1; }

    r->out_len = 0; r->buf_len = 0; r->same = 0;
    r->out[r->out_len++] = delim; // first element: delimiterForOverflow
    return 0;
}

// Encode one symbol. Faithfully ported from Go's RleCompressU16.Encode.
static void rle_encode(rle_enc_t *r, uint16_t sym) {
    int bc = r->buf_len;
    if (bc < 2) { r->buf[r->buf_len++] = sym; return; }

    uint16_t prev2 = r->buf[bc-2]; // prevPlusOne in Go
    uint16_t prev1 = r->buf[bc-1];

    if (prev2 == prev1 && prev1 == sym) {
        // Three consecutive equal values → same run.
        if (!r->same && bc > 2) {
            // Flush leading diff run (keep last 2 which start the same run).
            int fc = bc - 2;
            r->out[r->out_len++] = r->mid_count + (uint16_t)fc;
            for (int i = 0; i < fc; i++) r->out[r->out_len++] = r->buf[i];
            r->buf[0] = r->buf[bc-2]; r->buf[1] = r->buf[bc-1];
            r->buf_len = 2;
        }
        r->same = 1;
    } else {
        // Not all equal → diff mode.
        if (r->same && bc > 2) {
            // Flush same run; clear buffer.
            r->out[r->out_len++] = (uint16_t)bc;
            r->out[r->out_len++] = r->buf[0];
            r->buf_len = 0;
        }
        r->same = 0;
    }

    bc = r->buf_len;

    // Overflow check: flush when buffer reaches mid_count-1.
    if (bc >= (int)(r->mid_count - 1)) {
        if (r->same) {
            r->out[r->out_len++] = (uint16_t)(bc - 2);
            r->out[r->out_len++] = r->buf[0];
        } else {
            r->out[r->out_len++] = r->mid_count + (uint16_t)(bc - 2);
            for (int i = 0; i < bc - 2; i++) r->out[r->out_len++] = r->buf[i];
        }
        r->buf[0] = r->buf[bc-2]; r->buf[1] = r->buf[bc-1];
        r->buf_len = 2;
    }

    r->buf[r->buf_len++] = sym;
}

// Flush any remaining buffered symbols. Matches Go's RleCompressU16.Flush.
static void rle_finish(rle_enc_t *r) {
    int bc = r->buf_len;
    if (bc <= 0) return;
    if (r->same) {
        r->out[r->out_len++] = (uint16_t)bc;
        r->out[r->out_len++] = r->buf[0];
    } else {
        r->out[r->out_len++] = r->mid_count + (uint16_t)bc;
        for (int i = 0; i < bc; i++) r->out[r->out_len++] = r->buf[i];
    }
}

// ---------------------------------------------------------------------------
// Delta + RLE encode. Matches DeltaRleCompressU16.Compress in Go.
// Returns allocated uint16 array in *rle_out (caller must free), length in *rle_len.
// ---------------------------------------------------------------------------
static int delta_rle_encode(const uint16_t *pixels, int w, int h,
                              uint16_t **rle_out, int *rle_len) {
    int n = w * h;
    uint16_t maxv = 0;
    for (int i = 0; i < n; i++) if (pixels[i] > maxv) maxv = pixels[i];

    // Compute bit depth, delimiter, and delta threshold.
    int depth = 0;
    for (uint16_t v = maxv; v > 0; v >>= 1) depth++;
    if (!depth) depth = 1;

    uint16_t delim = (uint16_t)((1 << depth) - 1); // delimiterForOverflow
    uint16_t thr   = (uint16_t)((1 << (depth - 1)) - 1); // deltaThreshold

    rle_enc_t rle;
    if (rle_init(&rle, delim, w, h) != 0) return -1;

    // First RLE symbol is the image maxValue (read by decompressor to get thr).
    rle_encode(&rle, maxv);

#define ENCODE_DELTA(diff, raw_px) do {                         \
    int32_t _d = (diff);                                        \
    uint16_t _abs = (_d < 0) ? (uint16_t)(-_d) : (uint16_t)_d; \
    if (_abs >= thr) {                                          \
        rle_encode(&rle, delim);                                \
        rle_encode(&rle, (raw_px));                             \
    } else {                                                    \
        rle_encode(&rle, (uint16_t)((int32_t)thr + _d));        \
    }                                                           \
} while (0)

    // (0,0): prediction = 0
    ENCODE_DELTA((int32_t)pixels[0], pixels[0]);

    // First row (y=0, x>0): left neighbor only.
    for (int x = 1; x < w; x++) {
        int32_t pred = (int32_t)pixels[x-1];
        ENCODE_DELTA((int32_t)pixels[x] - pred, pixels[x]);
    }

    // Remaining rows.
    for (int y = 1; y < h; y++) {
        int base = y * w;
        // x=0: top neighbor only.
        {
            int32_t pred = (int32_t)pixels[base - w];
            ENCODE_DELTA((int32_t)pixels[base] - pred, pixels[base]);
        }
        // x>0: average of left and top.
        for (int x = 1; x < w; x++) {
            int idx = base + x;
            int32_t pred = ((int32_t)pixels[idx-1] + (int32_t)pixels[idx-w]) >> 1;
            ENCODE_DELTA((int32_t)pixels[idx] - pred, pixels[idx]);
        }
    }
#undef ENCODE_DELTA

    rle_finish(&rle);
    free(rle.buf);

    *rle_out = rle.out;
    *rle_len = rle.out_len;
    return 0;
}

// ---------------------------------------------------------------------------
// Shared FSE compression core: histogram → normalize → write header → tables → encode.
// magic1 = 0x02 (two-state) or 0x04 (four-state).
// ---------------------------------------------------------------------------
static int fse_compress_core(const uint16_t *rle_buf, int rle_len, uint8_t magic1,
                               uint8_t *out, size_t cap, size_t *out_len) {
    if (rle_len <= 3) return -1;

    // Histogram.
    uint32_t *cnt = (uint32_t *)calloc(MAX_SYM + 1, sizeof(uint32_t));
    int32_t  *norm = (int32_t *)calloc(MAX_SYM + 1, sizeof(int32_t));
    if (!cnt || !norm) { free(cnt); free(norm); return -2; }

    uint32_t sym_len = 0, max_cnt = 0;
    histogram(rle_buf, rle_len, cnt, &sym_len, &max_cnt);

    if (max_cnt == (uint32_t)rle_len) { free(cnt); free(norm); return -3; } // all-same: incompressible
    if (max_cnt == 1) { free(cnt); free(norm); return -4; } // each symbol unique: incompressible

    // Select table log and normalize.
    uint8_t tlog = select_tlog(rle_len, sym_len);
    if (norm_count(cnt, norm, sym_len, tlog, rle_len) != 0)
        norm_count2(cnt, norm, sym_len, tlog, rle_len);
    free(cnt);

    // Write 6-byte MIC header: [0xFF][magic1][count_u32_le]
    if (cap < 8) { free(norm); return -5; }
    out[0] = 0xFF; out[1] = magic1;
    out[2] = (uint8_t)(rle_len);
    out[3] = (uint8_t)(rle_len >> 8);
    out[4] = (uint8_t)(rle_len >> 16);
    out[5] = (uint8_t)(rle_len >> 24);
    size_t pos = 6;

    // Write FSE normalized count header.
    if (write_ncount(norm, sym_len, tlog, out, cap, &pos) != 0) {
        free(norm); return -6;
    }

    // Build compression tables.
    uint32_t table_size = 1u << tlog;
    sym_tt_t *sym_tt  = (sym_tt_t *)malloc(sym_len * sizeof(sym_tt_t));
    uint32_t *st_tab  = (uint32_t *)malloc(table_size * sizeof(uint32_t));
    if (!sym_tt || !st_tab) {
        free(sym_tt); free(st_tab); free(norm); return -7;
    }
    int zero_bits = 0;
    if (build_ctable(norm, sym_len, tlog, sym_tt, st_tab, &zero_bits) != 0) {
        free(sym_tt); free(st_tab); free(norm); return -8;
    }
    free(norm);

    // FSE encode into remainder of output buffer.
    bw_t bw;
    bw_init(&bw, out + pos, cap - pos);

    int rc;
    if (magic1 == 0x02)
        rc = compress2state(rle_buf, rle_len, sym_tt, st_tab, tlog, zero_bits, &bw);
    else
        rc = compress4state(rle_buf, rle_len, sym_tt, st_tab, tlog, zero_bits, &bw);

    free(sym_tt); free(st_tab);
    if (rc != 0) return -9;

    size_t total = pos + bw.len;
    // Reject if compressed output ≥ input (incompressible).
    if (total >= (size_t)rle_len * 2) return -10;

    *out_len = total;
    return 0;
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

int mic_compress_four_state(const uint16_t *pixels, int width, int height,
                             uint8_t *out, size_t out_cap, size_t *out_len) {
    if (!pixels || !out || !out_len || width <= 0 || height <= 0) return -1;
    if (out_cap < (size_t)(width * height * 2 + 4096)) return -1;

    uint16_t *rle_buf = NULL;
    int rle_len = 0;
    if (delta_rle_encode(pixels, width, height, &rle_buf, &rle_len) != 0) return -2;

    int rc = fse_compress_core(rle_buf, rle_len, 0x04, out, out_cap, out_len);
    free(rle_buf);
    return rc;
}

int mic_compress_two_state(const uint16_t *pixels, int width, int height,
                            uint8_t *out, size_t out_cap, size_t *out_len) {
    if (!pixels || !out || !out_len || width <= 0 || height <= 0) return -1;
    if (out_cap < (size_t)(width * height * 2 + 4096)) return -1;

    uint16_t *rle_buf = NULL;
    int rle_len = 0;
    if (delta_rle_encode(pixels, width, height, &rle_buf, &rle_len) != 0) return -2;

    int rc = fse_compress_core(rle_buf, rle_len, 0x02, out, out_cap, out_len);
    free(rle_buf);
    return rc;
}
