"""mic_loader.py — ctypes wrapper over the C PICS-8 decoder (mic_decompress_parallel).

Loads libmic_pics.{dylib,so} built by `make` in this directory. The C decoder
reads a PICS blob (the strip-container format, magic "PICS") and writes
width*height uint16 pixels into a caller-supplied buffer. This module is the
ONLY place Python touches the codec ABI.
"""

from __future__ import annotations

import ctypes
import sys
import threading
from pathlib import Path

import numpy as np

_LIB = None
_LIB_PATH: Path | None = None
_lock = threading.Lock()

_PICS_MAGIC = b"PICS"


def default_lib_path() -> Path:
    name = "libmic_pics.dylib" if sys.platform == "darwin" else "libmic_pics.so"
    return Path(__file__).resolve().parent / name


def load_lib(path: str | Path | None = None):
    """Load the shared library once per process, thread-safely."""
    global _LIB, _LIB_PATH
    with _lock:
        if _LIB is None:
            p = Path(path) if path else default_lib_path()
            if not p.exists():
                raise FileNotFoundError(
                    f"{p} not found. Build it first: `make` in the ai/ directory."
                )
            lib = ctypes.CDLL(str(p))
            # int mic_decompress_parallel(const uint8_t *compressed,
            #                             size_t compressed_len,
            #                             uint16_t *pixels_out,
            #                             int width, int height, int max_threads)
            lib.mic_decompress_parallel.restype = ctypes.c_int
            lib.mic_decompress_parallel.argtypes = [
                ctypes.POINTER(ctypes.c_uint8),   # compressed
                ctypes.c_size_t,                  # compressed_len
                ctypes.POINTER(ctypes.c_uint16),  # pixels_out
                ctypes.c_int,                     # width
                ctypes.c_int,                     # height
                ctypes.c_int,                     # max_threads (0 = all strips)
            ]
            _LIB = lib
            _LIB_PATH = p
    return _LIB


def lib_path() -> Path:
    load_lib()
    assert _LIB_PATH is not None, "library loaded but path unset"
    return _LIB_PATH


def is_pics_blob(data: bytes) -> bool:
    return len(data) >= 4 and bytes(data[:4]) == _PICS_MAGIC


def parse_pics_header(data: bytes) -> dict:
    """Parse the PICS header. Layout per ojph/mic_parallel.h + parallelstrips.go:
    magic[4] | width u32le | height u32le | stripCount u32le | headerLen u32le
    (20 bytes before the offset table)."""
    if not is_pics_blob(data):
        raise ValueError("not a PICS blob (bad magic)")
    u32 = lambda off: int.from_bytes(data[off : off + 4], "little")
    return {
        "width": u32(4),
        "height": u32(8),
        "strips": u32(12),
        "header_len": u32(16),
    }


def decompress_pics(data: bytes, width: int, height: int, max_threads: int = 8) -> np.ndarray:
    """Decode a PICS blob via the C decoder. Returns uint16 ndarray shape (h, w).

    The returned array owns its memory (copied out of the ctypes buffer), so
    torch.from_numpy on it is safe to keep around.
    """
    if not is_pics_blob(data):
        raise ValueError(
            "decompress_pics expects a PICS blob (_pics4.mic/_pics8.mic), not a plain MIC1 file"
        )
    lib = load_lib()
    n = width * height
    inp = (ctypes.c_uint8 * len(data)).from_buffer_copy(data)
    out = (ctypes.c_uint16 * n)()
    rc = lib.mic_decompress_parallel(inp, len(data), out, width, height, max_threads)
    if rc != 0:
        raise RuntimeError(f"mic_decompress_parallel failed with code {rc}")
    arr = np.frombuffer(out, dtype=np.uint16, count=n)
    return arr.reshape(height, width).copy()  # copy -> we own the memory


def decompress_pics_auto(data: bytes, max_threads: int = 8) -> np.ndarray:
    """Read dims from the PICS header, then decode."""
    hdr = parse_pics_header(data)
    return decompress_pics(data, hdr["width"], hdr["height"], max_threads)