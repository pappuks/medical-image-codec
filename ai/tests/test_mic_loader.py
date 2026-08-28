"""Loader-level tests: header parsing, blob rejection, decode correctness."""

import sys
from pathlib import Path

import numpy as np
import pytest

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / "ai"))

from mic_loader import (  # noqa: E402
    decompress_pics,
    decompress_pics_auto,
    is_pics_blob,
    parse_pics_header,
)


def test_is_pics_blob():
    blob = (REPO / "web/testdata/CR_pics8.mic").read_bytes()
    assert is_pics_blob(blob)
    assert not is_pics_blob(b"MIC1" + blob[4:])
    assert not is_pics_blob(b"")


def test_parse_header_cr():
    hdr = parse_pics_header((REPO / "web/testdata/CR_pics8.mic").read_bytes())
    assert hdr["width"] == 1760
    assert hdr["height"] == 2140
    assert hdr["strips"] == 8


def test_decompress_auto_matches_bin():
    blob = (REPO / "web/testdata/CR_pics8.mic").read_bytes()
    pixels = decompress_pics_auto(blob, max_threads=8)
    gt = np.fromfile(REPO / "testdata/CR_1760_2140_image.bin", dtype=np.uint16)
    assert np.array_equal(pixels.reshape(-1), gt)


def test_explicit_dims_path():
    blob = (REPO / "web/testdata/CT_pics4.mic").read_bytes()
    pixels = decompress_pics(blob, 512, 512, max_threads=4)
    assert pixels.shape == (512, 512)


def test_rejects_mic1():
    blob = (REPO / "web/testdata/CT.mic").read_bytes()  # plain MIC1, no PICS magic
    with pytest.raises(ValueError, match="PICS"):
        decompress_pics_auto(blob)


def test_rejects_wrong_dims():
    blob = (REPO / "web/testdata/CT_pics4.mic").read_bytes()
    with pytest.raises(RuntimeError):
        decompress_pics(blob, 256, 256, max_threads=4)  # dims mismatch -> C error