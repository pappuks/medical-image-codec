# MIC Documentation Index

Architecture and design documentation for MIC (Medical Image Codec). Grouped by
topic; see the root [README](../README.md) and [CLAUDE.md](../CLAUDE.md) for the
project overview and build/test commands.

## Codec architecture & internals

| Doc | What it covers |
|-----|----------------|
| [architecture.md](architecture.md) | MIC architecture reference — the Delta + RLE + FSE/Huffman pipeline, container formats (MIC1/MIC2/MIC3/MICR), key source files |
| [developer-guide.md](developer-guide.md) | Developer guide — working in the codebase, conventions, extension points |
| [16bit-alphabet-entropy-coding.md](16bit-alphabet-entropy-coding.md) | 16-bit alphabet entropy coding for medical images (FSE/ANS over the full 16-bit symbol range) |
| [native-optimizations.md](native-optimizations.md) | Go assembly, 2-state & 4-state FSE, and other native-speed optimizations |
| [parallel-strips.md](parallel-strips.md) | PICS — parallel single-image compression format and threading model |
| [adaptive-compression.md](adaptive-compression.md) | ML-informed adaptive parameter selection |

## Wavelet pipeline

| Doc | What it covers |
|-----|----------------|
| [wavelet-fse-analysis.md](wavelet-fse-analysis.md) | 5/3 integer wavelet + FSE pipeline: implementation and analysis |
| [wavelet-simd.md](wavelet-simd.md) | SIMD/AVX2-accelerated wavelet transform |

## Whole-slide imaging (WSI / RGB)

| Doc | What it covers |
|-----|----------------|
| [wsi-codec-plan.md](wsi-codec-plan.md) | WSI codec extension plan — MIC3 tiled format, YCoCg-R, pyramid levels |

## Results & benchmarks

| Doc | What it covers |
|-----|----------------|
| [benchmarks.md](benchmarks.md) | Canonical benchmark inventory, methodology, and the browser PACS-viewer benchmark |
| [compression-results.md](compression-results.md) | Compression ratio results across modalities |
| [htj2k-comparison.md](htj2k-comparison.md) | Fair in-process comparison vs HTJ2K (OpenJPH) |
| [jpegls-comparison.md](jpegls-comparison.md) | Fair in-process comparison vs JPEG-LS (CharLS) |
| [jxl-comparison.md](jxl-comparison.md) | Fair in-process comparison vs JPEG-XL (libjxl) |

## PACS web viewer & online demo

The dataset pipeline and the public-demo hosting architecture.

| Doc | What it covers |
|-----|----------------|
| [pacs-demo-roadmap.md](pacs-demo-roadmap.md) | **Roadmap / TODO** — current state, the Lambda/online-demo tasks, open bugs and coverage gaps. Start here. |
| [pacs-s3-datasets.md](pacs-s3-datasets.md) | Dataset catalog — sources, licenses, Tier A/B (lossless vs lossy), the IDC selection |
| [pacs-encode-design.md](pacs-encode-design.md) | Batch all-codec encoder design — routing, verification, manifest schema |
| [pacs-lambda-service-design.md](pacs-lambda-service-design.md) | **Online demo hosting** — CloudFront + Lambda + private S3, cross-origin-isolation strategy, IAM, cost, IaC |
| [pacs-access-control-design.md](pacs-access-control-design.md) | **Bot mitigation** — WAF Challenge, token minting order, the 202 failure mode, COEP interaction; identity options in the appendix |

## AI pipeline (MIC as an AI data plane)

MIC feeding GPU training and in-browser inference — architecture, measured
verdicts, and runbooks.

| Doc | What it covers |
|-----|----------------|
| [ai-pipeline.md](ai-pipeline.md) | **MIC → AI pipeline** — Part A: PyTorch adapter over the C PICS-8 decoder (MPS/CUDA benchmark, headroom verdict); Part B: in-browser ONNX/WebGPU inference (`?ai=1`), the brain-U-Net model, codec comparison. Start here. |

Related tooling READMEs outside `docs/`:
[`infra/README.md`](../infra/README.md) (AWS hosting stack — SAM/CloudFront/Lambda/WAF) ·
[scripts/pacs-ingest/README.md](../scripts/pacs-ingest/README.md) (ingest/encode/upload pipeline) ·
[scripts/aws/README.md](../scripts/aws/README.md) (EC2 benchmark runbook) ·
[web/README.md](../web/README.md) (dashboard + Node/Playwright benchmark) ·
[ai/README.md](../ai/README.md) (PyTorch adapter + GPU-feed benchmark) ·
[web/pacs-ai-model.md](../web/pacs-ai-model.md) (AI model provenance + license).
