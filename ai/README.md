# MIC → AI Pipeline (PyTorch adapter + GPU-feed benchmark)

Prove that MIC is an AI data-plane codec: a PyTorch `Dataset`/`DataLoader`
that decodes MIC-compressed images on the fly via the **C PICS-8 decoder**
(the fastest decoder in the paper) and feeds the GPU without decode becoming
the bottleneck. Runs on **MPS** (Apple Silicon) and **CUDA** (Linux GPU box).

## Architecture decisions

### Decision 1 — decode via the existing C PICS-8 decoder, no new codec code

`ojph/mic_parallel.c` exposes `mic_decompress_parallel()` — the C PICS
parallel-strips decoder. Against `results/20260622-225015/paper-tables.txt`
(Table 4/5) it is the **single fastest decoder in the paper**: ~3.2–4.3 GB/s
on large images (MG1 4,342 MB/s, CR 3,227, XR 3,197, SC1 3,174), ~1.0–1.5 GB/s
on 512×512 CT/MR slices. It is also the fastest encoder on large images.

We build it into a shared library and call it from Python via `ctypes`.
**No Python/Go re-implementation of the codec exists anywhere in this
pipeline** — the C decoder is the same code path the paper benchmarks.

- `max_threads`: pass `8` to reproduce the paper's PICS-C-8 numbers
  apples-to-apples; pass `0` to let every strip run as its own thread.
- Rejected alternatives: pure-Python decode (drift risk vs. the Go/C
  reference), subprocess-per-sample `mic-compress` (fork+IO per sample
  defeats a throughput benchmark), Go `-buildmode=c-shared` (unnecessary —
  the C decoder is already a clean C ABI and is faster).

### Decision 2 — corpus: `web/testdata/` PICS files + checksum-verified ground truth

`go run ./cmd/mic-compress -testdata` emits, per image, PICS variants
(`<NAME>_pics8.mic`, `_pics4.mic`, plus `_8s` FSE-state variants) into
`web/testdata/`, along with `manifest.json` carrying a raw-pixel `fnv1a32`
checksum per image. The benchmark uses:

- **MIC feed:** `web/testdata/<NAME>_pics8.mic` (the PICS-8 strip container).
- **Raw baseline:** the matching raw pixel dump under `testdata/`
  (`testdata/*.bin`, `testdata/expanded/*.bin`), matched to the manifest
  checksum so the pairing is proven, not assumed:

  | image | ground-truth bin (checksum-verified) |
  |---|---|
  | CR | `testdata/CR_1760_2140_image.bin` |
  | CT | `testdata/CT_512_512_image.bin` |
  | MR | `testdata/MR_256_256_image.bin` |
  | MG1 | `testdata/MG_image_bin2.bin` |
  | MG2 | `testdata/MG_Image_2_frame.bin` |
  | DX_HAND | `testdata/expanded/DX_HAND_1410_1480_image.bin` |
  | PET1 | `testdata/expanded/PET_NSCLC1_256_256_image.bin` |
  | MG3 | *(no local bin; verified via manifest `fnv1a32` only)* |
  | CINE_* frames | *(manifest checksum only)* |

Every decode in the test suite is verified bit-exact against the ground
truth (bin bytes where available, manifest `fnv1a32` otherwise).

### Decision 3 — dual GPU backend (MPS + CUDA)

`benchmark_feed.py --device {mps,cuda,cpu}` (default: auto-detect CUDA →
MPS → CPU). This Mac runs MPS; the CUDA box runs the same script with
`--device cuda`. Timing: `torch.cuda.Event` on CUDA; host-side
`time.perf_counter` elsewhere (MPS has no cross-stream event API).

## Build (this directory)

```bash
make            # -> libmic_pics.dylib (macOS) / libmic_pics.so (Linux)
make test       # ABI + bit-exact round-trip via .venv python
```

## CUDA machine setup (Linux + NVIDIA GPU)

```bash
# 1. build the shared lib
make            # produces libmic_pics.so

# 2. install CUDA torch (adjust cuXXX to your CUDA version)
.venv/bin/pip install torch --index-url https://download.pytorch.org/whl/cu126
.venv/bin/pip install numpy pytest

# 3. sanity: torch sees the GPU
.venv/bin/python -c "import torch; print(torch.__version__, torch.cuda.is_available(), torch.cuda.get_device_name(0))"

# 4. run the tests (bit-exact ground-truth round-trips)
.venv/bin/python -m pytest ai/tests/ -v

# 5. run the benchmark (headroom verdict + optional worker sweep via --workers -1)
.venv/bin/python ai/benchmark_feed.py --device cuda --iterations 30 --threads 8
.venv/bin/python ai/benchmark_feed.py --device cuda --iterations 30 --threads 8 --workers -1
```

The Python loader resolves the lib per-platform (`.dylib` on macOS, `.so`
on Linux) or takes an explicit path (`--lib`).

## Measured reference (MPS, Apple Silicon, 2026-08-27)

C PICS-8 decode: 1.6–3.2 GB/s (CR 7.18 MB; paper: 3.23 GB/s) vs. device
consume rate 0.35 GB/s (24-layer conv pacer @512²) → **headroom 4.7–9.0×,
decode NOT the bottleneck**. DataLoader `num_workers=2` recovers +25%
samples/s on the serial loop. Full numbers: `benchmark-notes.md`.

## Layout

```
ai/
├── README.md            ← this file
├── benchmark-notes.md   ← measured numbers + verdicts (append as you run)
├── Makefile             ← builds libmic_pics.{dylib,so} from ../ojph/*.c
├── mic_loader.py        ← ctypes wrapper over mic_decompress_parallel
├── mic_dataset.py       ← PICSDataset (torch.utils.data.Dataset)
├── benchmark_feed.py    ← raw-vs-MIC GPU-feed benchmark (MPS/CUDA/CPU)
└── tests/
    ├── test_abi.py          ← loads dylib, PICS-8 blob → bit-exact round-trip
    ├── test_mic_loader.py   ← loader-level correctness
    └── test_mic_dataset.py  ← Dataset dtype/shape/len
```