#!/usr/bin/env python
"""benchmark_feed.py — raw-vs-MIC GPU feed benchmark (MPS / CUDA / CPU).

Measures two feeding strategies against the same GPU workload:

  1. RAW baseline : pre-loaded raw uint16 pixel dumps -> .to(device)
  2. MIC  feed    : PICS blob -> C PICS-8 decode -> uint16 tensor -> .to(device)

Per config it reports samples/s, decode GB/s, transfer ms, and the headroom
ratio (decode throughput vs. device consume throughput) — the claim under
test is "decode never starves the GPU".

Usage:
  .venv/bin/python ai/benchmark_feed.py --device mps --iterations 30
  .venv/bin/python ai/benchmark_feed.py --device cuda --iterations 30 --threads 8
"""

from __future__ import annotations

import argparse
import json
import platform
import sys
import time
from pathlib import Path

import numpy as np
import torch
from torch.utils.data import DataLoader

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / "ai"))

from mic_dataset import PICSDataset, RawDataset  # noqa: E402
from mic_loader import load_lib  # noqa: E402

# The single-frame corpus (name -> PICS blob in web/testdata/).
SINGLE_FRAME = ["CR", "CT", "MR", "MG1", "MG2", "MG3", "DX_HAND"]

# Simulated per-sample GPU workload: a stack of 3x3 convs consuming the batch.
# `layers` scales the FLOPs; 24 layers ≈ 4 GFLOPs at 512² — the right order for
# a real radiology backbone (ResNet-50-class ≈ 8 GFLOPs at 512²). The default
# keeps the device (not memcpy) as the pacer, which is the honest comparison.
class TinyConv(torch.nn.Module):
    def __init__(self, layers: int = 24, channels: int = 8):
        super().__init__()
        mods = [torch.nn.Conv2d(1, channels, 3, padding=1), torch.nn.ReLU()]
        for _ in range(layers - 1):
            mods += [torch.nn.Conv2d(channels, channels, 3, padding=1), torch.nn.ReLU()]
        mods.append(torch.nn.AdaptiveAvgPool2d(1))
        self.net = torch.nn.Sequential(*mods)

    def forward(self, x):  # x: float32 [B, 1, H, W]
        return self.net(x)


def pick_device(name: str) -> torch.device:
    if name != "auto":
        assert name in ("mps", "cuda", "cpu"), f"unknown device {name}"
        if name == "mps" and not torch.backends.mps.is_available():
            sys.exit("mps requested but not available")
        if name == "cuda" and not torch.cuda.is_available():
            sys.exit("cuda requested but not available")
        return torch.device(name)
    if torch.cuda.is_available():
        return torch.device("cuda")
    if torch.backends.mps.is_available():
        return torch.device("mps")
    return torch.device("cpu")


def corpus_paths():
    pics_files, raw_files, raw_dims = [], [], {}
    RAW_BIN = {
        "CR": ("testdata/CR_1760_2140_image.bin", (2140, 1760)),
        "CT": ("testdata/CT_512_512_image.bin", (512, 512)),
        "MR": ("testdata/MR_256_256_image.bin", (256, 256)),
        "MG1": ("testdata/MG_image_bin2.bin", (2457, 1996)),
        "MG2": ("testdata/MG_Image_2_frame.bin", (2457, 1996)),
        "MG3": None,  # no local bin; MIC feed only
        "DX_HAND": ("testdata/expanded/DX_HAND_1410_1480_image.bin", (1480, 1410)),
    }
    for name in SINGLE_FRAME:
        for variant in ("pics8", "pics4"):
            p = REPO / f"web/testdata/{name}_{variant}.mic"
            if p.exists():
                pics_files.append(p)
                break
        rel = RAW_BIN.get(name)
        if rel:
            rel_path, (h, w) = rel
            if (REPO / rel_path).exists():
                raw_files.append(REPO / rel_path)
                raw_dims[REPO / rel_path] = (h, w)
    return pics_files, raw_files, raw_dims


def time_events(fn, iterations: int, device: torch.device, warmup: int = 3):
    """Median-of-iterations wall time for fn() with device sync. Returns
    (median_s, all_times_s)."""
    for _ in range(warmup):
        fn()
    if device.type == "cuda":
        torch.cuda.synchronize()
    elif device.type == "mps":
        torch.mps.synchronize()
    times = []
    for _ in range(iterations):
        t0 = time.perf_counter()
        fn()
        if device.type == "cuda":
            torch.cuda.synchronize()
        elif device.type == "mps":
            torch.mps.synchronize()
        times.append(time.perf_counter() - t0)
    times.sort()
    return times[len(times) // 2], times


def run_config(kind: str, dataset, loader, model, device, iterations: int, prep):
    """One timed pass: for each sample, optional decode (MIC) + to(device) + model."""
    transfer_s = 0.0
    compute_s = 0.0
    raw_bytes = 0
    n = 0

    # warmup
    for t, meta in dataset:
        model(prep(t))

    times = []
    for _ in range(iterations):
        t0 = time.perf_counter()
        for t, meta in dataset:
            raw_bytes += meta.get("raw_bytes", 0)
            n += 1
            x0 = time.perf_counter()
            x = prep(t)
            if device.type == "cuda":
                torch.cuda.synchronize()
            elif device.type == "mps":
                torch.mps.synchronize()
            x1 = time.perf_counter()
            transfer_s += x1 - x0
            model(x)
            if device.type == "cuda":
                torch.cuda.synchronize()
            elif device.type == "mps":
                torch.mps.synchronize()
            compute_s += time.perf_counter() - x1
        times.append(time.perf_counter() - t0)
    times.sort()
    median = times[len(times) // 2]
    bytes_per_iter = raw_bytes / max(1, iterations)
    return {
        "kind": kind,
        "samples": n // max(1, iterations),
        "raw_mb": bytes_per_iter / 1e6,
        "median_loop_s": median,
        "transfer_s": transfer_s / max(1, iterations),
        "compute_s": compute_s / max(1, iterations),
        "samples_per_s": (n // max(1, iterations)) / median,
        "gbps_fed": bytes_per_iter / 1e9 / median,
        # device consume rate: bytes the device actually processed per second
        # of pure compute (excludes transfer + decode) — the headroom baseline
        "consume_gbps": bytes_per_iter / 1e9 / compute_s if compute_s else 0.0,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--device", default="auto", choices=["auto", "mps", "cuda", "cpu"])
    ap.add_argument("--iterations", type=int, default=30)
    ap.add_argument("--threads", type=int, default=8, help="PICS max_threads (8=paper, 0=all strips)")
    ap.add_argument("--workers", type=int, default=0, help="DataLoader num_workers")
    ap.add_argument("--layers", type=int, default=24, help="conv layers in the pacer model (FLOPs knob)")
    ap.add_argument("--json", action="store_true", help="emit machine-readable JSON at the end")
    args = ap.parse_args()

    device = pick_device(args.device)
    load_lib()  # fail fast if the dylib isn't built

    pics_files, raw_files, raw_dims = corpus_paths()
    print(f"device={device.type}  torch={torch.__version__}  platform={platform.platform()}")
    print(f"corpus: {len(pics_files)} PICS blobs, {len(raw_files)} raw bins, "
          f"iterations={args.iterations}, threads={args.threads}, workers={args.workers}")

    model = TinyConv(layers=args.layers).to(device).eval()
    results = []

    def prep(x):
        """uint16 [H,W] -> float32 [1,1,H,W] on device, center-cropped to 512."""
        x = x.to(device).float().unsqueeze(0).unsqueeze(0)
        h, w = x.shape[-2], x.shape[-1]
        if h > 512 or w > 512:
            ch, cw = h // 2, w // 2
            x = x[..., ch - 256 : ch + 256, cw - 256 : cw + 256]
        return x

    # --- RAW baseline ---
    raw_ds = RawDataset(raw_files, dims=raw_dims)
    with torch.no_grad():
        r_raw = run_config("raw", raw_ds, None, model, device, args.iterations, prep)
    results.append(r_raw)

    # --- MIC feed ---
    mic_ds = PICSDataset(files=pics_files, max_threads=args.threads)
    with torch.no_grad():
        r_mic = run_config("mic", mic_ds, None, model, device, args.iterations, prep)
    results.append(r_mic)

    # --- report ---
    print()
    hdr = f"{'config':8s} {'samples/s':>10s} {'GB/s fed':>9s} {'xfer ms':>8s} {'compute ms':>10s} {'loop ms':>8s}"
    print(hdr)
    print("-" * len(hdr))
    for r in results:
        print(f"{r['kind']:8s} {r['samples_per_s']:>10.1f} {r['gbps_fed']:>9.2f} "
              f"{r['transfer_s']*1000:>8.1f} {r['compute_s']*1000:>10.1f} {r['median_loop_s']*1000:>8.1f}")

    # --- headroom verdict ---
    # decode throughput measured independently (single blob, repeated):
    from mic_loader import decompress_pics_auto

    blob = pics_files[0].read_bytes()
    t0 = time.perf_counter()
    for _ in range(10):
        decompress_pics_auto(blob, args.threads)
    dec_s = (time.perf_counter() - t0) / 10
    hdr_meta = mic_ds.metas[0]
    dec_gbps = hdr_meta["width"] * hdr_meta["height"] * 2 / 1e9 / dec_s
    consume_gbps = r_raw["consume_gbps"]  # device-only processing rate
    headroom = dec_gbps / consume_gbps if consume_gbps else float("inf")

    print()
    print(f"isolated C PICS decode ({pics_files[0].name}): {dec_gbps:.2f} GB/s")
    print(f"device consume rate (compute-only):        {consume_gbps:.2f} GB/s")
    print(f"MIC end-to-end loop vs raw loop:           {r_mic['median_loop_s']/r_raw['median_loop_s']:.2f}x")
    verdict = "NOT the bottleneck" if headroom >= 2.0 else ("MARGINAL" if headroom >= 1.0 else "BOTTLENECK")
    print(f"headroom: decode is {headroom:.1f}x the device consume rate -> decode is {verdict}")

    # --- worker sweep (DataLoader overlap; needs __main__ guard, hence here) ---
    if args.workers < 0:
        print("\n--- DataLoader worker sweep ---")
        print(f"{'workers':>7s} {'samples/s':>10s} {'loop ms':>8s}")
        from torch.utils.data import DataLoader

        from mic_dataset import _worker_init, passthrough_collate

        for nw in (0, 2, 4, 8):
            ds = PICSDataset(files=pics_files, max_threads=args.threads)
            dl = DataLoader(ds, batch_size=1, num_workers=nw,
                            collate_fn=passthrough_collate, worker_init_fn=_worker_init,
                            persistent_workers=(nw > 0))

            def run_dl():
                n_local = 0
                with torch.no_grad():
                    for t, meta in dl:
                        model(prep(t))
                        n_local += 1
                return n_local

            for _ in range(2):
                run_dl()  # warmup
            times = []
            n_seen = 0
            for _ in range(max(5, args.iterations // 2)):
                t0 = time.perf_counter()
                n_seen = run_dl()
                times.append(time.perf_counter() - t0)
            times.sort()
            med = times[len(times) // 2]
            print(f"{nw:>7d} {n_seen / med:>10.1f} {med * 1000:>8.1f}")

    if args.json:
        print(json.dumps({"results": results, "decode_gbps": dec_gbps,
                          "consume_gbps": consume_gbps, "headroom": headroom,
                          "verdict": verdict}, indent=2))


if __name__ == "__main__":
    main()