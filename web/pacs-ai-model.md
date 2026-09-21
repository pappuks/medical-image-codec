# PACS AI model — brain MRI U-Net (demo, not for clinical use)

The `?ai=1` benchmark section runs a real, pretrained segmentation model in
the browser via `onnxruntime-web`. This document records what it is, where it
came from, and what it is **not**.

## ⚠️ Not for clinical use

This model is **not FDA-cleared, CE-marked, or clinically validated** for any
diagnostic purpose. It exists to (a) prove the end-to-end browser AI pipeline
(MIC decode → preprocess → inference → overlay) and (b) produce honest
latency numbers. Output masks are not medical advice.

## Model

| | |
|---|---|
| Architecture | U-Net, 4 levels, batch-norm, `init_features=32` |
| Task | FLAIR abnormality segmentation in brain MRI |
| Training data | LGG MRI segmentation dataset (Kaggle `lgg-mri-segmentation`) |
| Parameters | 7,763,041 |
| Source | [`mateuszbuda/brain-segmentation-pytorch`](https://github.com/mateuszbuda/brain-segmentation-pytorch) |
| License | **MIT** (verified via GitHub API, 2026-08-27) |
| Artifact | `web/models/brain-segmentation-unet.onnx` (31.1 MB, opset 17, self-contained) |

Pretrained weights are loaded via `torch.hub` from the author's GitHub
release (`unet-e012d006.pt`); the model was **not** trained or fine-tuned in
this repo.

## I/O contract

- **Input:** `input`, float32 `[batch, 3, 256, 256]`, values in `[0, 1]`.
  The model was trained on 3-channel FLAIR slices with per-volume
  normalization; the browser preprocessor **triplicates the grayscale MIC
  slice into 3 channels**, resizes to 256×256 (bilinear), and maps the
  16-bit dynamic range into `[0,1]` via window/level or min-max.
- **Output:** `output`, float32 `[batch, 1, 256, 256]` — sigmoid abnormality
  probability map in `[0,1]`. The overlay threshold is ≥ 0.5.

## Export path (reproduce)

```python
import torch
model = torch.hub.load('mateuszbuda/brain-segmentation-pytorch', 'unet',
                       in_channels=3, out_channels=1, init_features=32,
                       pretrained=True, trust_repo=True)
model.eval()
dummy = torch.randn(1, 3, 256, 256)
torch.onnx.export(model, dummy, 'web/models/brain-segmentation-unet.onnx',
                  input_names=['input'], output_names=['output'],
                  dynamic_axes={'input': {0: 'batch'}, 'output': {0: 'batch'}},
                  opset_version=17, do_constant_folding=True)
# torch 2.13 may emit external weights (.onnx.data) — inline them:
import onnx
m = onnx.load('web/models/brain-segmentation-unet.onnx', load_external_data=True)
for t in m.graph.initializer: t.ClearField('data_location')
onnx.save(m, 'web/models/brain-segmentation-unet.onnx')
```

Export fidelity: max abs diff torch→onnxruntime **1.28e-07**.

## Smoke test

```bash
cd web && node scripts/probe-onnx.mjs
# PASS: brain U-Net forward pass returns [1,1,256,256]
# (wasm backend, ~150 ms on Apple M-series; WebGPU in-browser is faster)
```

## Fallback models (if this contract proves awkward)

- `ConstantinSeibold/ChestXRayAnatomySegmentation` — chest X-ray anatomy
  segmentation (PyPI + HuggingFace demo)
- MONAI model zoo bundles (many ship ONNX directly)