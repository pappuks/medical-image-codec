"""mic_dataset.py — torch Dataset over MIC PICS blobs, decoded via the C decoder.

Each sample: (uint16 tensor [H, W], dict(name, w, h, raw_bytes, compressed_bytes)).
Device transfer happens in the training loop / after collation — the Dataset
stays device-agnostic and returns CPU tensors, per standard torch practice.
"""

from __future__ import annotations

from pathlib import Path

import numpy as np
import torch
from torch.utils.data import Dataset

from mic_loader import decompress_pics_auto, parse_pics_header


def discover_pics_files(root: str | Path) -> list[Path]:
    """All PICS blobs under root (recursive), excluding the _8s FSE-state twins
    (same pixels, different entropy-coder state — keep one)."""
    root = Path(root)
    return sorted(
        p for p in root.rglob("*_pics[48].mic")
        if not p.name.endswith("_8s.mic") and not p.name.endswith("_4s.mic")
    )


# Module-level collate (picklable) for num_workers>0 DataLoader use with
# variable-size samples: hand the (tensor, meta) pair through as-is.
def passthrough_collate(batch):
    assert len(batch) == 1, "PICSDataset samples are variable-size; use batch_size=1"
    return batch[0]


def _worker_init(_worker_id):
    """Each DataLoader worker process re-loads the C dylib (lazy, cheap)."""
    from mic_loader import load_lib

    load_lib()


class PICSDataset(Dataset):
    """Decode-on-access dataset over PICS blobs.

    Args:
        files: explicit list of blob paths, or None to discover under `root`.
        root: directory searched when `files` is None.
        max_threads: PICS worker threads per decode (8 = paper's PICS-C-8;
            0 = every strip is its own thread).
        verify: if True, raise on any decode whose pixel count mismatches the
            header (cheap sanity; full checksum verification lives in tests).
    """

    def __init__(
        self,
        root: str | Path | None = None,
        files: "list[str | Path] | None" = None,
        max_threads: int = 8,
    ):
        if files is None:
            if root is None:
                raise ValueError("pass either files= or root=")
            files = list(discover_pics_files(root))
        if not files:
            raise ValueError("no PICS blobs found")
        self.files = [Path(f) for f in files]
        self.max_threads = max_threads
        # Cache headers once (cheap: 20-byte PICS header) to avoid re-parsing
        # the header inside __getitem__.
        self.metas = []
        for p in self.files:
            with p.open("rb") as fh:
                head = fh.read(20)
            hdr = parse_pics_header(head)
            self.metas.append({"width": hdr["width"], "height": hdr["height"]})

    def __len__(self) -> int:
        return len(self.files)

    def __getitem__(self, idx: int):
        p = self.files[idx]
        blob = p.read_bytes()
        pixels = decompress_pics_auto(blob, self.max_threads)  # (H, W) uint16, owned
        m = self.metas[idx]
        tensor = torch.from_numpy(pixels)  # shares the numpy buffer
        if not tensor.is_contiguous():
            tensor = tensor.contiguous()
        meta = {
            "name": p.stem,
            "path": str(p),
            "w": m["width"],
            "h": m["height"],
            "raw_bytes": m["width"] * m["height"] * 2,
            "compressed_bytes": len(blob),
            "ratio": (m["width"] * m["height"] * 2) / max(1, len(blob)),
        }
        return tensor, meta


class RawDataset(Dataset):
    """Uncompressed baseline: raw little-endian uint16 pixel dumps.

    Files are matched to known image dimensions (the dumps are headerless);
    `dims` maps stem -> (H, W) and must be provided for shape-aware use.
    """

    def __init__(self, files: "list[str | Path]", dims: "dict[str, tuple[int, int]] | None" = None):
        self.files = [Path(f) for f in files]
        if not self.files:
            raise ValueError("no raw files")
        self.sizes = [f.stat().st_size for f in self.files]
        self.dims = dims or {}

    def __len__(self) -> int:
        return len(self.files)

    def __getitem__(self, idx: int):
        f = self.files[idx]
        n = self.sizes[idx] // 2
        pixels = np.fromfile(f, dtype=np.uint16, count=n)
        h, w = self.dims.get(f.stem, (1, n))
        tensor = torch.from_numpy(pixels.copy()).view(h, w)
        meta = {
            "name": f.stem,
            "path": str(f),
            "raw_bytes": self.sizes[idx],
            "compressed_bytes": self.sizes[idx],
            "ratio": 1.0,
        }
        return tensor, meta