# MIC → AI Pipeline — GPU-feed adapter (PyTorch) and in-browser inference (ONNX/WebGPU)

MIC as an AI data-plane codec, proven with real measurements on two fronts:

- **Part A — training ingest:** a PyTorch `Dataset`/`DataLoader` that decodes
  MIC PICS blobs via the **C PICS-8 decoder** (the paper's fastest decoder)
  and feeds the GPU. Verdict: decode is **4.7–9.0× the device consume rate —
  decode never starves the GPU** (MPS, Apple Silicon; CUDA runbook included).
- **Part B — edge inference:** the `?ai=1` dashboard mode runs
  **MIC decode → preprocess → ONNX inference** entirely in the browser
  (PHI never leaves the device). PICS-C-WASM-8 decodes CR (7.2 MB) in
  **~4 ms** — 31× faster than pure-JS — nearly halving end-to-end AI
  pipeline latency vs the alternatives (289 ms → 157 ms).

Positioning: compete on **speed + random access + losslessness**, not raw
ratio. The codec's job is the data plane between archive and GPU/browser.

Status: **implemented and measured** (2026-08-27). Design/plan source:
`.hermes/plans/2026-08-27_mic-ai-pipeline.md`. Measured numbers live in
[`../ai/benchmark-notes.md`](../ai/benchmark-notes.md).

---

## Part A — PyTorch adapter over the C PICS-8 decoder

### Architecture decisions (why it's built this way)

1. **Decode via the existing C PICS-8 decoder, no new codec code.**
   `ojph/mic_parallel.c` exposes `mic_decompress_parallel()` — the same C
   implementation benchmarked in the paper (Table 4/5: fastest decoder on
   every image, 3.2–4.3 GB/s on large images). Built into a shared library
   and called from Python via `ctypes`. No Python/Go re-implementation
   exists anywhere in the pipeline — bit-exactness is guaranteed by
   construction, not by porting.
   - Rejected: pure-Python decode (drift risk), subprocess-per-sample
     `mic-compress` (fork+IO defeats a throughput benchmark), Go
     `-buildmode=c-shared` (unnecessary — the C ABI is already clean and
     faster).
2. **Corpus = `web/testdata/` PICS blobs + checksum-verified ground truth.**
   `go run ./cmd/mic-compress -testdata` emits PICS variants
   (`<NAME>_pics8.mic` for large images, `_pics4.mic` for small ones) plus
   `manifest.json` with per-image `fnv1a32` pixel checksums. Ground-truth
   raw bins under `testdata/` are matched to manifest checksums so the
   pairing is proven, not assumed.
   - **The C decoder reads PICS blobs, not plain MIC1.** Never feed a
     MIC1 file to `mic_decompress_parallel`.
3. **Dual GPU backend.** `--device {mps,cuda,cpu}` (auto: CUDA → MPS →
   CPU). MPS on Apple Silicon; CUDA on the Linux GPU box. `torch.cuda.Event`
   on CUDA; host-side `time.perf_counter` elsewhere (MPS has no cross-stream
   event API).

### Files

```
ai/
├── README.md            decisions + CUDA machine runbook + measured reference
├── benchmark-notes.md   measured numbers + verdicts (append-only)
├── Makefile             builds libmic_pics.{dylib,so} from ../ojph/*.c
├── mic_loader.py        ctypes wrapper over mic_decompress_parallel
├── mic_dataset.py       PICSDataset / RawDataset / DataLoader plumbing
├── benchmark_feed.py    raw-vs-MIC GPU-feed benchmark (MPS/CUDA/CPU)
└── tests/               22 tests, all bit-exact vs ground truth
```

### Running

```bash
# build the shared lib (macOS → .dylib, Linux → .so)
make -C ai

# tests — 22 passing; decode verified bit-exact against ground truth
.venv/bin/python -m pytest ai/tests/ -v

# benchmark: headroom verdict + worker sweep
.venv/bin/python ai/benchmark_feed.py --device mps --iterations 30 --threads 8
.venv/bin/python ai/benchmark_feed.py --device auto --iterations 30 --workers -1
```

### Measured results (MPS, Apple Silicon, torch 2.13.0, 2026-08-27)

```
config    samples/s  GB/s fed  xfer ms compute ms  loop ms
raw           439.5      2.34      3.2        6.1     13.7
mic           108.2      0.95      5.7       34.4     64.7

isolated C PICS decode (CR_pics8.mic): 1.63–3.18 GB/s (run variance)
device consume rate (compute-only):    0.35 GB/s
```

**Verdict: headroom 4.7–9.0× → decode is NOT the bottleneck.**
The serial-loop gap (MIC 4.7× raw) is a DataLoader-overlap problem, not a
decode-speed problem: `num_workers=2` recovers +25% samples/s (138→172).
Full numbers + run variance notes: `ai/benchmark-notes.md`.

Metrics definitions (they matter for honesty):
- `consume_gbps` — bytes the device processes per second of **pure compute**
  (excludes transfer + decode). This is the headroom baseline.
- `gbps_fed` — loop-inclusive bytes/s (transfer + decode + compute).
- `headroom` = isolated decode GB/s ÷ consume GB/s. ≥2.0 → "NOT the
  bottleneck", 1.0–2.0 → "MARGINAL", <1.0 → "BOTTLENECK".

### CUDA machine (Linux + NVIDIA GPU)

```bash
make                                        # libmic_pics.so
.venv/bin/pip install torch --index-url https://download.pytorch.org/whl/cu126
.venv/bin/python -m pytest ai/tests/ -v     # bit-exact round-trips
.venv/bin/python ai/benchmark_feed.py --device cuda --iterations 30 --threads 8 --workers -1
```

---

## Part B — In-browser AI inference (`?ai=1`)

### Architecture decisions

1. **Reuse the codec-adapter contract, not a parallel harness.**
   `runAIInference()` in `web/pacs-runner.mjs` reuses the runner's
   fetch/resolve machinery and the same warmup+median timing discipline as
   the codec tables (`timeMedian` for decode, `timeMedianAsync` for
   inference). Decode timing in the AI section is identical to the codec
   benchmark's — numbers are comparable.
2. **Run the live codec set, PICS-C-WASM-8 first.**
   `AI_DEFAULT_CODEC_IDS = ['pics-c-wasm-8', 'mic-4state', 'htj2k', 'jpegls']`
   — the pipeline numbers must reflect decode time (the demo's point).
   PICS blobs ship as `_pics8` (large images) or `_pics4` (small); the AI
   stage falls back to the alternate strip-count variant and records a ⚠
   note rather than silently skipping.
3. **Lazy-load onnxruntime-web.** The plain decode dashboard never pays for
   the runtime: the adapter `import()`s ort only when `?ai=1` runs.
4. **No bundler — vendor the ort bundle.** The repo serves raw ES modules,
   so `import('onnxruntime-web')` cannot resolve in a browser.
   `web/scripts/vendor-onnx.sh` copies the fully self-contained
   `ort.all.bundle.min.mjs` (+ jsep/plain `.wasm` binaries) into
   `web/vendor/onnx/`, mirroring the OpenJPH/CharLS vendor pattern.

### The model — real, pretrained, MIT; not for clinical use

`web/models/brain-segmentation-unet.onnx` (31 MB, opset 17, self-contained):

| | |
|---|---|
| Architecture | U-Net, 4 levels, batch-norm, init_features=32 |
| Task | FLAIR abnormality segmentation in brain MRI |
| Training data | LGG MRI segmentation (Kaggle `lgg-mri-segmentation`) |
| Params | 7,763,041 |
| Source | `mateuszbuda/brain-segmentation-pytorch` (GitHub; MIT — verified via API) |
| Export fidelity | max abs diff torch→onnxruntime **1.28e-07** |
| I/O contract | `input` float32 `[B,3,256,256]` in [0,1] → `output` `[B,1,256,256]` sigmoid map |
| Preprocessing | grayscale uint16 → min-max [0,1] → **triplicated to 3 channels** → box-resize 256×256 |

Full provenance, export recipe, and caveats:
[`../web/pacs-ai-model.md`](../web/pacs-ai-model.md). The model is a real
pretrained network, but **not FDA-cleared / clinically validated** — it
exists to prove the pipeline and produce honest latency numbers.

### Measured results (headless Chromium, WASM backend)

Quick image set, decode+infer per (image, codec):

| codec (CR 7.2 MB) | decode | AI pipeline |
|---|---|---|
| **PICS-C-WASM-8** | **4.3 ms** | **157 ms** |
| JPEG-LS | 72 ms | 230 ms |
| HTJ2K | 122 ms | 278 ms |
| MIC-4state (pure JS) | 134 ms | 289 ms |

- PICS-C-WASM-8 decodes **31× faster** than pure-JS and nearly halves
  end-to-end AI pipeline latency on large images.
- On 512² slices all codecs converge (0.3–3 ms decode vs ~150 ms WASM
  inference) — the regime where **WebGPU** inference (headful Chrome/Edge)
  makes decode time matter again. CI/headless runs report the WASM backend;
  don't quote them as WebGPU numbers.

### Running

```bash
# vendor ort (one-time, after npm install)
cd web && npm install && bash scripts/vendor-onnx.sh

# Node smoke test (WASM backend; asserts [1,3,256,256] -> [1,1,256,256])
cd web && node scripts/probe-onnx.mjs

# interactive: AI section
cd web && python3 serve.py 8080
# open http://localhost:8080/pacs-dashboard.html?ai=1  → Start benchmark

# headless end-to-end
cd web && npx playwright test tests/pacs-ai.spec.mjs
```

### WebGPU / deployment notes

- WebGPU needs the same cross-origin isolation the demo already ships
  (COOP/COEP in `serve.json` and `infra/template.yaml`) — no new header work.
- Node has no WebGPU: Playwright/CI exercises the WASM fallback; WebGPU
  numbers require a real headful Chrome/Edge. Don't quote CI numbers as
  WebGPU.
- Deploying to the live demo (B4, not yet done): upload
  `web/models/brain-segmentation-unet.onnx` + `web/vendor/onnx/*` to the app
  bucket; the `?ai=1` mode is already wired and works identically against the
  S3 study source.

---

## Cross-cutting lessons (apply to future work)

- **Never reimplement the codec in another language.** Go↔C↔JS↔WASM↔Python
  all wrap one implementation per target from the same sources; the tests
  verify bit-exactness against the Go reference at every boundary.
- **Verify against checksummed ground truth, not "it ran".** The
  `fnv1a32` manifest checksums (`web/testdata/manifest.json`) are the
  canonical pixel ground truth; Part A's Python tests and Part B's browser
  verification both key off them.
- **Honest metrics need explicit definitions.** `consume_gbps` (compute-only)
  vs `gbps_fed` (loop-inclusive) vs the paper's in-process MB/s answer three
  different questions; label every published number with which one it is.
- **Small images ship `_pics4`, large ship `_pics8`** (one PICS variant per
  image in `web/testdata`); any consumer must handle both or fall back with
  a recorded note.