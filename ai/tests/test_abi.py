"""ABI test: the C PICS-8 decoder round-trips real blobs bit-exact."""

import pytest

from conftest import GROUND_TRUTH, assert_decode_matches, pics_blob  # noqa: F401


def test_lib_loads(lib):
    assert lib is not None


def test_pics_header_parse():
    from mic_loader import parse_pics_header

    hdr = parse_pics_header(pics_blob("CT"))  # CT ships as _pics4
    assert hdr["width"] == 512 and hdr["height"] == 512
    assert hdr["strips"] == 4


@pytest.mark.parametrize("name", sorted(GROUND_TRUTH.keys()))
def test_pics8_roundtrip_bitexact(name):
    assert_decode_matches(name)


def test_rejects_non_pics_input():
    import mic_loader

    # a plain MIC1 file (no PICS magic) must be rejected with a clear error
    blob = (pytest.importorskip("pathlib").Path(__file__).parents[2]
            / "web/testdata/CT.mic").read_bytes()
    with pytest.raises(ValueError, match="PICS"):
        mic_loader.decompress_pics_auto(blob)