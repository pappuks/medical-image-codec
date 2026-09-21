#!/usr/bin/env python3
"""Select IDC volumetric series for the PACS S3 benchmark.

Filters the NCI Imaging Data Commons index (~1.03M series) down to a
modality-balanced, license-clean, LOSSLESS-only selection and writes
`idc-selection.json` for `pacs_ingest.py --with-idc` to download.

Hard filters (non-negotiable for a lossless-codec benchmark):
  * transfer syntax must be uncompressed or losslessly compressed
  * license must be CC BY (NC variants are EXCLUDED - they forbid commercial
    use, and `breast_cancer_screening_dbt` is CC BY-NC 4.0, so tomosynthesis
    is sourced from `ea1141` (CC BY 4.0) instead)

Usage:
    python3 select_idc.py --out ./pacs-data            # write selection
    python3 select_idc.py --out ./pacs-data --scale 2  # ~2x the data
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path

LOSSLESS_TS_NAMES = {
    "Implicit VR Little Endian",
    "Explicit VR Little Endian",
    "Explicit VR Big Endian",
    "JPEG 2000 Lossless",
    "JPEG Lossless",
    "JPEG-LS Lossless",
    "Explicit VR Little Endian,JPEG 2000 Lossless",
}
PERMISSIVE_LICENSES = {"CC BY 4.0", "CC BY 3.0"}

# (label, modality, [collections], n_series, min_MB, max_MB)
# Size bands keep us on genuinely volumetric studies while avoiding the
# 150 GB slide-microscopy monsters.
QUOTAS = [
    # --- volumetric CT ---
    ("ct-colonography", "CT", ["ct_colonography"],      6, 120, 600),
    ("ct-nlst",         "CT", ["nlst"],                 6,  50, 300),
    ("ct-4d-lung",      "CT", ["4d_lung"],              4,  20, 200),
    # --- volumetric MR ---
    ("mr-breast-adv",   "MR", ["advanced_mri_breast_lesions"], 5, 40, 400),
    ("mr-ispy2",        "MR", ["ispy2"],                5,  30, 400),
    ("mr-prostatex",    "MR", ["prostatex"],            4,   5, 200),
    # --- mammography 2D ---
    ("mg-cmmd",         "MG", ["cmmd"],                 6,   5, 100),
    ("mg-cbis-ddsm",    "MG", ["cbis_ddsm"],            5,  10, 200),
    ("mg-victre",       "MG", ["victre"],               4,  30, 300),
    # --- mammography 3D / tomosynthesis (CC BY 4.0 source) ---
    ("mg-tomo-ea1141",  "MG", ["ea1141"],               4, 300, 1200),
    # --- PET ---
    ("pt-psma",         "PT", ["psma_pet_ct_lesions"],  4,  10, 200),
    ("pt-nsclc",        "PT", ["acrin_nsclc_fdg_pet"],  3,   5, 100),
    # --- projection radiography ---
    ("cr-plain",        "CR", [],                       4,   5, 100),
    ("dx-plain",        "DX", [],                       4,   5, 100),
    # --- ultrasound ---
    ("us-series",       "US", [],                       4,  10, 300),
    # --- slide microscopy / WSI (feeds the MIC3 pipeline) ---
    ("sm-wsi-small",    "SM", ["htan_wustl"],           3,   5, 300),
    ("sm-wsi-mid",      "SM", ["htan_ohsu"],            2, 200, 1500),
]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="./pacs-data", type=Path)
    ap.add_argument("--scale", type=float, default=1.0,
                    help="multiply every per-group series count")
    args = ap.parse_args()

    from idc_index import IDCClient
    idx = IDCClient().index

    base = idx[
        idx["transfer_syntax_name"].isin(LOSSLESS_TS_NAMES)
        & idx["license_short_name"].isin(PERMISSIVE_LICENSES)
    ]
    print(f"index: {len(idx):,} series -> lossless + CC-BY: {len(base):,}")

    picked, seen = [], set()
    for label, modality, collections, n, lo, hi in QUOTAS:
        n = max(1, int(round(n * args.scale)))
        sel = base[(base["Modality"] == modality)
                   & (base["series_size_MB"] >= lo)
                   & (base["series_size_MB"] <= hi)]
        if collections:
            sel = sel[sel["collection_id"].isin(collections)]
        # Deterministic: sort by UID so re-runs pick the same series.
        sel = sel.sort_values("SeriesInstanceUID")
        sel = sel[~sel["SeriesInstanceUID"].isin(seen)].head(n)
        if len(sel) == 0:
            print(f"  [warn] {label}: no series matched (band {lo}-{hi} MB)")
            continue
        for _, r in sel.iterrows():
            seen.add(r["SeriesInstanceUID"])
            picked.append({
                "group": label,
                "id": f"{label}-{r['SeriesInstanceUID'][-8:]}",
                "modality": r["Modality"],
                "collection": r["collection_id"],
                "series_uid": r["SeriesInstanceUID"],
                "crdc_series_uuid": r.get("crdc_series_uuid", ""),
                "aws_url": r.get("series_aws_url", ""),
                "size_MB": float(r["series_size_MB"]),
                "instances": int(r["instanceCount"]),
                "transfer_syntax": r["transfer_syntax_name"],
                "license": r["license_short_name"],
                "source_DOI": r.get("source_DOI", ""),
                "description": str(r.get("SeriesDescription", "")),
            })
        tot = sel["series_size_MB"].sum()
        print(f"  {label:18s} {modality:3s} n={len(sel):2d}  {tot:9.1f} MB  "
              f"({sel['collection_id'].iloc[0]})")

    args.out.mkdir(parents=True, exist_ok=True)
    total_mb = sum(p["size_MB"] for p in picked)
    out = args.out / "idc-selection.json"
    out.write_text(json.dumps({
        "total_series": len(picked),
        "total_MB": round(total_mb, 1),
        "total_GB": round(total_mb / 1024, 2),
        "filters": {
            "lossless_only": sorted(LOSSLESS_TS_NAMES),
            "licenses": sorted(PERMISSIVE_LICENSES),
            "excluded": "CC BY-NC (non-commercial) — incl. breast_cancer_screening_dbt",
        },
        "series": picked,
    }, indent=2))

    print(f"\n=== SELECTION ===")
    by_mod: dict[str, list] = {}
    for p in picked:
        by_mod.setdefault(p["modality"], []).append(p)
    for m, rows in sorted(by_mod.items()):
        print(f"  {m:3s} n={len(rows):3d}  {sum(r['size_MB'] for r in rows)/1024:7.2f} GB")
    print(f"  TOTAL n={len(picked)}  {total_mb/1024:.2f} GB")
    print(f"\nWrote {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
