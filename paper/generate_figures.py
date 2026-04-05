#!/usr/bin/env python3
"""
Generate all publication figures for the MIC paper.

Figures produced (in paper/figures/):
  fig1_pipeline.png          – Compression pipeline block diagram
  fig2_histogram.png         – Delta-residual distributions for 5 modalities
  fig3_multistate_speedup.png – 1/2/4-state FSE speedup bar chart (21 images)
  fig4_pareto.png            – Compression ratio vs. decompression throughput
  fig5_predictor_ablation.png – Per-image predictor ratio comparison
  fig6_tablelog_ablation.png  – TableLog ablation gain heatmap

Run from the repo root:
    .venv/bin/python3 paper/generate_figures.py
"""

import csv
import os
import math
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.patches as mpatches
import matplotlib.ticker as ticker
import numpy as np
from matplotlib.gridspec import GridSpec

OUT_DIR = "paper/figures"
os.makedirs(OUT_DIR, exist_ok=True)

# ── shared style ──────────────────────────────────────────────────────────────
plt.rcParams.update({
    "font.family": "serif",
    "font.size": 9,
    "axes.titlesize": 10,
    "axes.labelsize": 9,
    "xtick.labelsize": 8,
    "ytick.labelsize": 8,
    "legend.fontsize": 8,
    "figure.dpi": 150,
    "savefig.dpi": 300,
    "savefig.bbox": "tight",
    "axes.spines.top": False,
    "axes.spines.right": False,
})

BLUE   = "#2563EB"
ORANGE = "#EA580C"
GREEN  = "#16A34A"
PURPLE = "#7C3AED"
GREY   = "#6B7280"
RED    = "#DC2626"
TEAL   = "#0D9488"
GOLD   = "#D97706"

# ── data ─────────────────────────────────────────────────────────────────────

IMAGES = [
    "MR","CT","CR","XR",
    "MG1","MG2","MG3","MG4",
    "CT1","CT2","MG-N",
    "MR1","MR2","MR3","MR4",
    "NM1","RG1","RG2","RG3","SC1","XA1",
]

# Compression ratios — Table VIII of the paper
RATIO = {
    "Δ+Zstd-19": [1.95,2.05,3.24,1.43,7.20,7.21,2.04,3.10,2.47,3.24,2.04,1.79,2.77,3.47,3.58,4.88,1.45,3.68,5.46,3.46,4.37],
    "MIC":       [2.35,2.24,3.69,1.74,8.79,8.77,2.24,3.47,2.79,3.49,2.24,2.09,3.28,3.93,4.12,5.15,1.70,4.23,6.08,3.71,5.01],
    "Wavelet":   [2.38,1.67,3.81,1.76,8.67,8.65,2.32,3.59,2.49,2.87,2.32,2.14,3.34,4.09,4.18,5.02,1.70,4.32,6.82,3.70,4.94],
    "HTJ2K":     [2.38,1.77,3.77,1.67,8.25,8.24,2.22,3.51,2.70,3.29,2.23,2.13,3.35,4.33,4.21,5.76,1.63,4.32,6.99,3.85,4.88],
    "JPEG-LS":   [2.52,2.68,3.96,1.76,8.91,8.90,2.38,3.71,3.19,4.54,2.38,2.30,3.52,4.51,4.49,6.28,1.72,4.51,7.31,4.73,5.39],
}

# ARM64 decompression throughput (MB/s) — Table X of the paper
DECOMP = {
    "MIC-Go":       [144,191,296,308,482,479,308,417,239,238,316,278,333,375,316,327,235,367,374,375,331],
    "MIC-4s-C":     [348,356,524,533,683,686,531,625,436,439,536,521,563,639,571,632,406,590,604,587,576],
    "Wavelet+SIMD": [248,316,567,627,678,697,422,516,425,481,468,435,498,507,479,575,584,644,656,388,459],
    "HTJ2K":        [265,307,367,334,810,790,338,548,362,375,340,325,388,441,406,410,332,443,562,401,419],
    "JPEG-LS":      [102,137,153,108,409,416,153,184,182,175,153,116,172,236,197,210,104,193,246,229,204],
    "PICS-C-4":     [710,955,1635,1666,2112,2120,1673,2004,1013,1041,1711,1207,1552,1430,1341,1400,1128,1803,1944,1861,1583],
    "PICS-C-8":     [482,1092,2661,3025,3656,3773,3117,3689,1183,1189,3175,1402,2466,1614,1558,1679,2017,3194,3302,3279,2493],
}

# Isolated FSE benchmark (pure Go, ARM64) — BenchmarkFSEDecompress4State
FSE = {
    "1-state": [514,829,140,1444,1140,743,1453,2633,982,1108,2944,1562,2306,1472,1779,1658,1676,2432,2143,1997,2005],
    "2-state": [689,995,1044,1774,1746,1064,4115,3707,1241,1092,4138,2027,2867,1582,1973,1898,2652,3409,2995,2788,2348],
    "4-state": [841,1295,966,1952,1873,1319,5462,5287,1572,1434,5696,2591,3521,1841,2389,2254,3128,3810,3587,2927,2639],
}

# Predictor ablation — TestPredictorAblation
PRED = {
    "Left-only": [2.206,2.309,3.512,1.696,8.560,8.546,2.287,3.353,2.744,3.258,2.271,1.991,3.024,3.827,3.854,4.901,1.630,4.110,5.506,3.818,4.858],
    "Avg (MIC)": [2.353,2.238,3.693,1.738,8.786,8.774,2.237,3.474,2.790,3.486,2.239,2.088,3.283,3.927,4.125,5.149,1.698,4.231,6.079,3.714,5.010],
    "Paeth":     [2.324,2.286,3.587,1.729,8.595,8.584,2.282,3.392,2.900,3.600,2.165,2.093,3.311,4.263,4.301,4.718,1.683,4.138,6.588,4.053,4.872],
    "MED":       [2.364,2.306,3.632,1.734,8.690,8.678,2.302,3.415,2.949,3.697,2.188,2.118,3.337,4.328,4.364,4.751,1.698,4.164,6.671,4.104,4.941],
}

# TableLog ablation — TestTableLogAblation
TL = {
    "TL=11":   [2.348,2.238,3.474,1.738,7.995,7.984,2.237,3.474,2.790,3.486,2.239,2.088,3.138,3.894,4.125,5.092,1.698,3.850,5.637,3.714,4.846],
    "TL=12":   [2.348,2.238,3.628,1.738,8.566,8.553,2.237,3.474,2.790,3.486,2.239,2.088,3.240,3.894,4.125,5.149,1.698,4.124,5.952,3.714,5.010],
    "TL=13":   [2.353,2.238,3.693,1.738,8.786,8.774,2.237,3.474,2.790,3.486,2.239,2.088,3.283,3.927,4.125,5.187,1.698,4.231,6.079,3.714,5.060],
    "Adaptive":[2.353,2.238,3.693,1.738,8.786,8.774,2.237,3.474,2.790,3.486,2.239,2.088,3.283,3.927,4.125,5.149,1.698,4.231,6.079,3.714,5.010],
}

def geomean(vals):
    return math.exp(sum(math.log(v) for v in vals) / len(vals))


# ── Figure 1: Pipeline block diagram ─────────────────────────────────────────
def fig1_pipeline():
    # Layout: 5 boxes × bw=1.6 + 4 gaps × 0.4 + 0.2 left + 0.2 right = 10.0 exactly
    BW = 1.6    # box width
    GAP = 0.4   # gap between boxes
    LPAD = 0.2  # left padding

    box_xs = [LPAD + i * (BW + GAP) for i in range(5)]  # [0.2, 2.2, 4.2, 6.2, 8.2]

    fig, ax = plt.subplots(figsize=(8, 2.8))
    ax.set_xlim(0, 10)
    ax.set_ylim(0, 3)
    ax.axis("off")

    boxes = list(zip(box_xs, [
        ("Raw 16-bit\nPixels",      GREY,  "white"),
        ("Delta\nEncoding",         BLUE,  "white"),
        ("16-bit RLE",              TEAL,  "white"),
        ("FSE/ANS\nEntropy Coder",  GREEN, "white"),
        ("Compressed\nBitstream",   GREY,  "white"),
    ]))

    BH, BY = 0.9, 1.05
    for (bx, (label, fc, tc)) in boxes:
        rect = mpatches.FancyBboxPatch(
            (bx, BY), BW, BH,
            boxstyle="round,pad=0.05",
            facecolor=fc, edgecolor="white", linewidth=1.5, zorder=3)
        ax.add_patch(rect)
        ax.text(bx + BW/2, BY + BH/2, label,
                ha="center", va="center", color=tc,
                fontsize=8.5, fontweight="bold", zorder=4)

    # Arrows between boxes
    for i in range(len(boxes) - 1):
        x1 = boxes[i][0] + BW
        x2 = boxes[i+1][0]
        ym = BY + BH / 2
        ax.annotate("", xy=(x2, ym), xytext=(x1, ym),
                    arrowprops=dict(arrowstyle="-|>", color=GREY,
                                   lw=1.5, mutation_scale=12), zorder=5)

    # Annotation labels beneath each processing box (indices 1, 2, 3)
    box_centers = [bx + BW / 2 for (bx, _) in boxes]
    annots = [
        (box_centers[1], "pixel − avg(top,left)"),
        (box_centers[2], "same/diff runs"),
        (box_centers[3], "table-driven\ntANS"),
    ]
    for (ax_x, txt) in annots:
        ax.text(ax_x, BY - 0.35, txt, ha="center", va="top",
                fontsize=7, color=GREY, style="italic")

    ax.set_title("MIC Compression Pipeline", fontsize=11, fontweight="bold", pad=6)
    plt.tight_layout()
    plt.savefig(f"{OUT_DIR}/fig1_pipeline.png")
    plt.close()
    print("fig1_pipeline.png done")


# ── Figure 2: Delta residual histograms ───────────────────────────────────────
def fig2_histogram():
    csv_path = "paper/figures/histogram_data.csv"
    if not os.path.exists(csv_path):
        print("WARNING: histogram_data.csv not found, skipping fig2")
        return

    data = {}
    with open(csv_path) as f:
        for row in csv.DictReader(f):
            img = row["image"]
            r   = int(row["residual"])
            c   = int(row["count"])
            if img not in data:
                data[img] = {}
            data[img][r] = c

    order  = ["MR", "CT", "CR", "MG1", "XA1"]
    colors = [BLUE, ORANGE, GREEN, PURPLE, TEAL]
    labels = {"MR": "MR (brain)", "CT": "CT", "CR": "CR (chest X-ray)",
              "MG1": "MG1 (mammography)", "XA1": "XA1 (fluoroscopy)"}

    fig, axes = plt.subplots(1, len(order), figsize=(10, 2.6), sharey=False)

    for idx, (img, col) in enumerate(zip(order, colors)):
        ax  = axes[idx]
        hist = data.get(img, {})
        xs   = sorted(hist.keys())
        ys   = [hist[x] for x in xs]
        total = sum(ys)

        # Display only |r| ≤ 30 for clarity
        window = 30
        xw = [x for x in xs if abs(x) <= window]
        yw = [hist[x] / total * 100 for x in xw]

        ax.bar(xw, yw, color=col, alpha=0.85, width=1.0, linewidth=0)
        ax.set_xlim(-window - 1, window + 1)
        ax.set_xlabel("Residual value", fontsize=7)
        if idx == 0:
            ax.set_ylabel("Frequency (%)", fontsize=7)
        ax.set_title(labels[img], fontsize=7.5, pad=3)
        ax.tick_params(labelsize=6.5)
        ax.yaxis.set_major_formatter(ticker.FormatStrFormatter("%.1f"))

        # Annotate center peak
        peak = max(yw)
        ax.axvline(0, color="black", lw=0.6, ls="--", alpha=0.5)
        ax.text(0.97, 0.97, f"r=0: {hist.get(0,0)/total*100:.1f}%",
                transform=ax.transAxes, ha="right", va="top",
                fontsize=6.5, color="black")

    fig.suptitle("Delta Residual Distributions After Spatial Prediction", fontsize=10, fontweight="bold")
    plt.tight_layout(rect=[0, 0, 1, 0.93])
    plt.savefig(f"{OUT_DIR}/fig2_histogram.png")
    plt.close()
    print("fig2_histogram.png done")


# ── Figure 3: Multi-state FSE speedup ─────────────────────────────────────────
def fig3_multistate():
    n = len(IMAGES)
    x = np.arange(n)
    w = 0.25

    s1 = np.array(FSE["1-state"], dtype=float)
    s2 = np.array(FSE["2-state"], dtype=float)
    s4 = np.array(FSE["4-state"], dtype=float)

    # Normalise to 1-state = 1.0 per image
    r2 = s2 / s1
    r4 = s4 / s1

    fig, ax = plt.subplots(figsize=(11, 3.2))

    YLIM = 4.5   # cap y-axis; CR 4-state is 6.9× (zeroBits anomaly)

    # Plot bars; clamp display values so clipped bars don't show outside frame
    r2_disp = np.minimum(r2, YLIM)
    r4_disp = np.minimum(r4, YLIM)

    ax.bar(x - w,  [1.0]*n, w, label="1-state", color=GREY,  alpha=0.85)
    ax.bar(x,       r2_disp, w, label="2-state", color=BLUE,  alpha=0.85)
    ax.bar(x + w,   r4_disp, w, label="4-state", color=GREEN, alpha=0.85)

    # Geomean lines
    gm2 = geomean(r2.tolist())
    gm4 = geomean(r4.tolist())
    ax.axhline(gm4, color=GREEN, ls="--", lw=1.0, alpha=0.7,
               label=f"4-state geomean = {gm4:.2f}×")
    ax.axhline(gm2, color=BLUE, ls=":", lw=1.0, alpha=0.7,
               label=f"2-state geomean = {gm2:.2f}×")

    ax.set_xticks(x)
    ax.set_xticklabels(IMAGES, rotation=45, ha="right", fontsize=7.5)
    ax.set_ylabel("Relative throughput (1-state = 1.0)", fontsize=8.5)
    ax.set_title("Isolated FSE Decompression: Multi-State Speedup (ARM64, pure Go)\n"
                 "CR bar clipped at 4.5× — actual 4-state = 6.9× (zeroBits safe-path avoided by multi-state)",
                 fontsize=9, fontweight="bold")
    ax.set_ylim(0, YLIM)
    ax.yaxis.set_minor_locator(ticker.AutoMinorLocator())
    ax.legend(ncol=4, fontsize=7.5, loc="upper right")

    # Annotate clipped CR bars with actual values
    cr_idx = IMAGES.index("CR")
    for offset, val, color in [(0, r2[cr_idx], BLUE), (w, r4[cr_idx], GREEN)]:
        ax.text(cr_idx + offset, YLIM * 0.97, f"{val:.1f}×",
                ha="center", va="top", fontsize=6.5, color=color, fontweight="bold")
    ax.text(cr_idx, YLIM * 0.72, "zeroBits\npath", ha="center",
            fontsize=6.5, color=RED, style="italic")

    plt.tight_layout()
    plt.savefig(f"{OUT_DIR}/fig3_multistate_speedup.png")
    plt.close()
    print("fig3_multistate_speedup.png done")


# ── Figure 4: Pareto plot ────────────────────────────────────────────────────
def fig4_pareto():
    codecs = {
        "MIC-Go":       (RATIO["MIC"],   DECOMP["MIC-Go"],       GREY,   "o",  "MIC-Go (pure Go)"),
        "MIC-4s-C":     (RATIO["MIC"],   DECOMP["MIC-4s-C"],     BLUE,   "s",  "MIC-4state-C"),
        "Wavelet+SIMD": (RATIO["Wavelet"],DECOMP["Wavelet+SIMD"],TEAL,  "D",  "Wavelet V2 SIMD"),
        "HTJ2K":        (RATIO["HTJ2K"], DECOMP["HTJ2K"],         ORANGE, "^",  "HTJ2K (OpenJPH)"),
        "JPEG-LS":      (RATIO["JPEG-LS"],DECOMP["JPEG-LS"],      RED,    "v",  "JPEG-LS (CharLS)"),
        "PICS-C-4":     (RATIO["MIC"],   DECOMP["PICS-C-4"],      GREEN,  "P",  "PICS-C-4 (4 threads)"),
        "PICS-C-8":     (RATIO["MIC"],   DECOMP["PICS-C-8"],      PURPLE, "*",  "PICS-C-8 (8 threads)"),
    }

    fig, ax = plt.subplots(figsize=(7, 4.5))

    for key, (ratios, speeds, color, marker, label) in codecs.items():
        # Per-image scatter (small, transparent)
        ax.scatter(speeds, ratios, color=color, marker=marker,
                   s=12, alpha=0.25, zorder=3)
        # Geomean point (large, opaque)
        gm_r = geomean(ratios)
        gm_s = geomean(speeds)
        ax.scatter([gm_s], [gm_r], color=color, marker=marker,
                   s=80, zorder=5, linewidths=0.8,
                   edgecolors="white" if color != "white" else "black",
                   label=f"{label}  ({gm_s:.0f} MB/s, {gm_r:.2f}×)")

    ax.set_xscale("log")
    ax.set_xlabel("Decompression throughput (MB/s, log scale)", fontsize=9)
    ax.set_ylabel("Compression ratio (×)", fontsize=9)
    ax.set_title("Compression Ratio vs. Decompression Throughput — ARM64 Apple M2 Max\n"
                 "(large marker = geomean; small dots = individual images)",
                 fontsize=9, fontweight="bold")
    ax.xaxis.set_major_formatter(ticker.FuncFormatter(lambda v, _: f"{int(v):,}"))
    ax.legend(fontsize=7.5, loc="upper left", framealpha=0.9)
    ax.grid(True, which="major", ls=":", alpha=0.4)
    ax.grid(True, which="minor", ls=":", alpha=0.15)

    plt.tight_layout()
    plt.savefig(f"{OUT_DIR}/fig4_pareto.png")
    plt.close()
    print("fig4_pareto.png done")


# ── Figure 5: Predictor ablation ─────────────────────────────────────────────
def fig5_predictor():
    avg  = np.array(PRED["Avg (MIC)"])
    left = (np.array(PRED["Left-only"]) - avg) / avg * 100
    pae  = (np.array(PRED["Paeth"])     - avg) / avg * 100
    med  = (np.array(PRED["MED"])       - avg) / avg * 100

    n = len(IMAGES)
    x = np.arange(n)
    w = 0.26

    fig, ax = plt.subplots(figsize=(11, 3.5))

    ax.bar(x - w,     left, w, label="Left-only",    color=ORANGE, alpha=0.85)
    ax.bar(x,         pae,  w, label="Paeth",         color=GREEN,  alpha=0.85)
    ax.bar(x + w,     med,  w, label="MED (JPEG-LS)", color=PURPLE, alpha=0.85)

    ax.axhline(0, color="black", lw=0.8)

    # Geomean deltas
    gm_pae = (geomean(PRED["Paeth"])     / geomean(avg.tolist()) - 1) * 100
    gm_med = (geomean(PRED["MED"])       / geomean(avg.tolist()) - 1) * 100
    gm_lft = (geomean(PRED["Left-only"]) / geomean(avg.tolist()) - 1) * 100
    ax.axhline(gm_pae, color=GREEN,  ls="--", lw=0.9, alpha=0.7)
    ax.axhline(gm_med, color=PURPLE, ls="--", lw=0.9, alpha=0.7)
    ax.axhline(gm_lft, color=ORANGE, ls=":",  lw=0.9, alpha=0.7)

    ax.set_xticks(x)
    ax.set_xticklabels(IMAGES, rotation=45, ha="right", fontsize=7.5)
    ax.set_ylabel("Ratio change vs. Avg predictor (%)", fontsize=8.5)
    ax.set_title("Predictor Ablation — Compression Ratio Change vs. Avg (MIC Default)", fontsize=10, fontweight="bold")
    ax.yaxis.set_minor_locator(ticker.AutoMinorLocator())
    ax.legend(ncol=3, fontsize=7.5, loc="lower right",
              title=f"Geomean: Left={gm_lft:+.1f}%  Paeth={gm_pae:+.1f}%  MED={gm_med:+.1f}%",
              title_fontsize=7)
    ax.grid(True, axis="y", ls=":", alpha=0.35)

    plt.tight_layout()
    plt.savefig(f"{OUT_DIR}/fig5_predictor_ablation.png")
    plt.close()
    print("fig5_predictor_ablation.png done")


# ── Figure 6: TableLog ablation ───────────────────────────────────────────────
def fig6_tablelog():
    tl11 = np.array(TL["TL=11"])
    tl13 = np.array(TL["TL=13"])
    gain = (tl13 - tl11) / tl11 * 100   # % gain from TL=11 → TL=13

    n = len(IMAGES)
    x = np.arange(n)

    # Color bars: green where adaptive bumps, grey where it doesn't
    adap = np.array(TL["Adaptive"])
    bumped = (adap > tl11 + 0.001)  # adaptive selected > TL=11

    colors = [GREEN if b else GREY for b in bumped]

    fig, axes = plt.subplots(2, 1, figsize=(11, 5), gridspec_kw={"height_ratios": [2, 1]})

    # Top: absolute ratios for TL=11, TL=12, TL=13, Adaptive
    ax = axes[0]
    w = 0.2
    ax.bar(x - 1.5*w, TL["TL=11"],    w, label="TL=11",    color=GREY,   alpha=0.8)
    ax.bar(x - 0.5*w, TL["TL=12"],    w, label="TL=12",    color=BLUE,   alpha=0.8)
    ax.bar(x + 0.5*w, TL["TL=13"],    w, label="TL=13",    color=GREEN,  alpha=0.8)
    ax.bar(x + 1.5*w, TL["Adaptive"], w, label="Adaptive", color=ORANGE, alpha=0.8,
           edgecolor="black", linewidth=0.5)
    ax.set_xticks(x)
    ax.set_xticklabels(IMAGES, rotation=45, ha="right", fontsize=7.5)
    ax.set_ylabel("Compression ratio (×)", fontsize=8.5)
    ax.set_title("TableLog Ablation — Compression Ratio vs. Forced/Adaptive TableLog", fontsize=10, fontweight="bold")
    ax.legend(ncol=4, fontsize=7.5)

    # Bottom: gain from TL=11 → TL=13
    ax2 = axes[1]
    bars = ax2.bar(x, gain, color=colors, alpha=0.85, edgecolor="none")
    ax2.axhline(0, color="black", lw=0.7)
    ax2.set_xticks(x)
    ax2.set_xticklabels(IMAGES, rotation=45, ha="right", fontsize=7.5)
    ax2.set_ylabel("TL=11→13 gain (%)", fontsize=8.5)

    # Legend patch for bottom panel
    p1 = mpatches.Patch(color=GREEN, label="Adaptive bumps TL")
    p2 = mpatches.Patch(color=GREY,  label="No adaptive bump")
    ax2.legend(handles=[p1, p2], fontsize=7.5, loc="upper right")
    ax2.grid(True, axis="y", ls=":", alpha=0.35)

    plt.tight_layout()
    plt.savefig(f"{OUT_DIR}/fig6_tablelog_ablation.png")
    plt.close()
    print("fig6_tablelog_ablation.png done")


# ── main ─────────────────────────────────────────────────────────────────────
if __name__ == "__main__":
    fig1_pipeline()
    fig2_histogram()
    fig3_multistate()
    fig4_pareto()
    fig5_predictor()
    fig6_tablelog()
    print(f"\nAll figures written to {OUT_DIR}/")
