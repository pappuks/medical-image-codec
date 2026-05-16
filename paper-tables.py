#!/usr/bin/env python3
"""Format Go benchmark output as the tables that appear in
paper/mic-paper-v6-ieee-tmi.tex.

Usage:
    paper-tables.py <results-directory>

The directory is expected to contain the .txt files produced by
run-paper-benchmarks.sh:
    01-all-codecs-decompress.txt   -> Table 1 (ratios), Tables 4/5 (decomp)
    02-all-codecs-encode.txt       -> Tables 2/3 (encode)
    03-fse-1state.txt              -> Table 6 (FSE 1-state)
    04-fse-4state.txt              -> Table 6 (FSE 4-state)
    05-delta-zstd.txt              -> Table 1 Δ+Zstd-19 column
    06-wavelet-simd.txt            -> Wavelet column in Tables 1/4/5

Tables are written both to stdout (aligned ASCII for terminal review) and to
<results-dir>/paper-tables.txt.
"""

import re
import sys
from collections import defaultdict
from pathlib import Path

# Canonical 21-image order matching the paper's tables.
IMAGE_ORDER = [
    "MR", "CT", "CR", "XR", "MG1", "MG2", "MG3", "MG4",
    "CT1", "CT2", "MG-N",
    "MR1", "MR2", "MR3", "MR4", "NM1",
    "RG1", "RG2", "RG3", "SC1", "XA1",
]

# Raw image size in MB as reported by the paper Table 1 (col 2). Used purely
# for display so the generated table matches the paper layout exactly.
RAW_MB = {
    "MR": 0.13, "CT": 0.50, "CR": 7.18, "XR": 10.1,
    "MG1": 9.35, "MG2": 9.35, "MG3": 27.3, "MG4": 26.0,
    "CT1": 0.50, "CT2": 0.50, "MG-N": 27.3,
    "MR1": 0.50, "MR2": 2.00, "MR3": 0.50, "MR4": 0.50, "NM1": 0.50,
    "RG1": 6.86, "RG2": 7.18, "RG3": 5.91, "SC1": 9.71, "XA1": 2.00,
}

# One regex covers every benchmark line we care about. Fields after MB/s vary
# (some benches add `<ratio> ratio`), so the ratio group is optional.
BENCH_RE = re.compile(
    r"^Benchmark(?P<bench>[A-Za-z0-9_]+)/(?P<rest>\S+?)-\d+\s+"
    r"\d+\s+\d+(?:\.\d+)?\s+ns/op\s+"
    r"(?P<mbs>\d+(?:\.\d+)?)\s+MB/s"
    r"(?:.*?\s(?P<ratio>\d+(?:\.\d+)?)\s+ratio)?"
)


def parse_results(results_dir: Path):
    """Return {bench_name: {variant: {image: (mbs, ratio_or_None)}}}.

    For benches whose path is `<image>` (no variant) we use variant='_'.
    For benches whose path is `<image>/<state>` (FSE microbenches) we treat
    state as the variant.
    For benches whose path is `<variant>/<image>` we use that directly.
    """
    data = defaultdict(lambda: defaultdict(dict))
    for txt in sorted(results_dir.glob("*.txt")):
        for line in txt.read_text(errors="replace").splitlines():
            m = BENCH_RE.match(line.strip())
            if not m:
                continue
            bench = m.group("bench")
            parts = m.group("rest").split("/")
            mbs = float(m.group("mbs"))
            ratio = float(m.group("ratio")) if m.group("ratio") else None

            if bench in ("AllCodecs", "AllCodecsEncode"):
                # <variant>/<image>
                if len(parts) != 2:
                    continue
                variant, image = parts
            elif bench in ("FSEDecompress", "FSEDecompress4State"):
                # <image>/<state>
                if len(parts) != 2:
                    continue
                image, variant = parts
            elif bench == "DeltaZstdDecompress":
                # <image>/<variant>   (variant in {MIC, zstd-3})
                if len(parts) != 2:
                    continue
                image, variant = parts
            elif bench == "WaveletV2SIMDRLEFSECompress":
                # <image>
                if len(parts) != 1:
                    continue
                image, variant = parts[0], "_"
            else:
                continue
            data[bench][variant][image] = (mbs, ratio)
    return data


def fmt_ratio(x):
    return f"{x:5.2f}x" if x is not None else "   -- "


def fmt_mbs(x):
    if x is None:
        return "   -- "
    if x >= 1000:
        return f"{x:6,.0f}"
    return f"{x:6.0f}"


def fmt_gain(base, fast):
    if base is None or fast is None or base <= 0:
        return "   -- "
    return f"{(fast / base - 1) * 100:+5.0f}%"


def render_table(title, headers, rows):
    """Return aligned ASCII table as a string."""
    widths = [max(len(h), max((len(r[i]) for r in rows), default=0))
              for i, h in enumerate(headers)]
    sep = "  ".join("-" * w for w in widths)

    out = [title, "=" * len(title), ""]
    out.append("  ".join(h.ljust(widths[i]) for i, h in enumerate(headers)))
    out.append(sep)
    for r in rows:
        out.append("  ".join(c.ljust(widths[i]) for i, c in enumerate(r)))
    out.append("")
    return "\n".join(out)


def table_ratios(data):
    """Table 1 — Compression ratios."""
    allc = data.get("AllCodecs", {})
    zstd = data.get("DeltaZstdDecompress", {})
    wav = data.get("WaveletV2SIMDRLEFSECompress", {}).get("_", {})

    headers = ["Image", "Raw(MB)", "Δ+Zstd-19", "MIC",
               "Wavelet", "PICS-4", "PICS-8", "HTJ2K", "JPEG-LS"]
    rows = []
    for img in IMAGE_ORDER:
        rows.append([
            img,
            f"{RAW_MB.get(img, 0):.2f}",
            fmt_ratio((zstd.get("zstd-19", {}).get(img, (None, None)))[1]),
            fmt_ratio((allc.get("MIC-4state", {}).get(img, (None, None)))[1]),
            fmt_ratio((wav.get(img, (None, None)))[1]),
            fmt_ratio((allc.get("PICS-4", {}).get(img, (None, None)))[1]),
            fmt_ratio((allc.get("PICS-8", {}).get(img, (None, None)))[1]),
            fmt_ratio((allc.get("HTJ2K", {}).get(img, (None, None)))[1]),
            fmt_ratio((allc.get("JPEGLS", {}).get(img, (None, None)))[1]),
        ])
    return render_table(
        "Table 1 — Lossless compression ratios (paper tab:ratios)",
        headers, rows)


def _throughput_table(title, src, variants, header_labels, wavelet_data=None):
    headers = ["Image"] + header_labels
    rows = []
    for img in IMAGE_ORDER:
        row = [img]
        for v in variants:
            if v == "__wavelet__":
                pair = (wavelet_data or {}).get(img, (None, None))
            else:
                pair = src.get(v, {}).get(img, (None, None))
            row.append(fmt_mbs(pair[0]))
        rows.append(row)
    return render_table(title, headers, rows)


def table_encode(data):
    """Tables 2/3 — Encoding throughput."""
    src = data.get("AllCodecsEncode", {})
    variants = ["MIC-Go", "MIC-4state", "MIC-4state-C", "MIC-C",
                "Wavelet+SIMD", "HTJ2K", "JPEGLS",
                "PICS-2", "PICS-4", "PICS-8"]
    labels = ["MIC-Go", "MIC-4s", "MIC-4s-C", "MIC-C",
              "Wav+SIMD", "HTJ2K", "JPEG-LS",
              "PICS-2", "PICS-4", "PICS-8"]
    return _throughput_table(
        "Table 2/3 — Encoding throughput, MB/s (paper tab:enc-*)",
        src, variants, labels)


def table_decompress(data):
    """Tables 4/5 — Decompression throughput."""
    src = data.get("AllCodecs", {})
    wav = data.get("WaveletV2SIMDRLEFSECompress", {}).get("_", {})
    variants = ["MIC-Go", "MIC-4state", "MIC-4state-C", "MIC-4state-SIMD",
                "__wavelet__", "HTJ2K", "JPEGLS",
                "PICS-C-2", "PICS-C-4", "PICS-C-8"]
    labels = ["MIC-Go", "MIC-4s", "MIC-4s-C", "MIC-4s-SIMD",
              "Wav+SIMD", "HTJ2K", "JPEG-LS",
              "PICS-C-2", "PICS-C-4", "PICS-C-8"]
    return _throughput_table(
        "Table 4/5 — Decompression throughput, MB/s (paper tab:decomp-*)",
        src, variants, labels, wavelet_data=wav)


def table_fse(data):
    """Table 6 — FSE 1-state vs 4-state microbench."""
    one = data.get("FSEDecompress", {}).get("1state", {})
    four = data.get("FSEDecompress4State", {}).get("4state", {})

    headers = ["Image", "1-state MB/s", "4-state MB/s", "gain"]
    rows = []
    one_vals, four_vals = [], []
    for img in IMAGE_ORDER:
        b = one.get(img, (None, None))[0]
        f = four.get(img, (None, None))[0]
        rows.append([img, fmt_mbs(b), fmt_mbs(f), fmt_gain(b, f)])
        if b is not None and f is not None and b > 0:
            one_vals.append(b)
            four_vals.append(f)

    # Geometric mean across images that have both numbers (matches the paper's
    # "Geomean (21)" row).
    if one_vals and four_vals:
        from math import exp, log
        gm = lambda xs: exp(sum(log(x) for x in xs) / len(xs))
        gm1, gm4 = gm(one_vals), gm(four_vals)
        rows.append(["Geomean", fmt_mbs(gm1), fmt_mbs(gm4), fmt_gain(gm1, gm4)])
    return render_table(
        "Table 6 — FSE decompression microbench (paper tab:fse-combined)",
        headers, rows)


def main():
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    results_dir = Path(sys.argv[1])
    if not results_dir.is_dir():
        print(f"not a directory: {results_dir}", file=sys.stderr)
        sys.exit(2)

    data = parse_results(results_dir)

    sections = [
        table_ratios(data),
        table_encode(data),
        table_decompress(data),
        table_fse(data),
    ]
    out = "\n\n".join(sections) + "\n"
    sys.stdout.write(out)

    out_path = results_dir / "paper-tables.txt"
    out_path.write_text(out)
    print(f"\nWrote {out_path}", file=sys.stderr)


if __name__ == "__main__":
    main()
