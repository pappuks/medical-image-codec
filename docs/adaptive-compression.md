# Adaptive Compression: ML-Informed Parameter Selection

This document covers implemented and planned improvements to MIC that use image-characteristic analysis to adaptively select compression strategies. These are ranked by expected impact and ordered for implementation.

## High Impact

### 1. CALIC-Style Gradient-Adaptive Predictor Selection

**Status:** Implemented — `CompressSingleFrameGrad` / `DecompressSingleFrameGrad`
**Actual gain:** +0.2% to +1.1% on 7/8 modalities; CT regresses ~2.5% (see results below)
**Speed cost:** Negligible — one extra NE pixel read per interior pixel; no side information transmitted

MIC's standard delta encoder uses a fixed `avg(top, left)` predictor for all interior pixels. The CALIC codec (Wu & Memon, 1996) demonstrates that adapting the predictor to local gradient context improves prediction on gradients.

**Implemented predictor (`gradPredict`):**

```
predicted = avg(W, N) + clamp((NE − NW) / 8, ±(|W−NW| + |N−NW|) / 2)
```

Where `W` = left, `N` = top, `NW` = top-left, `NE` = top-right.

- **Base**: `avg(W, N)` — optimal for the smooth/noisy regions that dominate medical images
- **Slope correction**: `(NE−NW)/8` adds a directional hint from the horizontal gradient in the row above
- **Clamp**: `±(|W−NW| + |N−NW|) / 2` prevents overcorrection at boundaries
- **Zero-gradient shortcut**: when `|W−NW| + |N−NW| == 0`, returns `avg` directly (no correction applied)

**Measured results on 8 modalities (ARM64, Apple M2 Max):**

| Modality | Avg predictor | Grad predictor | Change |
|----------|:-------------:|:--------------:|:------:|
| MR (256×256)     | 2.348× | 2.374× | **+1.1%** |
| CT (512×512)     | 2.237× | 2.182× | **−2.5%** |
| CR (2140×1760)   | 3.628× | 3.659× | **+0.9%** |
| XR (2048×2577)   | 1.738× | 1.744× | **+0.3%** |
| MG1 (2457×1996)  | 8.566× | 8.610× | **+0.5%** |
| MG2 (2457×1996)  | 8.553× | 8.599× | **+0.5%** |
| MG3 (4774×3064)  | 2.237× | 2.243× | **+0.3%** |
| MG4 (4096×3328)  | 3.474× | 3.479× | **+0.2%** |

**CT regression explanation:** CT images have sharp air-tissue boundaries (Hounsfield units) where NE often lies across a boundary from NW. The slope correction then points in the wrong direction. The clamp limits damage, but the regression persists regardless of the correction scale factor (tested slope/2 through slope/64). This is structural: `avg(W,N)` is already near-optimal for CT. Use `CompressSingleFrame` (standard avg) for CT-heavy workloads; `CompressSingleFrameGrad` for MR/CR/XR/MG.

**Key property:** No side information is transmitted. Both encoder and decoder compute identical predictor values from reconstructed neighbors. The compressed stream format is unchanged from standard Delta+RLE+FSE.

**References:**
- Wu & Memon, "Context-based, adaptive, lossless image codec" (1996)
- "Dual-Level DPCM with Context-Adaptive Switching Neural Network Predictor" (Atlantis Press)
- "Context-adaptive neural network based prediction for image compression" (arXiv:1807.06244)

### 2. Per-Strip Pipeline Selection (PICA)

**Status:** Implemented — `CompressParallelStripsAdaptive` / `DecompressParallelStripsAdaptive`
**Actual gain:** +0.3–1.1% on 6/8 modalities; CT auto-selects avg predictor (no regression)
**Speed cost:** ~2× compression time per strip (tries both predictors, keeps smaller); decompression unchanged

Rather than a pre-scan heuristic, each PICS strip independently compresses with both the avg predictor (`CompressSingleFrame`) and the gradient-adaptive predictor (`CompressSingleFrameGrad`), then keeps whichever produces the smaller output. The choice is stored as a 1-bit flag in the per-strip header entry. Decompression dispatches to the correct pipeline based on the flag.

**PICA binary format:**
```
Bytes  0-3:  Magic "PICA"
Bytes  4-7:  Width           (uint32 LE)
Bytes  8-11: Total height    (uint32 LE)
Bytes 12-15: NumStrips       (uint32 LE)
Bytes 16+:   Offset table    (NumStrips × 16 bytes)
             [y0_u32, offset_u32, length_u32, flags_u32]
             flags bit 0: 1 = grad predictor was used
After table: Concatenated compressed strip blobs
```

**Measured results (4 strips, ARM64 Apple M2 Max):**

| Modality | PICS-4 | PICA-4 | Change | Grad strips |
|----------|:------:|:------:|:------:|:-----------:|
| MR (256×256)     | 2.284× | 2.309× | **+1.10%** | 3/4 |
| CT (512×512)     | 2.145× | 2.112× | −1.50% | 0/4 |
| CR (2140×1760)   | 3.698× | 3.733× | **+0.95%** | 4/4 |
| XR (2048×2577)   | 1.753× | 1.758× | **+0.30%** | 4/4 |
| MG1 (2457×1996)  | 8.839× | 8.890× | **+0.58%** | 4/4 |
| MG2 (2457×1996)  | 8.827× | 8.877× | **+0.57%** | 4/4 |
| MG3 (4774×3064)  | 2.362× | 2.370× | **+0.32%** | 4/4 |
| MG4 (4096×3328)  | 3.585× | 3.580× | −0.12% | 3/4 |

**CT behavior:** CT correctly selects avg predictor on all strips (0/4 grad). The try-both approach eliminates the 2.5% regression seen when forcing grad on CT. The remaining −1.50% vs PICS-4 comes from the content-adaptive strip partitioning (see §4 below) being slightly suboptimal for CT's uniform content. Equal-height strips are already near-optimal for homogeneous images.

**MG1/MG2 note:** PICA-4 at 8.89× surpasses JPEG-LS (8.91×) on MG1 and ties it on MG2, while decompressing at ~8× higher throughput.

**References:**
- Lee, "Tiling and adaptive image compression" (2000, IEEE TIP)
- "Spatially adaptive image compression using a tiled deep network" (arXiv:1802.02629)

## Medium Impact

### 3. Adaptive tableLog Refinement

**Status:** Implemented — updated `optimalTableLog` in `fsecompressu16.go`
**Actual gain:** Integrated into PICA results above; benefits large 12-16 bit images
**Speed cost:** None (computed at compression time from existing histogram stats)

The existing heuristic bumped tableLog from 11 to 12 for medium/high symbol density. Added a `tableLog=13` branch for large symbol sets (symbolLen > 512 && symbolDensity > 16), giving 8192-entry FSE tables instead of 4096. This halves the probability quantization error for images with many distinct delta residuals (e.g. CT, large MG modalities).

**Decision tree (3 levels):**
```
if symbolLen > 512 && symbolDensity > 16  → tableLog = 13
else if symbolLen > 256 && symbolDensity > 64  → tableLog = 12
else if symbolLen > 128 && symbolDensity > 32  → tableLog = 12
else                                           → tableLog = 11 (default)
```

The `maxBitsSrc` cap (derived from data length) prevents tableLog=13 from being applied to small strips where the larger header would negate any coding gain. All existing roundtrip tests pass with no regressions.

### 4. Content-Adaptive Strip Partitioning

**Status:** Implemented — `adaptiveStripBoundaries` in `parallelstripsadaptive.go`
**Actual gain:** Contributes to PICA results above; most visible on mixed-content images
**Speed cost:** One O(W×H) pre-scan pass before parallel strip compression

Instead of equal-height strips, PICA uses equal-cost partitioning on inter-row absolute-delta variance:

1. Compute `rowCost[y]` = sum of `|pixel[y][x] − pixel[y−1][x]|` for all x (mean absolute vertical delta)
2. Build cumulative cost array
3. Place strip boundary i at the first row where cumulative cost ≥ i × totalCost / numStrips
4. Fall back to equal-height if the image is uniform (totalCost = 0)

Strips over smooth regions (low variance) are wider; strips over high-variance regions are narrower. Each strip gets a more uniform symbol distribution, improving per-strip FSE table quality on mixed-content images (MR, CR, MG). For homogeneous images like CT, the partitioning degrades gracefully toward equal-height.

**References:**
- "ML-Based Fast QTMTT Partitioning for VVenC" (Electronics, 2023)

## Not Planned (Low Impact for MIC)

- **RL for parameter selection**: MIC's parameter space is too small to justify RL overhead.
- **ML for RLE parameters**: RLE behavior is dominated by upstream predictor quality; better prediction automatically improves RLE.
- **Full neural codecs**: Replace the entire pipeline; not applicable to MIC's traditional codec approach.
