# Benchmark notes — MIC→AI GPU feed

Append-only log of measured numbers. Never edit old entries; add a new
dated section instead. Hardware + flags must accompany every number.

## 2026-08-27 — setup verification (Apple M-series, macOS)

- `ojph/mic_decompress_c.c` + `ojph/mic_parallel.c` compile standalone
  (`gcc -O3 -fPIC`) and link into `libmic_pics.dylib`;
  `nm -gU` shows `mic_decompress_parallel` exported.
- Ground-truth mapping (fnv1a32-verified): CR/CT/MR/MG1/MG2/DX_HAND/PET1
  have local bins; MG3 + CINE frames verify via manifest checksum only.

*(numbers below are appended as A4 runs)*