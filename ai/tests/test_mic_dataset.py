"""Dataset-level tests: shapes, dtypes, round-trip vs ground truth, DataLoader."""

import sys
from pathlib import Path

import numpy as np
import pytest

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / "ai"))

torch = pytest.importorskip("torch")

from mic_dataset import PICSDataset, RawDataset, discover_pics_files  # noqa: E402


def test_discover_pics_files():
    files = discover_pics_files(REPO / "web/testdata")
    assert len(files) > 0
    names = {f.name for f in files}
    # single-frame corpus present
    assert any(n.startswith(("CR_", "MG1_", "DX_HAND_")) for n in names)
    # no FSE-state twins leaked through
    assert not any(n.endswith(("_8s.mic", "_4s.mic")) for n in names)


def test_dataset_shapes_and_dtype():
    ds = PICSDataset(files=[REPO / "web/testdata/CR_pics8.mic"])
    assert len(ds) == 1
    t, meta = ds[0]
    assert t.dtype == torch.uint16
    assert t.shape == (2140, 1760)  # (H, W)
    assert meta["w"] == 1760 and meta["h"] == 2140
    assert meta["raw_bytes"] == 1760 * 2140 * 2
    assert meta["ratio"] > 3.0  # CR ~3.7x per the paper


def test_dataset_matches_ground_truth():
    from conftest import GROUND_TRUTH

    for name, gt_rel in GROUND_TRUTH.items():
        if gt_rel is None:
            continue
        suffix = "pics8" if (REPO / f"web/testdata/{name}_pics8.mic").exists() else "pics4"
        ds = PICSDataset(files=[REPO / f"web/testdata/{name}_{suffix}.mic"])
        t, _ = ds[0]
        gt = np.fromfile(REPO / gt_rel, dtype=np.uint16)
        assert np.array_equal(t.numpy().reshape(-1), gt), f"{name} mismatch"


def test_dataloader_batching():
    from torch.utils.data import DataLoader

    files = [
        REPO / "web/testdata/MR_pics4.mic",
        REPO / "web/testdata/CT_pics4.mic",
        REPO / "web/testdata/CR_pics8.mic",
    ]
    ds = PICSDataset(files=files)
    # varying shapes -> batch_size=1 with a no-op collate is the honest setup
    dl = DataLoader(ds, batch_size=1, collate_fn=lambda b: b[0])
    items = list(dl)
    assert len(items) == 3
    assert all(isinstance(t, torch.Tensor) and t.dtype == torch.uint16 for t, _ in items)


def test_raw_dataset_baseline():
    raw = RawDataset([REPO / "testdata/CT_512_512_image.bin"])
    t, meta = raw[0]
    assert t.dtype == torch.uint16
    assert meta["ratio"] == 1.0
    assert t.numel() * 2 == 512 * 512 * 2


def test_mps_or_cpu_forward():
    """The sample tensors transfer to the active device cleanly."""
    ds = PICSDataset(files=[REPO / "web/testdata/MR_pics4.mic"])
    t, _ = ds[0]
    if torch.backends.mps.is_available():
        dev = torch.device("mps")
    else:
        dev = torch.device("cpu")
    td = t.to(dev)  # uint16 -> MPS/CUDA may require float conversion on some ops
    assert td.device.type == dev.type