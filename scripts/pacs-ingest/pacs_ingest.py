#!/usr/bin/env python3
"""PACS web-viewer S3 dataset ingest.

Downloads free open-access DICOM sources, verifies each object's
TransferSyntaxUID, classifies Tier A (lossless -> valid codec ground truth)
vs Tier B (lossy source -> viewer-demo only), extracts modality/dimension/
frame metadata, lays out the S3 key structure, and writes a manifest.json.

Design rule: the S3 bucket holds `raw/` + one compressed object per codec.
The raw object MUST be lossless, or every ratio/PSNR number computed against
it is meaningless -- so lossy sources are quarantined into Tier B and never
placed under a `raw/` key.

Usage:
    python3 pacs_ingest.py --out ./pacs-data            # direct sources only
    python3 pacs_ingest.py --out ./pacs-data --with-idc # also pull IDC series
    python3 pacs_ingest.py --out ./pacs-data --plan     # dry run, no download

Requires: pydicom (repo .venv has it), and for --with-idc the `aws` CLI.
"""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import shutil
import subprocess
import sys
import urllib.request
import zipfile
from dataclasses import dataclass, field
from pathlib import Path

# --- Transfer-syntax classification -----------------------------------------
# Lossy transfer syntaxes: a `raw` object encoded with any of these is already
# degraded and cannot serve as lossless-codec ground truth.
LOSSY_TS = {
    "1.2.840.10008.1.2.4.50": "JPEG Baseline (lossy)",
    "1.2.840.10008.1.2.4.51": "JPEG Extended (lossy)",
    "1.2.840.10008.1.2.4.81": "JPEG-LS Lossy (Near-lossless)",
    "1.2.840.10008.1.2.4.91": "JPEG 2000 (lossy)",
    "1.2.840.10008.1.2.4.93": "JPEG 2000 Part 2 (lossy)",
    "1.2.840.10008.1.2.4.101": "MPEG2 MP@ML",
    "1.2.840.10008.1.2.4.102": "MPEG-4 AVC/H.264",
    "1.2.840.10008.1.2.4.103": "MPEG-4 AVC/H.264 BD",
    "1.2.840.10008.1.2.4.203": "HTJ2K (lossy)",
}
# Lossless / uncompressed transfer syntaxes -> Tier A eligible.
LOSSLESS_TS = {
    "1.2.840.10008.1.2": "Implicit VR LE (uncompressed)",
    "1.2.840.10008.1.2.1": "Explicit VR LE (uncompressed)",
    "1.2.840.10008.1.2.1.99": "Deflated Explicit VR LE",
    "1.2.840.10008.1.2.2": "Explicit VR Big Endian (uncompressed)",
    "1.2.840.10008.1.2.4.57": "JPEG Lossless",
    "1.2.840.10008.1.2.4.70": "JPEG Lossless SV1",
    "1.2.840.10008.1.2.4.80": "JPEG-LS Lossless",
    "1.2.840.10008.1.2.4.90": "JPEG 2000 Lossless",
    "1.2.840.10008.1.2.5": "RLE Lossless",
    "1.2.840.10008.1.2.4.201": "HTJ2K Lossless",
}

CODEC_KEYS = ["mic", "pics", "htj2k", "jls", "jxl"]


@dataclass
class Source:
    id: str
    modality_label: str       # human label; real Modality is read from the file
    tier: str                 # "A" or "B" (expected; verified after download)
    license: str
    attribution: str
    kind: str                 # http_zip | http_dcm | pydicom | idc
    locator: str              # URL / testdata name / IDC series UUID
    note: str = ""


# --- Seed set: everything here is directly fetchable and license-clean -------
SOURCES: list[Source] = [
    # ---- Tier A: genuine lossless / enhanced multi-frame ground truth -------
    Source("enh-ct-multiframe", "CT enhanced multi-frame", "A",
           "pydicom test data (public sample)", "pydicom / DICOM WG",
           "pydicom", "eCT_Supplemental.dcm",
           "True single-file enhanced CT multi-frame volumetric object."),
    Source("enh-mr-multiframe", "MR multi-frame", "A",
           "pydicom test data (public sample)", "pydicom / DICOM WG",
           "pydicom", "emri_small.dcm",
           "Multi-frame MR (small) — enhanced-style frame stack."),
    Source("ct-small", "CT slice", "A",
           "pydicom test data (public sample)", "pydicom",
           "pydicom", "CT_small.dcm", "Single-frame CT, uncompressed 16-bit."),
    Source("mr-small", "MR slice", "A",
           "pydicom test data (public sample)", "pydicom",
           "pydicom", "MR_small.dcm", "Single-frame MR, uncompressed."),
    Source("us-lossless", "US multi-frame", "A",
           "pydicom test data (public sample)", "pydicom",
           "pydicom", "US1_J2KR.dcm",
           "Ultrasound, JPEG 2000 *reversible* (lossless) — valid Tier A."),
    # An intentionally lossy file to prove the Tier-A rejection path works.
    Source("us-lossy-probe", "US (lossy probe)", "B",
           "pydicom test data (public sample)", "pydicom",
           "pydicom", "US1_J2KI.dcm",
           "JPEG 2000 *irreversible* (lossy) — must be quarantined to Tier B."),

    # ---- Tier A: uncompressed single-frame from a public sample site --------
    Source("mr-brain-rubo", "MR brain slice", "A",
           "Rubomedical sample (no formal license; treat as demo)",
           "rubomedical.com",
           "http_zip", "https://www.rubomedical.com/dicom_files/dicom_viewer_Mrbrain.zip",
           "Uncompressed 12-bit MR single frame."),

    # ---- Tier B: lossy cine — viewer-demo only, excluded from fidelity ------
    Source("xa-cine-0002", "XA angiogram cine", "B",
           "Rubomedical sample (no formal license; demo use)", "rubomedical.com",
           "http_zip", "https://www.rubomedical.com/dicom_files/dicom_viewer_0002.zip",
           "512x512 x96, lossy JPEG."),
    Source("xa-cine-0009", "XA angiogram cine", "B",
           "Rubomedical sample (no formal license; demo use)", "rubomedical.com",
           "http_zip", "https://www.rubomedical.com/dicom_files/dicom_viewer_0009.zip",
           "512x512 x137, lossy JPEG."),
    Source("us-cine-0020", "US cine", "B",
           "Rubomedical sample (no formal license; demo use)", "rubomedical.com",
           "http_zip", "https://www.rubomedical.com/dicom_files/dicom_viewer_0020.zip",
           "600x430 x11, RLE."),

    # ---- Tier A volumetric at scale: IDC (opt-in via --with-idc) ------------
    # Populate `locator` with IDC series UUIDs from the IDC portal
    # (https://portal.imaging.datacommons.cancer.gov). Each is pulled from the
    # public, unauthenticated bucket s3://idc-open-data/<series-uuid>/.
    # Left empty by default so multi-GB pulls never fire automatically.
    # Example (uncomment + set a real UUID):
    # Source("ct-lidc-idc", "CT chest volume (IDC/LIDC-IDRI)", "A",
    #        "CC-BY 3.0 (LIDC-IDRI via NCI IDC)", "TCIA/IDC LIDC-IDRI",
    #        "idc", "<series-instance-uid-prefix>/", "Volumetric CT."),
]


def sha256_file(p: Path) -> str:
    h = hashlib.sha256()
    with p.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def fetch(src: Source, raw_src: Path) -> list[Path]:
    """Download/resolve a source into raw_src/<id>/, return list of .dcm paths."""
    import pydicom.data as pdata

    dest = raw_src / src.id
    dest.mkdir(parents=True, exist_ok=True)

    if src.kind == "pydicom":
        path = pdata.get_testdata_file(src.locator)
        if not path:
            raise FileNotFoundError(f"pydicom testdata '{src.locator}' not found")
        out = dest / Path(path).name
        if not out.exists():
            shutil.copy(path, out)
        return [out]

    if src.kind == "http_dcm":
        out = dest / (src.id + ".dcm")
        if not out.exists():
            urllib.request.urlretrieve(src.locator, out)
        return [out]

    if src.kind == "http_zip":
        marker = dest / ".unzipped"
        if not marker.exists():
            data = urllib.request.urlopen(src.locator, timeout=60).read()
            zpath = dest / "_src.zip"
            zpath.write_bytes(data)
            try:
                with zipfile.ZipFile(io.BytesIO(data)) as z:
                    z.extractall(dest)
            except (NotImplementedError, zipfile.BadZipFile):
                # Some sample zips use Deflate64 etc. that Python can't inflate;
                # fall back to the system `unzip`, which handles more methods.
                subprocess.run(["unzip", "-o", "-q", str(zpath), "-d", str(dest)],
                               check=True)
            zpath.unlink(missing_ok=True)
            marker.write_text("ok")
        # Collect DICOM-looking files (many samples have no extension).
        dcms = []
        for p in sorted(dest.rglob("*")):
            if p.is_file() and p.name != ".unzipped":
                if _looks_dicom(p):
                    dcms.append(p)
        return dcms

    if src.kind == "idc":
        if not src.locator:
            return []  # unconfigured IDC slot -> skip
        _idc_pull(src.locator, dest)
        return [p for p in sorted(dest.rglob("*.dcm")) if p.is_file()]

    raise ValueError(f"unknown source kind: {src.kind}")


def _looks_dicom(p: Path) -> bool:
    try:
        with p.open("rb") as f:
            f.seek(128)
            return f.read(4) == b"DICM"
    except OSError:
        return False


def _idc_pull(series_prefix: str, dest: Path) -> None:
    """Pull one IDC series from the public unauthenticated bucket."""
    uri = f"s3://idc-open-data/{series_prefix}"
    print(f"  [idc] aws s3 cp --no-sign-request --recursive {uri}")
    subprocess.run(
        ["aws", "s3", "cp", "--no-sign-request", "--recursive", uri, str(dest)],
        check=True,
    )


def inspect(dcm: Path) -> dict:
    import pydicom
    ds = pydicom.dcmread(str(dcm), stop_before_pixels=True, force=True)
    ts = str(getattr(ds.file_meta, "TransferSyntaxUID", "")) if hasattr(ds, "file_meta") else ""
    lossy = ts in LOSSY_TS
    ts_name = LOSSY_TS.get(ts) or LOSSLESS_TS.get(ts) or (ts or "unknown")
    return {
        "file": dcm.name,
        "modality": str(getattr(ds, "Modality", "?")),
        "rows": int(getattr(ds, "Rows", 0)),
        "cols": int(getattr(ds, "Columns", 0)),
        "frames": int(getattr(ds, "NumberOfFrames", 1) or 1),
        "bits": int(getattr(ds, "BitsStored", getattr(ds, "BitsAllocated", 0)) or 0),
        "photometric": str(getattr(ds, "PhotometricInterpretation", "?")),
        "transfer_syntax": ts,
        "transfer_syntax_name": ts_name,
        "lossy": lossy,
    }


def ingest_idc_selection(sel_path: Path, raw_src: Path, bucket: str) -> list[dict]:
    """Download every series in idc-selection.json and build manifest entries."""
    from idc_index import IDCClient

    sel = json.loads(sel_path.read_text())
    series = sel["series"]
    print(f"\n[idc] {len(series)} series, {sel['total_GB']} GB total")
    client = IDCClient()
    entries = []

    for i, s in enumerate(series, 1):
        dest = raw_src / s["id"]
        dest.mkdir(parents=True, exist_ok=True)
        done = dest / ".complete"
        if not done.exists():
            print(f"[idc {i}/{len(series)}] {s['id']} {s['modality']} "
                  f"{s['size_MB']:.0f} MB ({s['collection']})")
            try:
                client.download_dicom_series(
                    seriesInstanceUID=s["series_uid"],
                    downloadDir=str(dest),
                    dirTemplate="",          # flat: files straight into dest
                    quiet=True,
                )
                done.write_text("ok")
            except Exception as e:  # noqa: BLE001
                print(f"  ERROR: {e}", file=sys.stderr)
                continue
        else:
            print(f"[idc {i}/{len(series)}] {s['id']} (cached)")

        dcms = [p for p in sorted(dest.rglob("*")) if p.is_file() and _looks_dicom(p)]
        if not dcms:
            print("  (no DICOM files found)", file=sys.stderr)
            continue

        meta = inspect(dcms[0])
        actual_tier = "B" if meta["lossy"] else "A"
        if actual_tier == "B":
            print(f"  ⚠ QUARANTINED: lossy ({meta['transfer_syntax_name']})")
        base = s["id"]
        entries.append({
            "id": s["id"],
            "modality_label": f"{s['modality']} {s['collection']} (IDC)",
            "tier": actual_tier,
            "declared_tier": "A",
            "quarantined": actual_tier == "B",
            "promoted": False,
            "license": s["license"],
            "attribution": f"NCI Imaging Data Commons / {s['collection']}"
                           + (f" — DOI {s['source_DOI']}" if s.get("source_DOI") else ""),
            "note": s.get("description", ""),
            "source_kind": "idc",
            "source_locator": s["series_uid"],
            "files": len(dcms),
            "representative": meta,
            "bytes": sum(p.stat().st_size for p in dcms),
            "sha256_representative": sha256_file(dcms[0]),
            "s3": {
                "bucket": bucket,
                "raw_key": (f"{base}/raw/" if actual_tier == "A" else f"{base}/demo/"),
                "codec_prefixes": ({c: f"{base}/{c}/" for c in CODEC_KEYS}
                                   if actual_tier == "A" else {}),
            },
        })
    return entries


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--out", default="./pacs-data", type=Path)
    ap.add_argument("--bucket", default="mic-pacs-demo",
                    help="S3 bucket name for key layout in the manifest")
    ap.add_argument("--with-idc", action="store_true",
                    help="also pull IDC volumetric series from idc-selection.json")
    ap.add_argument("--plan", action="store_true",
                    help="dry run: print what would be fetched, no download")
    args = ap.parse_args()

    out: Path = args.out
    raw_src = out / "raw-src"
    raw_src.mkdir(parents=True, exist_ok=True)

    entries = []
    errors = []
    for src in SOURCES:
        if src.kind == "idc" and (not args.with_idc or not src.locator):
            print(f"[skip] {src.id} (IDC; use --with-idc and set a series UUID)")
            continue
        if args.plan:
            print(f"[plan] {src.id:20s} {src.kind:9s} tier {src.tier}  {src.locator}")
            continue
        print(f"[fetch] {src.id} ({src.kind})")
        try:
            dcms = fetch(src, raw_src)
        except Exception as e:  # noqa: BLE001 - report and continue
            print(f"  ERROR: {e}", file=sys.stderr)
            errors.append({"id": src.id, "error": str(e)})
            continue
        if not dcms:
            print("  (no DICOM files produced)")
            continue

        meta = inspect(dcms[0])  # representative frame/slice for the series
        actual_tier = "B" if meta["lossy"] else "A"
        # Trust the real transfer syntax over the declared tier.
        quarantined = src.tier == "A" and actual_tier == "B"   # lossy demotion
        promoted = src.tier == "B" and actual_tier == "A"      # lossless promotion
        if quarantined:
            print(f"  ⚠ QUARANTINED: declared Tier A but source is LOSSY "
                  f"({meta['transfer_syntax_name']}) -> forced to Tier B")
        if promoted:
            print(f"  ↑ promoted to Tier A: source is lossless "
                  f"({meta['transfer_syntax_name']})")

        # S3 key layout. Lossless -> raw/ + codec dirs. Lossy -> demo/ only.
        base = f"{src.id}"
        if actual_tier == "A":
            raw_key = f"{base}/raw/{meta['file']}"
            codec_keys = {c: f"{base}/{c}/" for c in CODEC_KEYS}
        else:
            raw_key = f"{base}/demo/{meta['file']}"  # never under raw/
            codec_keys = {}

        entries.append({
            "id": src.id,
            "modality_label": src.modality_label,
            "tier": actual_tier,
            "declared_tier": src.tier,
            "quarantined": quarantined,
            "promoted": promoted,
            "license": src.license,
            "attribution": src.attribution,
            "note": src.note,
            "source_kind": src.kind,
            "source_locator": src.locator,
            "files": len(dcms),
            "representative": meta,
            "bytes": sum(p.stat().st_size for p in dcms),
            "sha256_representative": sha256_file(dcms[0]),
            "s3": {
                "bucket": args.bucket,
                "raw_key": raw_key,
                "codec_prefixes": codec_keys,
            },
        })

    if args.plan:
        return 0

    if args.with_idc:
        sel_path = out / "idc-selection.json"
        if not sel_path.exists():
            print(f"ERROR: {sel_path} missing — run select_idc.py first", file=sys.stderr)
            return 1
        entries.extend(ingest_idc_selection(sel_path, raw_src, args.bucket))

    manifest = {
        "bucket": args.bucket,
        "codecs": CODEC_KEYS,
        "tier_A_count": sum(1 for e in entries if e["tier"] == "A"),
        "tier_B_count": sum(1 for e in entries if e["tier"] == "B"),
        "entries": entries,
        "errors": errors,
    }
    man_path = out / "manifest.json"
    man_path.write_text(json.dumps(manifest, indent=2))

    print("\n=== INGEST SUMMARY ===")
    print(f"Tier A (codec ground truth): {manifest['tier_A_count']}")
    print(f"Tier B (viewer-demo only):   {manifest['tier_B_count']}")
    for e in entries:
        m = e["representative"]
        flag = " QUARANTINED" if e["quarantined"] else (" promoted" if e["promoted"] else "")
        # Volumetric depth = files x frames: a CT/MR *series* is many
        # single-frame files, while tomo/cine is one file with many frames.
        depth = e["files"] * m["frames"]
        kind = "frames" if m["frames"] > 1 else "slices"
        print(f"  [{e['tier']}] {e['id']:28s} {m['modality']:3s} "
              f"{m['rows']}x{m['cols']} {kind}={depth:<5d} "
              f"{m['transfer_syntax_name']}{flag}")
    if errors:
        print(f"\n{len(errors)} error(s): " + ", ".join(e['id'] for e in errors))
    print(f"\nManifest: {man_path}")
    print(f"Next: compress each Tier-A raw/ object into {CODEC_KEYS} and "
          f"`aws s3 sync {out}/raw-src s3://{args.bucket}/`")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
