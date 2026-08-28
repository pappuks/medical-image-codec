"""Shared test fixtures for the ai/ test suite."""

from __future__ import annotations

import sys
from pathlib import Path

import numpy as np
import pytest

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / "ai"))

from mic_loader import decompress_pics_auto, load_lib, parse_pics_header  # noqa: E402

# fnv1a32 over raw little-endian pixel bytes — must match web/pacs-model.mjs.
def fnv1a32_hex(data: bytes | np.ndarray) -> str:
    if isinstance(data, np.ndarray):
        data = data.tobytes()
    h = 0x811C9DC5
    for b in data:
        h ^= b
        h = (h * 0x01000193) & 0xFFFFFFFF
    return f"fnv1a32:{h:08x}"


# Single-frame corpus: name -> (ground-truth bin relative to repo root, or None
# when only the manifest checksum is available locally).
GROUND_TRUTH = {
    "CR": "testdata/CR_1760_2140_image.bin",
    "CT": "testdata/CT_512_512_image.bin",
    "MR": "testdata/MR_256_256_image.bin",
    "MG1": "testdata/MG_image_bin2.bin",
    "MG2": "testdata/MG_Image_2_frame.bin",
    "DX_HAND": "testdata/expanded/DX_HAND_1410_1480_image.bin",
    "MG3": None,  # no local bin; manifest-checksum-verified only
}
# PET1 ships no PICS variant in web/testdata (tiny 256x256 image); excluded
# from the PICS round-trip corpus. CINE_* frames verify via manifest checksum
# and are exercised by the benchmark, not this unit test.


@pytest.fixture(scope="session")
def lib():
    return load_lib()


@pytest.fixture(scope="session")
def manifest():
    import json

    return json.loads((REPO / "web/testdata/manifest.json").read_text())["images"]


def pics_blob(name: str) -> bytes:
    """Load the image's PICS blob. The repo emits exactly one PICS variant per
    image: _pics8 for large images, _pics4 for small ones (see pacs-runner.mjs
    candidatePaths comment). Raises FileNotFoundError if neither exists."""
    base = REPO / "web/testdata"
    for variant in ("pics8", "pics4"):
        p = base / f"{name}_{variant}.mic"
        if p.exists():
            return p.read_bytes()
    raise FileNotFoundError(f"no PICS variant for {name} in {base}")


def assert_decode_matches(name: str):
    """Decode <name>'s PICS-8 blob and verify bit-exactness against ground truth."""
    blob = pics_blob(name)
    hdr = parse_pics_header(blob)
    pixels = decompress_pics_auto(blob)

    assert pixels.shape == (hdr["height"], hdr["width"])
    assert pixels.dtype == np.uint16

    gt_rel = GROUND_TRUTH.get(name)
    if gt_rel:
        gt = np.fromfile(REPO / gt_rel, dtype=np.uint16)
        assert gt.size == pixels.size, f"{name}: size {pixels.size} != gt {gt.size}"
        assert np.array_equal(pixels.reshape(-1), gt), f"{name}: pixels differ from ground truth"
    else:
        want = manifest_checksum(name)
        got = fnv1a32_hex(pixels)
        assert got == want, f"{name}: checksum {got} != manifest {want}"
    return pixels, hdr


def manifest_checksum(name: str) -> str:
    import json

    m = json.loads((REPO / "web/testdata/manifest.json").read_text())["images"]
    return m[name]["checksum"]