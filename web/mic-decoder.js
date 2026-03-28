// MIC (Medical Image Codec) - Browser Decoder
// Lossless decompression for Delta+RLE+FSE compressed 16-bit medical images.
// Copyright 2021 Kuldeep Singh. MIT License.
//
// Usage:
//   import { MICDecoder } from './mic-decoder.js';
//   const pixels = MICDecoder.decode(compressedBytes, width, height);
//   // pixels is a Uint16Array of width*height elements
//
//   // Or decode a .mic container file:
//   const { pixels, width, height } = MICDecoder.decodeFile(micFileBytes);

// ─── Constants ───────────────────────────────────────────────────────────────

const MIN_TABLELOG = 5;
const MAX_TABLELOG = 17;
const MAX_SYMBOL_VALUE = 65535;

// ─── Reverse Bit Reader (FSE/ANS) ───────────────────────────────────────────
// Reads bits from the end of the stream backwards, using a 64-bit BigInt buffer.

class BitReader {
  constructor() {
    /** @type {Uint8Array} */
    this.in = null;
    /** @type {number} next byte to read is at in[off - 1] */
    this.off = 0;
    /** @type {bigint} 64-bit buffer */
    this.value = 0n;
    /** @type {number} bits consumed from buffer */
    this.bitsRead = 64;
  }

  /** @param {Uint8Array} input */
  init(input) {
    if (input.length < 1) {
      throw new Error('corrupt stream: too short');
    }
    this.in = input;
    this.off = input.length;
    const lastByte = input[input.length - 1];
    if (lastByte === 0) {
      throw new Error('corrupt stream: did not find end of stream');
    }
    this.bitsRead = 64;
    this.value = 0n;
    if (input.length >= 8) {
      this._fillFastStart();
    } else {
      this.fill();
      this.fill();
    }
    // Skip alignment marker: highest set bit of last byte
    this.bitsRead += 8 - highBits(lastByte);
  }

  _fillFastStart() {
    // Read 8 bytes little-endian as BigInt
    const o = this.off - 8;
    const b = this.in;
    this.value =
      BigInt(b[o]) |
      (BigInt(b[o + 1]) << 8n) |
      (BigInt(b[o + 2]) << 16n) |
      (BigInt(b[o + 3]) << 24n) |
      (BigInt(b[o + 4]) << 32n) |
      (BigInt(b[o + 5]) << 40n) |
      (BigInt(b[o + 6]) << 48n) |
      (BigInt(b[o + 7]) << 56n);
    this.bitsRead = 0;
    this.off -= 8;
  }

  fillFast() {
    if (this.bitsRead < 32) return;
    const o = this.off - 4;
    const b = this.in;
    // >>> 0 on the whole expression to ensure unsigned conversion to BigInt
    const low = BigInt(
      ((b[o]) | (b[o + 1] << 8) | (b[o + 2] << 16) | (b[o + 3] << 24)) >>> 0
    );
    this.value = ((this.value << 32n) | low) & 0xFFFFFFFFFFFFFFFFn;
    this.bitsRead -= 32;
    this.off -= 4;
  }

  fill() {
    if (this.bitsRead < 32) return;
    if (this.off > 4) {
      const o = this.off - 4;
      const b = this.in;
      const low = BigInt(
        ((b[o]) | (b[o + 1] << 8) | (b[o + 2] << 16) | (b[o + 3] << 24)) >>> 0
      );
      this.value = ((this.value << 32n) | low) & 0xFFFFFFFFFFFFFFFFn;
      this.bitsRead -= 32;
      this.off -= 4;
      return;
    }
    while (this.off > 0) {
      this.value = ((this.value << 8n) | BigInt(this.in[this.off - 1])) & 0xFFFFFFFFFFFFFFFFn;
      this.bitsRead -= 8;
      this.off--;
    }
  }

  /** @param {number} n - bits to read (0-32) @returns {number} */
  getBitsFast(n) {
    const shift = BigInt(this.bitsRead & 63);
    const v = Number(
      ((this.value << shift) & 0xFFFFFFFFFFFFFFFFn) >> BigInt((64 - n) & 63)
    );
    this.bitsRead += n;
    return v >>> 0;
  }

  /** @param {number} n - bits to read (0-32), safe version @returns {number} */
  getBits(n) {
    if (n === 0 || this.bitsRead >= 64) return 0;
    return this.getBitsFast(n);
  }

  finished() {
    return this.bitsRead >= 64 && this.off === 0;
  }

  close() {
    if (this.bitsRead > 64) {
      throw new Error('unexpected EOF in bitstream');
    }
  }
}

// ─── Forward Byte Reader ─────────────────────────────────────────────────────
// Reads little-endian values from a byte stream (used for NCount header).

class ByteReader {
  constructor() {
    /** @type {Uint8Array} */
    this.b = null;
    /** @type {number} */
    this.off = 0;
  }

  /** @param {Uint8Array} input */
  init(input) {
    this.b = input;
    this.off = 0;
  }

  advance(n) {
    this.off += n;
  }

  /** @returns {number} uint32 LE at current offset */
  uint32() {
    const o = this.off;
    const b = this.b;
    return (b[o] | (b[o + 1] << 8) | (b[o + 2] << 16) | ((b[o + 3] << 24) >>> 0)) >>> 0;
  }

  /** @returns {Uint8Array} unread portion */
  unread() {
    return this.b.subarray(this.off);
  }

  remain() {
    return this.b.length - this.off;
  }
}

// ─── Helper Functions ────────────────────────────────────────────────────────

/** Number of bits needed to represent val (equivalent to bits.Len32 - 1). */
function highBits(val) {
  if (val === 0) return 0;
  return 31 - Math.clz32(val);
}

/** bits.Len16 equivalent */
function bitsLen16(val) {
  if (val === 0) return 0;
  return 32 - Math.clz32(val);
}

/** Table step for FSE symbol spreading */
function tableStep(tableSize) {
  return ((tableSize >>> 1) + (tableSize >>> 3) + 3) >>> 0;
}

// ─── FSE Decompressor ────────────────────────────────────────────────────────

class FSEDecompressor {
  constructor() {
    this.norm = new Int32Array(MAX_SYMBOL_VALUE + 1);
    this.decTable = null; // array of {newState, symbol, nbBits}
    this.symbolLen = 0;
    this.actualTableLog = 0;
    this.zeroBits = false;
    this.byteReader = new ByteReader();
    this.bitReader = new BitReader();
  }

  /**
   * Decompress FSE-encoded bytes into uint16 symbols.
   * Automatically detects 1-state vs 2-state vs 4-state format via magic bytes.
   * @param {Uint8Array} input - FSE compressed data
   * @returns {Uint16Array} decompressed uint16 symbols
   */
  decompress(input) {
    // 4-state magic: [0xFF][0x04] followed by 4-byte LE count
    if (input.length >= 2 && input[0] === 0xFF && input[1] === 0x04) {
      return this._decompress4State(input);
    }
    // 2-state magic: [0xFF][0x02] followed by 4-byte LE count
    if (input.length >= 2 && input[0] === 0xFF && input[1] === 0x02) {
      return this._decompress2State(input);
    }
    this.byteReader.init(input);
    this._readNCount();
    this._buildDtable();
    return this._decompressStream();
  }

  /**
   * Dispatch for 4-state FSE streams (magic [0xFF][0x04]).
   * @param {Uint8Array} input - full 4-state compressed buffer (including 6-byte header)
   * @returns {Uint16Array}
   */
  _decompress4State(input) {
    if (input.length < 6) throw new Error('fse4state: input too small');
    const count = (input[2] | (input[3] << 8) | (input[4] << 16) | ((input[5] << 24) >>> 0)) >>> 0;
    // FSE header + bitstream start at byte 6
    this.byteReader.init(input.subarray(6));
    this._readNCount();
    this._buildDtable();
    return this._decompressStream4State(count);
  }

  /**
   * Dispatch for 2-state FSE streams (magic [0xFF][0x02]).
   * @param {Uint8Array} input - full 2-state compressed buffer (including 6-byte header)
   * @returns {Uint16Array}
   */
  _decompress2State(input) {
    if (input.length < 6) throw new Error('fse2state: input too small');
    const count = (input[2] | (input[3] << 8) | (input[4] << 16) | ((input[5] << 24) >>> 0)) >>> 0;
    // FSE header + bitstream start at byte 6
    this.byteReader.init(input.subarray(6));
    this._readNCount();
    this._buildDtable();
    return this._decompressStream2State(count);
  }

  /**
   * Decode a 2-state FSE bitstream into exactly `count` symbols.
   * Two independent state machines (A, B) handle alternating symbols,
   * mirroring the Go compress2State / decompress2State implementation.
   * @param {number} count - exact number of symbols to produce
   * @returns {Uint16Array}
   */
  _decompressStream2State(count) {
    const br = this.bitReader;
    br.init(this.byteReader.unread());

    const dt = this.decTable;
    const tableLog = this.actualTableLog;

    // Encoder wrote stateB then stateA; decoder reads stateA first (top of reversed stream).
    let stateA = br.getBits(tableLog);
    br.fill();
    let stateB = br.getBits(tableLog);

    const out = new Uint16Array(count);
    let outPos = 0;
    let remaining = count;

    if (!this.zeroBits) {
      // Fast path: no symbol has nbBits == 0.
      while (br.off >= 8 && remaining >= 4) {
        br.fillFast();

        const nA0 = dt[stateA];
        const nB0 = dt[stateB];
        const lowA0 = br.getBitsFast(nA0.nbBits);
        const lowB0 = br.getBitsFast(nB0.nbBits);
        stateA = nA0.newState + lowA0;
        stateB = nB0.newState + lowB0;

        br.fillFast();

        const nA1 = dt[stateA];
        const nB1 = dt[stateB];
        const lowA1 = br.getBitsFast(nA1.nbBits);
        const lowB1 = br.getBitsFast(nB1.nbBits);
        stateA = nA1.newState + lowA1;
        stateB = nB1.newState + lowB1;

        out[outPos++] = nA0.symbol;
        out[outPos++] = nB0.symbol;
        out[outPos++] = nA1.symbol;
        out[outPos++] = nB1.symbol;
        remaining -= 4;
      }
    } else {
      // Safe path: some symbols output 0 bits.
      while (br.off >= 8 && remaining >= 4) {
        br.fillFast();

        const nA0 = dt[stateA];
        const nB0 = dt[stateB];
        const lowA0 = br.getBits(nA0.nbBits);
        const lowB0 = br.getBits(nB0.nbBits);
        stateA = nA0.newState + lowA0;
        stateB = nB0.newState + lowB0;

        br.fillFast();

        const nA1 = dt[stateA];
        const nB1 = dt[stateB];
        const lowA1 = br.getBits(nA1.nbBits);
        const lowB1 = br.getBits(nB1.nbBits);
        stateA = nA1.newState + lowA1;
        stateB = nB1.newState + lowB1;

        out[outPos++] = nA0.symbol;
        out[outPos++] = nB0.symbol;
        out[outPos++] = nA1.symbol;
        out[outPos++] = nB1.symbol;
        remaining -= 4;
      }
    }

    // Tail: drain remaining symbols in A, B order.
    while (remaining > 0) {
      br.fill();
      const nA = dt[stateA];
      stateA = nA.newState + br.getBits(nA.nbBits);
      out[outPos++] = nA.symbol;
      if (--remaining === 0) break;

      br.fill();
      const nB = dt[stateB];
      stateB = nB.newState + br.getBits(nB.nbBits);
      out[outPos++] = nB.symbol;
      remaining--;
    }

    br.close();
    return out;
  }

  /**
   * Decode a 4-state FSE bitstream into exactly `count` symbols.
   * Four independent state machines (A,B,C,D) handle symbols at
   * positions mod 4 == 0,1,2,3 respectively, mirroring the Go
   * compress4State / decompress4State implementation.
   * @param {number} count - exact number of symbols to produce
   * @returns {Uint16Array}
   */
  _decompressStream4State(count) {
    const br = this.bitReader;
    br.init(this.byteReader.unread());

    const dt = this.decTable;
    const tableLog = this.actualTableLog;

    // Read initial states A→B→(fill)→C→(fill)→D.
    // Mirrors decompress4State in fse4state.go: encoder wrote D→C→B→A,
    // so decoder reads A first (top of reversed stream).
    let stateA = br.getBits(tableLog);
    let stateB = br.getBits(tableLog);
    br.fill();
    let stateC = br.getBits(tableLog);
    br.fill();
    let stateD = br.getBits(tableLog);

    // Pre-allocate exact output size (count is stored in stream header).
    const out = new Uint16Array(count);
    let outPos = 0;
    let remaining = count;

    if (!this.zeroBits) {
      // Fast path: no symbol has nbBits == 0, so getBitsFast is safe.
      while (br.off >= 8 && remaining >= 4) {
        br.fillFast();

        const nA = dt[stateA];
        const nB = dt[stateB];
        const lowA = br.getBitsFast(nA.nbBits);
        const lowB = br.getBitsFast(nB.nbBits);
        stateA = nA.newState + lowA;
        stateB = nB.newState + lowB;

        br.fillFast();

        const nC = dt[stateC];
        const nD = dt[stateD];
        const lowC = br.getBitsFast(nC.nbBits);
        const lowD = br.getBitsFast(nD.nbBits);
        stateC = nC.newState + lowC;
        stateD = nD.newState + lowD;

        out[outPos++] = nA.symbol;
        out[outPos++] = nB.symbol;
        out[outPos++] = nC.symbol;
        out[outPos++] = nD.symbol;
        remaining -= 4;
      }
    } else {
      // Safe path: some symbols output 0 bits; must use getBits to handle n==0.
      while (br.off >= 8 && remaining >= 4) {
        br.fillFast();

        const nA = dt[stateA];
        const nB = dt[stateB];
        const lowA = br.getBits(nA.nbBits);
        const lowB = br.getBits(nB.nbBits);
        stateA = nA.newState + lowA;
        stateB = nB.newState + lowB;

        br.fillFast();

        const nC = dt[stateC];
        const nD = dt[stateD];
        const lowC = br.getBits(nC.nbBits);
        const lowD = br.getBits(nD.nbBits);
        stateC = nC.newState + lowC;
        stateD = nD.newState + lowD;

        out[outPos++] = nA.symbol;
        out[outPos++] = nB.symbol;
        out[outPos++] = nC.symbol;
        out[outPos++] = nD.symbol;
        remaining -= 4;
      }
    }

    // Tail: drain remaining symbols in A, B, C, D order (mirrors Go tail loop).
    while (remaining > 0) {
      br.fill();
      const nA = dt[stateA];
      stateA = nA.newState + br.getBits(nA.nbBits);
      out[outPos++] = nA.symbol;
      if (--remaining === 0) break;

      br.fill();
      const nB = dt[stateB];
      stateB = nB.newState + br.getBits(nB.nbBits);
      out[outPos++] = nB.symbol;
      if (--remaining === 0) break;

      br.fill();
      const nC = dt[stateC];
      stateC = nC.newState + br.getBits(nC.nbBits);
      out[outPos++] = nC.symbol;
      if (--remaining === 0) break;

      br.fill();
      const nD = dt[stateD];
      stateD = nD.newState + br.getBits(nD.nbBits);
      out[outPos++] = nD.symbol;
      remaining--;
    }

    br.close();
    return out;
  }

  _readNCount() {
    const b = this.byteReader;
    if (b.remain() < 4) throw new Error('input too small');

    let bitStream = b.uint32();
    let nbBits = (bitStream & 0xF) + MIN_TABLELOG;
    if (nbBits > MAX_TABLELOG) throw new Error('tableLog too large');
    bitStream >>>= 4;
    let bitCount = 4;

    this.actualTableLog = nbBits;
    let remaining = (1 << nbBits) + 1;
    let threshold = 1 << nbBits;
    let gotTotal = 0;
    nbBits++;

    let charnum = 0;
    let previous0 = false;
    const iend = b.remain();

    while (remaining > 1) {
      if (previous0) {
        let n0 = charnum;
        while ((bitStream & 0xFFFF) === 0xFFFF) {
          n0 += 24;
          if (b.off < iend - 5) {
            b.advance(2);
            bitStream = b.uint32() >>> bitCount;
          } else {
            bitStream >>>= 16;
            bitCount += 16;
          }
        }
        while ((bitStream & 3) === 3) {
          n0 += 3;
          bitStream >>>= 2;
          bitCount += 2;
        }
        n0 += bitStream & 3;
        bitCount += 2;
        if (n0 > MAX_SYMBOL_VALUE) throw new Error('maxSymbolValue too small');
        while (charnum < n0) {
          this.norm[charnum] = 0;
          charnum++;
        }
        if (b.off <= iend - 7 || b.off + (bitCount >>> 3) <= iend - 4) {
          b.advance(bitCount >>> 3);
          bitCount &= 7;
          bitStream = b.uint32() >>> bitCount;
        } else {
          bitStream >>>= 2;
        }
      }

      const max = (2 * threshold - 1) - remaining;
      let count;
      if (((bitStream | 0) & (threshold - 1)) < max) {
        count = (bitStream | 0) & (threshold - 1);
        bitCount += nbBits - 1;
      } else {
        count = (bitStream | 0) & (2 * threshold - 1);
        if (count >= threshold) {
          count -= max;
        }
        bitCount += nbBits;
      }

      count--; // extra accuracy
      if (count < 0) {
        remaining += count;
        gotTotal -= count;
      } else {
        remaining -= count;
        gotTotal += count;
      }
      this.norm[charnum] = count;
      charnum++;
      previous0 = count === 0;
      while (remaining < threshold) {
        nbBits--;
        threshold >>= 1;
      }
      if (b.off <= iend - 7 || b.off + (bitCount >>> 3) <= iend - 4) {
        b.advance(bitCount >>> 3);
        bitCount &= 7;
      } else {
        bitCount -= 8 * (b.b.length - 4 - b.off);
        b.off = b.b.length - 4;
      }
      bitStream = b.uint32() >>> (bitCount & 31);
    }

    this.symbolLen = charnum;
    if (this.symbolLen <= 1) throw new Error(`symbolLen (${this.symbolLen}) too small`);
    if (this.symbolLen > MAX_SYMBOL_VALUE + 1) throw new Error(`symbolLen too big`);
    if (remaining !== 1) throw new Error(`corruption detected (remaining ${remaining} != 1)`);
    if (bitCount > 32) throw new Error(`corruption detected (bitCount ${bitCount} > 32)`);
    if (gotTotal !== 1 << this.actualTableLog) {
      throw new Error(`corruption detected (total ${gotTotal} != ${1 << this.actualTableLog})`);
    }
    b.advance((bitCount + 7) >>> 3);
  }

  _buildDtable() {
    const tableSize = 1 << this.actualTableLog;
    let highThreshold = tableSize - 1;

    // Allocate decode table: array of objects for clarity
    this.decTable = new Array(tableSize);
    for (let i = 0; i < tableSize; i++) {
      this.decTable[i] = { newState: 0, symbol: 0, nbBits: 0 };
    }

    const symbolNext = new Uint32Array(this.symbolLen);

    // Init: lay down low-probability symbols
    this.zeroBits = false;
    const largeLimit = 1 << (this.actualTableLog - 1);
    for (let i = 0; i < this.symbolLen; i++) {
      const v = this.norm[i];
      if (v === -1) {
        this.decTable[highThreshold].symbol = i;
        highThreshold--;
        symbolNext[i] = 1;
      } else {
        if (v >= largeLimit) {
          this.zeroBits = true;
        }
        symbolNext[i] = v;
      }
    }

    // Spread symbols
    const tableMask = tableSize - 1;
    const step = tableStep(tableSize);
    let position = 0;
    for (let ss = 0; ss < this.symbolLen; ss++) {
      const v = this.norm[ss];
      for (let i = 0; i < v; i++) {
        this.decTable[position].symbol = ss;
        position = (position + step) & tableMask;
        while (position > highThreshold) {
          position = (position + step) & tableMask;
        }
      }
    }
    if (position !== 0) throw new Error('corrupted input (position != 0)');

    // Build decoding table
    for (let u = 0; u < tableSize; u++) {
      const symbol = this.decTable[u].symbol;
      const nextState = symbolNext[symbol];
      symbolNext[symbol] = nextState + 1;
      const nBits = this.actualTableLog - highBits(nextState);
      this.decTable[u].nbBits = nBits;
      const newState = (nextState << nBits) - tableSize;
      if (newState >= tableSize) {
        throw new Error(`newState (${newState}) outside table size (${tableSize})`);
      }
      this.decTable[u].newState = newState;
    }
  }

  /** @returns {Uint16Array} */
  _decompressStream() {
    const br = this.bitReader;
    br.init(this.byteReader.unread());

    const dt = this.decTable;
    let state = br.getBits(this.actualTableLog);

    // Output buffer - pre-allocate generously, will trim at end
    let out = new Uint16Array(65536);
    let outPos = 0;

    function ensureCapacity(needed) {
      if (outPos + needed > out.length) {
        const newBuf = new Uint16Array(Math.max(out.length * 2, outPos + needed));
        newBuf.set(out);
        out = newBuf;
      }
    }

    // Main decode loop
    if (!this.zeroBits) {
      while (br.off >= 8) {
        br.fillFast();
        const n0 = dt[state];
        const lb0 = br.getBitsFast(n0.nbBits);
        state = n0.newState + lb0;

        const n1 = dt[state];
        const lb1 = br.getBitsFast(n1.nbBits);
        state = n1.newState + lb1;

        br.fillFast();

        const n2 = dt[state];
        const lb2 = br.getBitsFast(n2.nbBits);
        state = n2.newState + lb2;

        const n3 = dt[state];
        const lb3 = br.getBitsFast(n3.nbBits);
        state = n3.newState + lb3;

        ensureCapacity(4);
        out[outPos++] = n0.symbol;
        out[outPos++] = n1.symbol;
        out[outPos++] = n2.symbol;
        out[outPos++] = n3.symbol;
      }
    } else {
      while (br.off >= 8) {
        br.fillFast();
        const n0 = dt[state];
        const lb0 = br.getBits(n0.nbBits);
        state = n0.newState + lb0;

        const n1 = dt[state];
        const lb1 = br.getBits(n1.nbBits);
        state = n1.newState + lb1;

        br.fillFast();

        const n2 = dt[state];
        const lb2 = br.getBits(n2.nbBits);
        state = n2.newState + lb2;

        const n3 = dt[state];
        const lb3 = br.getBits(n3.nbBits);
        state = n3.newState + lb3;

        ensureCapacity(4);
        out[outPos++] = n0.symbol;
        out[outPos++] = n1.symbol;
        out[outPos++] = n2.symbol;
        out[outPos++] = n3.symbol;
      }
    }

    // Final bits (tail)
    while (true) {
      const isFinished = br.finished() && dt[state].nbBits > 0;
      if (isFinished) {
        if (state !== 0) {
          ensureCapacity(1);
          out[outPos++] = dt[state].symbol;
        }
        break;
      }
      br.fill();
      const n = dt[state];
      const lowBits = br.getBits(n.nbBits);
      state = n.newState + lowBits;
      ensureCapacity(1);
      out[outPos++] = n.symbol;
    }

    br.close();
    return out.subarray(0, outPos);
  }
}

// ─── RLE Decompressor ────────────────────────────────────────────────────────

class RLEDecompressor {
  /**
   * @param {Uint16Array} input - RLE-encoded uint16 stream
   * @param {number} startIndex - starting index in input
   */
  constructor(input, startIndex) {
    this.in = input;
    this.i = startIndex;
    this.c = 0;
    this.midCount = 0;
    this.recurringValue = 0;
  }

  /** Initialize from the maxValue word. */
  initFromMaxValue(maxValue) {
    const pixelDepth = bitsLen16(maxValue);
    this.midCount = (1 << (pixelDepth - 1)) - 1;
  }

  /** Decode the next symbol (DecodeNext2 fast path). @returns {number} */
  decodeNext() {
    // Fast path: in a "same" run
    if (this.c > 0 && this.c < this.midCount) {
      this.c--;
      return this.recurringValue;
    }

    // Need new block header
    if (this.c === 0 || this.c === this.midCount) {
      this.c = this.in[this.i++];
      if (this.c <= this.midCount) {
        this.recurringValue = this.in[this.i++];
        this.c--;
        return this.recurringValue;
      }
    }

    // "diff" run: distinct values
    const output = this.in[this.i++];
    this.c--;
    return output;
  }
}

// ─── Delta+RLE Combined Decompressor ────────────────────────────────────────

/**
 * Decompress a Delta+RLE encoded uint16 stream back to original pixels.
 * This combines RLE decoding with delta prediction reversal in one pass.
 *
 * @param {Uint16Array} rleSymbols - Output from FSE decompression
 * @param {number} width - Image width in pixels
 * @param {number} height - Image height in pixels
 * @returns {Uint16Array} Original pixel data
 */
function deltaRleDecompress(rleSymbols, width, height) {
  // rleSymbols[0] is the RLE's maxValue header (= delimiterForOverflow from compression).
  // The RLE decoder Init reads it and derives midCount from it.
  const rleMaxValue = rleSymbols[0];

  // Initialize RLE decoder: reads rleSymbols[0] as maxValue, starts at index 1
  const rle = new RLEDecompressor(rleSymbols, 1);
  rle.initFromMaxValue(rleMaxValue);

  // The first RLE-decoded symbol is the actual image maxValue.
  // (DeltaRleCompressU16 encodes maxValue as the first symbol.)
  const maxValue = rle.decodeNext();
  const pixelDepth = bitsLen16(maxValue);
  const deltaThreshold = (1 << (pixelDepth - 1)) - 1;
  const delimiterForOverflow = (1 << pixelDepth) - 1;

  const out = new Uint16Array(width * height);

  // Helper: decode one delta+RLE symbol and write to output
  function decodeSymbol(index, prevSymbol) {
    const inputVal = rle.decodeNext();
    if (inputVal === delimiterForOverflow) {
      out[index] = rle.decodeNext();
    } else {
      const diff = inputVal - deltaThreshold;
      out[index] = (prevSymbol + diff) & 0xFFFF;
    }
  }

  // Top-left corner (x=0, y=0): no neighbors
  {
    const inputVal = rle.decodeNext();
    if (inputVal === delimiterForOverflow) {
      out[0] = rle.decodeNext();
    } else {
      out[0] = (inputVal - deltaThreshold) & 0xFFFF;
    }
  }

  // First row (y=0, x>0): only left neighbor
  for (let x = 1; x < width; x++) {
    decodeSymbol(x, out[x - 1]);
  }

  // Remaining rows
  for (let y = 1; y < height; y++) {
    const rowStart = y * width;

    // First column (x=0): only top neighbor
    decodeSymbol(rowStart, out[rowStart - width]);

    // Interior pixels (x>0, y>0): average of left + top
    for (let x = 1; x < width; x++) {
      const idx = rowStart + x;
      const prevSymbol = (out[idx - 1] + out[idx - width]) >> 1;
      decodeSymbol(idx, prevSymbol);
    }
  }

  return out;
}

// ─── Standalone Delta Decompressor (for separate pipeline) ──────────────────

/**
 * Decompress delta-encoded uint16 data back to original pixels.
 * @param {Uint16Array} input - Delta-encoded data (first word is maxValue)
 * @param {number} width
 * @param {number} height
 * @returns {Uint16Array}
 */
function deltaDecompress(input, width, height) {
  const maxValue = input[0];
  const pixelDepth = bitsLen16(maxValue);
  const deltaThreshold = (1 << (pixelDepth - 1)) - 1;
  const delimiterForOverflow = (1 << pixelDepth) - 1;
  const out = new Uint16Array(width * height);
  let ic = 1;

  // Corner (0,0)
  {
    const v = input[ic++];
    if (v === delimiterForOverflow) {
      out[0] = input[ic++];
    } else {
      out[0] = (v - deltaThreshold) & 0xFFFF;
    }
  }

  // First row
  for (let x = 1; x < width; x++) {
    const v = input[ic++];
    if (v === delimiterForOverflow) {
      out[x] = input[ic++];
    } else {
      out[x] = (out[x - 1] + v - deltaThreshold) & 0xFFFF;
    }
  }

  // Remaining rows
  for (let y = 1; y < height; y++) {
    const rowStart = y * width;

    // First column
    const v = input[ic++];
    if (v === delimiterForOverflow) {
      out[rowStart] = input[ic++];
    } else {
      out[rowStart] = (out[rowStart - width] + v - deltaThreshold) & 0xFFFF;
    }

    // Interior
    for (let x = 1; x < width; x++) {
      const idx = rowStart + x;
      const iv = input[ic++];
      if (iv === delimiterForOverflow) {
        out[idx] = input[ic++];
      } else {
        const prev = (out[idx - 1] + out[idx - width]) >> 1;
        out[idx] = (prev + iv - deltaThreshold) & 0xFFFF;
      }
    }
  }

  return out;
}

// ─── Standalone RLE Decompressor ────────────────────────────────────────────

/**
 * Decompress RLE-encoded uint16 data.
 * @param {Uint16Array} input - RLE-encoded stream (first word is maxValue)
 * @returns {Uint16Array}
 */
function rleDecompress(input) {
  const maxValue = input[0];
  const pixelDepth = bitsLen16(maxValue);
  const midCount = (1 << (pixelDepth - 1)) - 1;

  let i = 1;
  // Read output length
  const outLen = (input[i] << 16) + input[i + 1];
  i += 2;

  const rle = new RLEDecompressor(input, i);
  rle.midCount = midCount;

  const out = new Uint16Array(outLen);
  for (let j = 0; j < outLen; j++) {
    out[j] = rle.decodeNext();
  }
  return out;
}

// ─── Container Format ────────────────────────────────────────────────────────
// .mic file format:
//   Bytes 0-3:   Magic "MIC1"
//   Bytes 4-7:   Width  (uint32 LE)
//   Bytes 8-11:  Height (uint32 LE)
//   Bytes 12-15: Pipeline type (uint32 LE): 1=Delta+RLE+FSE
//   Bytes 16-19: Compressed data length (uint32 LE)
//   Bytes 20+:   FSE compressed data

const MIC_MAGIC = 0x3143494D;  // "MIC1" in LE
const MIC2_MAGIC = 0x3243494D; // "MIC2" in LE
const MIC3_MAGIC = 0x3343494D; // "MIC3" in LE
const PICS_MAGIC = 0x53434950; // "PICS" in LE
const MIC2_HEADER_SIZE = 20;
const MIC2_ENTRY_SIZE = 8;
const PICS_HEADER_BASE = 20;   // 4+4+4+4+4 bytes before offset table
const PIPELINE_TEMPORAL = 0x02;

// MIC3 WSI constants
const MIC3_HEADER_SIZE = 48;
const MIC3_LEVEL_SIZE = 20;
const MIC3_TILE_ENTRY_SIZE = 16;
const PLANE_CONSTANT_ZERO = 0;
const PLANE_CONSTANT = 1;
const PLANE_COMPRESSED = 2;
const PLANE_RAW = 3;

// ─── PICS Parallel Strip Support ─────────────────────────────────────────────

/**
 * Parse a PICS parallel-strip header without decompressing.
 * @param {Uint8Array} fileBytes
 * @returns {{ width: number, height: number, numStrips: number, stripH: number,
 *             strips: Array<{offset: number, length: number}>, dataOffset: number }}
 */
function parsePICSHeader(fileBytes) {
  const dv = new DataView(fileBytes.buffer, fileBytes.byteOffset, fileBytes.byteLength);
  if (fileBytes.length < PICS_HEADER_BASE) throw new Error('PICS: file too small');
  const magic = dv.getUint32(0, true);
  if (magic !== PICS_MAGIC) throw new Error('PICS: bad magic');

  const width     = dv.getUint32(4,  true);
  const height    = dv.getUint32(8,  true);
  const numStrips = dv.getUint32(12, true);
  const stripH    = dv.getUint32(16, true);

  const dataOffset = PICS_HEADER_BASE + numStrips * 8;
  if (fileBytes.length < dataOffset) throw new Error('PICS: truncated offset table');

  const strips = [];
  for (let s = 0; s < numStrips; s++) {
    const tbl = PICS_HEADER_BASE + s * 8;
    strips.push({
      offset: dv.getUint32(tbl,     true),
      length: dv.getUint32(tbl + 4, true),
    });
  }

  return { width, height, numStrips, stripH, strips, dataOffset };
}

/**
 * Decode a PICS parallel-strip file sequentially (single-threaded).
 * @param {Uint8Array} fileBytes
 * @returns {{ pixels: Uint16Array, width: number, height: number, isPICS: true, numStrips: number }}
 */
function decodePICS(fileBytes) {
  const hdr = parsePICSHeader(fileBytes);
  const { width, height, numStrips, stripH, strips, dataOffset } = hdr;

  const out = new Uint16Array(width * height);

  for (let s = 0; s < numStrips; s++) {
    const y0 = s * stripH;
    const y1 = Math.min(y0 + stripH, height);
    const sh = y1 - y0;
    const blobStart = dataOffset + strips[s].offset;
    const blob = fileBytes.subarray(blobStart, blobStart + strips[s].length);

    const fse = new FSEDecompressor();
    const rleSymbols = fse.decompress(blob);
    const stripPixels = deltaRleDecompress(rleSymbols, width, sh);
    out.set(stripPixels, y0 * width);
  }

  return { pixels: out, width, height, isPICS: true, numStrips };
}

// ─── MIC2 Multiframe Support ────────────────────────────────────────────────

/**
 * Parse a MIC2 multiframe header without decompressing frame data.
 * @param {Uint8Array} fileBytes
 * @returns {{ width: number, height: number, frameCount: number, temporal: boolean, frameTable: Array<{offset: number, length: number}>, dataOffset: number }}
 */
function parseMIC2Header(fileBytes) {
  const dv = new DataView(fileBytes.buffer, fileBytes.byteOffset, fileBytes.byteLength);
  if (fileBytes.length < MIC2_HEADER_SIZE) throw new Error('MIC2: file too small');

  const magic = dv.getUint32(0, true);
  if (magic !== MIC2_MAGIC) throw new Error('MIC2: invalid magic');

  const width = dv.getUint32(4, true);
  const height = dv.getUint32(8, true);
  const frameCount = dv.getUint32(12, true);
  const flags = fileBytes[16];
  const temporal = (flags & PIPELINE_TEMPORAL) !== 0;

  const tableSize = frameCount * MIC2_ENTRY_SIZE;
  const dataOffset = MIC2_HEADER_SIZE + tableSize;
  if (fileBytes.length < dataOffset) throw new Error('MIC2: file truncated in frame table');

  const frameTable = [];
  for (let i = 0; i < frameCount; i++) {
    const base = MIC2_HEADER_SIZE + i * MIC2_ENTRY_SIZE;
    frameTable.push({
      offset: dv.getUint32(base, true),
      length: dv.getUint32(base + 4, true),
    });
  }

  return { width, height, frameCount, temporal, frameTable, dataOffset };
}

/**
 * Decode temporal delta residuals back to pixels: current = prev + unzigzag(residual).
 * @param {Uint16Array} residual - ZigZag-encoded temporal residuals
 * @param {Uint16Array} prev - Previous frame pixels
 * @returns {Uint16Array}
 */
function temporalDeltaDecode(residual, prev) {
  const out = new Uint16Array(residual.length);
  for (let i = 0; i < residual.length; i++) {
    // UnZigZag: (ux >> 1) ^ -(ux & 1)
    const ux = residual[i];
    const diff = (ux >>> 1) ^ (-(ux & 1));
    out[i] = (prev[i] + diff) & 0xFFFF;
  }
  return out;
}

/**
 * Decompress a temporal residual frame (RLE+FSE, no spatial delta).
 * @param {Uint8Array} compressed - FSE compressed bytes
 * @returns {Uint16Array} - ZigZag-encoded residuals
 */
function decompressResidualFrame(compressed) {
  const fse = new FSEDecompressor();
  const rleData = fse.decompress(compressed);
  // RLE format: rleData[0] = maxValue, then length (2 words), then RLE-encoded data
  return rleDecompress(rleData);
}

// ─── MIC3 WSI Support ────────────────────────────────────────────────────────

/**
 * Parse a MIC3 WSI header.
 * @param {Uint8Array} fileBytes
 * @returns {{ width, height, tileWidth, tileHeight, channels, bitsPerSample, colorTransform, levels, tileTable, dataOffset, totalTiles, isMIC3 }}
 */
function parseMIC3Header(fileBytes) {
  const dv = new DataView(fileBytes.buffer, fileBytes.byteOffset, fileBytes.byteLength);
  if (fileBytes.length < MIC3_HEADER_SIZE) throw new Error('MIC3: file too small');

  const magic = dv.getUint32(0, true);
  if (magic !== MIC3_MAGIC) throw new Error('MIC3: invalid magic');
  const version = dv.getUint32(4, true);
  if (version !== 1) throw new Error(`MIC3: unsupported version ${version}`);

  const width = dv.getUint32(8, true);
  const height = dv.getUint32(12, true);
  const tileWidth = dv.getUint32(16, true);
  const tileHeight = dv.getUint32(20, true);
  const channels = dv.getUint16(24, true);
  const bitsPerSample = fileBytes[26];
  const flags = fileBytes[27];
  const colorTransform = (flags & 0x02) !== 0;
  const levelCount = dv.getUint16(28, true);
  const totalTiles = dv.getUint32(32, true); // low 32 bits of uint64

  let off = MIC3_HEADER_SIZE;
  const levels = [];
  for (let i = 0; i < levelCount; i++) {
    levels.push({
      width: dv.getUint32(off, true),
      height: dv.getUint32(off + 4, true),
      tilesX: dv.getUint32(off + 8, true),
      tilesY: dv.getUint32(off + 12, true),
      firstTileIdx: dv.getUint32(off + 16, true),
    });
    off += MIC3_LEVEL_SIZE;
  }

  const tileTable = [];
  for (let i = 0; i < totalTiles; i++) {
    tileTable.push({
      offset: dv.getUint32(off, true),       // low 32 of uint64
      length: dv.getUint32(off + 8, true),    // low 32 of uint64
    });
    off += MIC3_TILE_ENTRY_SIZE;
  }

  return {
    width, height, tileWidth, tileHeight, channels, bitsPerSample,
    colorTransform, levels, tileTable, dataOffset: off, totalTiles, isMIC3: true,
  };
}

/**
 * YCoCg-R inverse color transform.
 * @param {Uint16Array} y  - luminance plane
 * @param {Uint16Array} co - ZigZag-encoded orange chrominance
 * @param {Uint16Array} cg - ZigZag-encoded green chrominance
 * @param {number} width
 * @param {number} height
 * @returns {Uint8Array} interleaved RGB bytes
 */
function yCoCgRInverse(y, co, cg, width, height) {
  const n = width * height;
  const rgb = new Uint8Array(n * 3);
  for (let i = 0; i < n; i++) {
    const yVal = y[i];
    // UnZigZag: signed = (unsigned >>> 1) ^ -(unsigned & 1)
    const coVal = (co[i] >>> 1) ^ (-(co[i] & 1));
    const cgVal = (cg[i] >>> 1) ^ (-(cg[i] & 1));
    const t = yVal - (cgVal >> 1);
    const g = cgVal + t;
    const b = t - (coVal >> 1);
    const r = coVal + b;
    rgb[i * 3] = r & 0xFF;
    rgb[i * 3 + 1] = g & 0xFF;
    rgb[i * 3 + 2] = b & 0xFF;
  }
  return rgb;
}

/**
 * Decompress a single WSI plane from its encoded blob.
 * @param {Uint8Array} data - plane blob (mode byte + payload)
 * @param {number} tileWidth
 * @param {number} tileHeight
 * @returns {Uint16Array}
 */
function decompressWSIPlane(data, tileWidth, tileHeight) {
  if (data.length === 0) throw new Error('empty plane data');
  const mode = data[0];
  const n = tileWidth * tileHeight;

  switch (mode) {
    case PLANE_CONSTANT_ZERO:
      return new Uint16Array(n);

    case PLANE_CONSTANT: {
      if (data.length < 3) throw new Error('constant plane truncated');
      const val = data[1] | (data[2] << 8);
      const out = new Uint16Array(n);
      out.fill(val);
      return out;
    }

    case PLANE_COMPRESSED: {
      const compressed = data.subarray(1);
      const fse = new FSEDecompressor();
      const rleSymbols = fse.decompress(compressed);
      return deltaRleDecompress(rleSymbols, tileWidth, tileHeight);
    }

    case PLANE_RAW: {
      if (data.length < 1 + n * 2) throw new Error('raw plane truncated');
      const out = new Uint16Array(n);
      for (let i = 0; i < n; i++) {
        out[i] = data[1 + i * 2] | (data[2 + i * 2] << 8);
      }
      return out;
    }

    default:
      throw new Error(`unknown plane mode ${mode}`);
  }
}

/**
 * Decompress an RGB tile blob (3 planes with length headers).
 * @param {Uint8Array} blob
 * @param {number} tileWidth
 * @param {number} tileHeight
 * @param {boolean} colorTransform
 * @returns {Uint8Array} interleaved RGB bytes
 */
function decompressRGBTileBlob(blob, tileWidth, tileHeight, colorTransform) {
  if (blob.length < 12) throw new Error('MIC3: RGB tile blob too small');
  const dv = new DataView(blob.buffer, blob.byteOffset, blob.byteLength);
  const yLen = dv.getUint32(0, true);
  const coLen = dv.getUint32(4, true);
  const cgLen = dv.getUint32(8, true);
  let off = 12;

  const yPlane = decompressWSIPlane(blob.subarray(off, off + yLen), tileWidth, tileHeight);
  off += yLen;
  const coPlane = decompressWSIPlane(blob.subarray(off, off + coLen), tileWidth, tileHeight);
  off += coLen;
  const cgPlane = decompressWSIPlane(blob.subarray(off, off + cgLen), tileWidth, tileHeight);

  if (colorTransform) {
    return yCoCgRInverse(yPlane, coPlane, cgPlane, tileWidth, tileHeight);
  }

  // No color transform: interleave planes as RGB
  const n = tileWidth * tileHeight;
  const rgb = new Uint8Array(n * 3);
  for (let i = 0; i < n; i++) {
    rgb[i * 3] = yPlane[i] & 0xFF;
    rgb[i * 3 + 1] = coPlane[i] & 0xFF;
    rgb[i * 3 + 2] = cgPlane[i] & 0xFF;
  }
  return rgb;
}

/**
 * Decompress all tiles at a given pyramid level and compose into a full image.
 * @param {Uint8Array} fileBytes - complete MIC3 file
 * @param {object} hdr - parsed MIC3 header
 * @param {number} levelIdx - pyramid level index
 * @returns {Uint8Array} interleaved RGB bytes for the full level
 */
function decompressMIC3Level(fileBytes, hdr, levelIdx) {
  const level = hdr.levels[levelIdx];
  const { tileWidth, tileHeight, channels, bitsPerSample, colorTransform } = hdr;
  const bytesPerPixel = channels;

  const result = new Uint8Array(level.width * level.height * bytesPerPixel);

  for (let ty = 0; ty < level.tilesY; ty++) {
    for (let tx = 0; tx < level.tilesX; tx++) {
      const globalIdx = level.firstTileIdx + ty * level.tilesX + tx;
      const entry = hdr.tileTable[globalIdx];
      const start = hdr.dataOffset + entry.offset;
      const blob = fileBytes.subarray(start, start + entry.length);

      let tileRGB;
      if (channels === 3 && bitsPerSample === 8) {
        tileRGB = decompressRGBTileBlob(blob, tileWidth, tileHeight, colorTransform);
      } else {
        throw new Error('MIC3: only 8-bit RGB supported in browser decoder');
      }

      // Copy tile into result, handling edge tiles
      const startX = tx * tileWidth;
      const startY = ty * tileHeight;
      const copyW = Math.min(tileWidth, level.width - startX);
      const copyH = Math.min(tileHeight, level.height - startY);

      for (let y = 0; y < copyH; y++) {
        const srcOff = y * tileWidth * bytesPerPixel;
        const dstOff = ((startY + y) * level.width + startX) * bytesPerPixel;
        const len = copyW * bytesPerPixel;
        result.set(tileRGB.subarray(srcOff, srcOff + len), dstOff);
      }
    }
  }

  return result;
}

// ─── Public API ──────────────────────────────────────────────────────────────

export const MICDecoder = {
  /**
   * Decode FSE-compressed Delta+RLE data back to original 16-bit pixels.
   *
   * @param {Uint8Array} compressedBytes - FSE compressed byte stream
   * @param {number} width - Image width in pixels
   * @param {number} height - Image height in pixels
   * @returns {Uint16Array} Decompressed pixel data (width * height elements)
   */
  decode(compressedBytes, width, height) {
    // Step 1: FSE decompress → RLE-encoded uint16 symbols
    const fse = new FSEDecompressor();
    const rleSymbols = fse.decompress(compressedBytes);

    // Step 2: Combined Delta+RLE decompress → original pixels
    return deltaRleDecompress(rleSymbols, width, height);
  },

  /**
   * Decode a .mic container file (MIC1 single-frame or MIC2 multiframe).
   * For MIC2, returns the first frame and metadata.
   *
   * @param {Uint8Array} fileBytes - Complete .mic file contents
   * @returns {{ pixels: Uint16Array, width: number, height: number, isMIC2?: boolean, frameCount?: number, temporal?: boolean }}
   */
  decodeFile(fileBytes) {
    const dv = new DataView(fileBytes.buffer, fileBytes.byteOffset, fileBytes.byteLength);
    const magic = dv.getUint32(0, true);

    if (magic === PICS_MAGIC) {
      return decodePICS(fileBytes);
    }

    if (magic === MIC3_MAGIC) {
      const hdr = parseMIC3Header(fileBytes);
      const rgb = decompressMIC3Level(fileBytes, hdr, 0);
      return {
        rgb, width: hdr.levels[0].width, height: hdr.levels[0].height,
        channels: hdr.channels, isMIC3: true, mic3Header: hdr,
      };
    }

    if (magic === MIC2_MAGIC) {
      const hdr = parseMIC2Header(fileBytes);
      const pixels = this.decodeMIC2Frame(fileBytes, 0, null, hdr);
      return {
        pixels, width: hdr.width, height: hdr.height,
        isMIC2: true, frameCount: hdr.frameCount, temporal: hdr.temporal,
      };
    }

    if (magic !== MIC_MAGIC) {
      throw new Error(`Invalid .mic file (bad magic: 0x${magic.toString(16)})`);
    }

    const width = dv.getUint32(4, true);
    const height = dv.getUint32(8, true);
    const pipeline = dv.getUint32(12, true);
    const compLen = dv.getUint32(16, true);

    if (pipeline !== 1) {
      throw new Error(`Unsupported pipeline type: ${pipeline} (expected 1 = Delta+RLE+FSE)`);
    }

    const compressedBytes = fileBytes.subarray(20, 20 + compLen);
    const pixels = this.decode(compressedBytes, width, height);

    return { pixels, width, height, isMIC2: false };
  },

  /**
   * Parse MIC2 header without decompressing.
   * @param {Uint8Array} fileBytes
   * @returns {{ width: number, height: number, frameCount: number, temporal: boolean, frameTable: Array, dataOffset: number }}
   */
  parseMIC2Header(fileBytes) {
    return parseMIC2Header(fileBytes);
  },

  /**
   * Parse a PICS parallel-strip header without decompressing.
   * @param {Uint8Array} fileBytes
   * @returns {{ width: number, height: number, numStrips: number, stripH: number, strips: Array, dataOffset: number }}
   */
  parsePICSHeader(fileBytes) {
    return parsePICSHeader(fileBytes);
  },

  /**
   * Decode a single frame from a MIC2 multiframe file.
   * @param {Uint8Array} fileBytes - Complete MIC2 file
   * @param {number} frameIndex - Frame to decode
   * @param {Uint16Array|null} prevPixels - Previous decoded frame (required for temporal mode, frame > 0)
   * @param {object} [hdr] - Pre-parsed header (optional, will parse if not provided)
   * @returns {Uint16Array}
   */
  decodeMIC2Frame(fileBytes, frameIndex, prevPixels, hdr) {
    if (!hdr) hdr = parseMIC2Header(fileBytes);

    const entry = hdr.frameTable[frameIndex];
    const start = hdr.dataOffset + entry.offset;
    const compressed = fileBytes.subarray(start, start + entry.length);

    if (hdr.temporal && frameIndex > 0) {
      // Temporal: decompress residuals (RLE+FSE), then apply temporal delta decode
      const residuals = decompressResidualFrame(compressed);
      if (!prevPixels) {
        throw new Error(`MIC2 temporal: prevPixels required for frame ${frameIndex}`);
      }
      return temporalDeltaDecode(residuals, prevPixels);
    }

    // Independent mode or frame 0: full spatial Delta+RLE+FSE
    return this.decode(compressed, hdr.width, hdr.height);
  },

  /**
   * Decode only the FSE layer (useful for debugging or alternative pipelines).
   * @param {Uint8Array} compressedBytes
   * @returns {Uint16Array}
   */
  fseDecompress(compressedBytes) {
    const fse = new FSEDecompressor();
    return fse.decompress(compressedBytes);
  },

  /**
   * Decode only the RLE layer.
   * @param {Uint16Array} rleData - RLE-encoded data (first word is maxValue)
   * @returns {Uint16Array}
   */
  rleDecompress(rleData) {
    return rleDecompress(rleData);
  },

  /**
   * Decode only the delta layer.
   * @param {Uint16Array} deltaData - Delta-encoded data (first word is maxValue)
   * @param {number} width
   * @param {number} height
   * @returns {Uint16Array}
   */
  deltaDecompress(deltaData, width, height) {
    return deltaDecompress(deltaData, width, height);
  },

  /**
   * Decode combined Delta+RLE from uint16 symbols.
   * @param {Uint16Array} rleSymbols - RLE symbols (first word is maxValue)
   * @param {number} width
   * @param {number} height
   * @returns {Uint16Array}
   */
  deltaRleDecompress(rleSymbols, width, height) {
    return deltaRleDecompress(rleSymbols, width, height);
  },

  /**
   * Parse MIC3 WSI header without decompressing tiles.
   * @param {Uint8Array} fileBytes
   * @returns {object} header with levels, tileTable, etc.
   */
  parseMIC3Header(fileBytes) {
    return parseMIC3Header(fileBytes);
  },

  /**
   * Decode all tiles at a specific pyramid level of a MIC3 file.
   * @param {Uint8Array} fileBytes - Complete MIC3 file
   * @param {number} levelIdx - Pyramid level (0 = full resolution)
   * @returns {{ rgb: Uint8Array, width: number, height: number }}
   */
  decodeMIC3Level(fileBytes, levelIdx) {
    const hdr = parseMIC3Header(fileBytes);
    if (levelIdx < 0 || levelIdx >= hdr.levels.length) {
      throw new Error(`MIC3: level ${levelIdx} out of range [0, ${hdr.levels.length})`);
    }
    const rgb = decompressMIC3Level(fileBytes, hdr, levelIdx);
    return { rgb, width: hdr.levels[levelIdx].width, height: hdr.levels[levelIdx].height };
  },
};

// Also export as default for convenience
export default MICDecoder;
