#!/usr/bin/env python3
"""Upload the ingested PACS dataset to S3 with per-object license metadata.

Reads `manifest.json` (written by pacs_ingest.py) and uploads each study to its
planned key prefix:

    <id>/raw/...     Tier A  (lossless -> valid codec ground truth)
    <id>/demo/...    Tier B  (lossy source -> viewer demo only)

Every object carries `license` / `attribution` / `tier` user-metadata, because
CC-BY requires credit to travel with the data, and the viewer reads it back to
display attribution.

Credentials are taken from the environment (AWS_PROFILE /
AWS_SHARED_CREDENTIALS_FILE / AWS_ACCESS_KEY_ID). Nothing is written to the
repo -- do not add credentials to this file.

Usage:
    python3 upload_s3.py --bucket <name> --region us-west-1
    python3 upload_s3.py --bucket <name> --dry-run
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


# Codec artifact directories produced by mic-pacs-encode / mic-pacs-refgen
# under pacs-data/encoded/<study-id>/.
CODEC_DIRS = ["mic", "pics", "htj2k", "jls", "jxl"]


def ascii_meta(s: str, limit: int = 900) -> str:
    """S3 user-metadata must be US-ASCII header-safe."""
    s = (s or "").replace("\n", " ").replace("\r", " ")
    s = re.sub(r"[^\x20-\x7E]", "", s)
    return s[:limit].strip()


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="./pacs-data", type=Path)
    ap.add_argument("--bucket", required=True)
    ap.add_argument("--region", default="us-west-1")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    manifest = json.loads((args.data / "manifest.json").read_text())
    entries = manifest["entries"]
    raw_src = args.data / "raw-src"

    total_files = total_bytes = 0
    print(f"uploading {len(entries)} studies -> s3://{args.bucket} ({args.region})")

    for i, e in enumerate(entries, 1):
        src = raw_src / e["id"]
        if not src.is_dir():
            print(f"  [skip] {e['id']}: no local dir", file=sys.stderr)
            continue
        # Tier A -> raw/, Tier B -> demo/. Never put lossy data under raw/.
        sub = "raw" if e["tier"] == "A" else "demo"
        dest = f"s3://{args.bucket}/{e['id']}/{sub}/"

        meta = {
            "tier": e["tier"],
            "modality": ascii_meta(e["representative"]["modality"]),
            "license": ascii_meta(e["license"]),
            "attribution": ascii_meta(e["attribution"]),
            "transfer-syntax": ascii_meta(e["representative"]["transfer_syntax_name"]),
            "lossy": "true" if e["representative"]["lossy"] else "false",
        }
        meta_arg = ",".join(f"{k}={v}" for k, v in meta.items() if v)

        cmd = [
            "aws", "s3", "sync", str(src), dest,
            "--region", args.region,
            "--metadata", meta_arg,
            "--exclude", ".complete", "--exclude", ".unzipped",
            "--only-show-errors",
        ]
        if args.dry_run:
            cmd.append("--dryrun")

        n = sum(1 for p in src.rglob("*") if p.is_file() and not p.name.startswith("."))
        b = sum(p.stat().st_size for p in src.rglob("*") if p.is_file())
        print(f"  [{i}/{len(entries)}] {e['id']:30s} tier {e['tier']} "
              f"{n:4d} files {b/2**20:8.1f} MiB -> {sub}/")
        r = subprocess.run(cmd, capture_output=True, text=True)
        if r.returncode != 0:
            print(f"    ERROR: {r.stderr.strip()[:300]}", file=sys.stderr)
            continue
        total_files += n
        total_bytes += b

        # --- codec artifacts -------------------------------------------------
        # Derived works inherit the source study's license/attribution: CC-BY
        # obligations follow the pixels into every compressed variant. Only
        # Tier A produces codec artifacts (lossy sources are never ground truth).
        if e["tier"] != "A":
            continue
        enc = args.data / "encoded" / e["id"]
        if not enc.is_dir():
            continue
        for codec in CODEC_DIRS:
            cdir = enc / codec
            if not cdir.is_dir() or not any(cdir.iterdir()):
                continue
            cmeta = dict(meta)
            cmeta["codec"] = codec
            cmeta["derived-from"] = f"{e['id']}/{sub}"
            cmd = [
                "aws", "s3", "sync", str(cdir),
                f"s3://{args.bucket}/{e['id']}/{codec}/",
                "--region", args.region,
                "--metadata", ",".join(f"{k}={v}" for k, v in cmeta.items() if v),
                "--only-show-errors",
            ]
            if args.dry_run:
                cmd.append("--dryrun")
            cn = sum(1 for p in cdir.rglob("*") if p.is_file())
            cb = sum(p.stat().st_size for p in cdir.rglob("*") if p.is_file())
            r = subprocess.run(cmd, capture_output=True, text=True)
            if r.returncode != 0:
                print(f"    ERROR {codec}: {r.stderr.strip()[:200]}", file=sys.stderr)
                continue
            print(f"        {codec:6s} {cn:4d} files {cb/2**20:8.1f} MiB")
            total_files += cn
            total_bytes += cb

        # Per-study encode manifests, so the viewer can read ratios/checksums.
        for frag in ("mic-manifest.json", "ref-manifest.json"):
            fp = enc / frag
            if fp.is_file() and not args.dry_run:
                subprocess.run(
                    ["aws", "s3", "cp", str(fp),
                     f"s3://{args.bucket}/{e['id']}/{frag}",
                     "--region", args.region, "--content-type", "application/json",
                     "--only-show-errors"], check=False)

    # Manifest at the bucket root so the viewer can discover the dataset.
    if not args.dry_run:
        subprocess.run(
            ["aws", "s3", "cp", str(args.data / "manifest.json"),
             f"s3://{args.bucket}/manifest.json", "--region", args.region,
             "--content-type", "application/json", "--only-show-errors"],
            check=False,
        )

    print(f"\nuploaded {total_files} files, {total_bytes/2**30:.2f} GiB")
    print(f"manifest: s3://{args.bucket}/manifest.json")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
