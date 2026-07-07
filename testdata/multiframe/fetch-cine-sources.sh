#!/usr/bin/env bash
# fetch-cine-sources.sh — (re)download the multi-frame DICOM sources used by the
# PACS cine benchmark and regenerate the one transcoded (uncompressed) file.
#
# All sources are public-domain sample DICOMs. Four of the five are already
# stored uncompressed (Explicit VR Little Endian, transfer syntax 1.2.840.10008.1.2.1)
# and are read directly by `github.com/suyashkumar/dicom` in cmd/mic-compress.
# The XA angiography sample is only distributed JPEG-Lossless-encapsulated, so we
# transcode it to uncompressed once here (the Go DICOM lib only decodes native
# pixel data). Requires the project .venv with pydicom + pylibjpeg.
#
# Usage:  bash testdata/multiframe/fetch-cine-sources.sh
set -euo pipefail
cd "$(dirname "$0")"

OME="https://downloads.openmicroscopy.org/images/DICOM/samples"   # (c) Sébastien Barré, public domain
PYD="https://raw.githubusercontent.com/pydicom/pydicom-data/master/data_store/data"
TFIO="https://raw.githubusercontent.com/tensorflow/io/master/tests/test_dicom"

echo "Downloading native (uncompressed) multi-frame sources…"
curl -fsSL -o MR-MONO2-8-16x-heart.dcm  "$OME/MR-MONO2-8-16x-heart.dcm"    # cardiac cine MR, 16f 256x256 8b
curl -fsSL -o NM-MONO2-16-13x-heart.dcm "$OME/NM-MONO2-16-13x-heart.dcm"   # nuclear medicine gated heart, 13f 64x64 16b
curl -fsSL -o emri_small.dcm            "$PYD/emri_small.dcm"              # enhanced/volumetric MR, 10f 64x64 16b
curl -fsSL -o eCT_Supplemental.dcm      "$PYD/eCT_Supplemental.dcm"       # enhanced CT, 2f 512x512 16b

echo "Downloading + transcoding JPEG-Lossless XA to uncompressed…"
curl -fsSL -o XA-src-jpegll.dcm "$TFIO/XA-MONO2-8-12x-catheter.dcm"       # XA coronary angiography, 12f 512x512 8b (JPEG-LL)
../../.venv/bin/python - <<'PY'
import warnings; warnings.filterwarnings("ignore")
import pydicom
from pydicom.uid import ExplicitVRLittleEndian
ds = pydicom.dcmread("XA-src-jpegll.dcm")
arr = ds.pixel_array                       # decodes JPEG-Lossless via pylibjpeg
ds.PixelData = arr.tobytes()
ds.file_meta.TransferSyntaxUID = ExplicitVRLittleEndian
ds['PixelData'].VR = 'OW' if ds.BitsAllocated == 16 else 'OB'
ds.save_as("XA-MONO2-8-12x-catheter.dcm", enforce_file_format=True)
print("transcoded XA-MONO2-8-12x-catheter.dcm:", arr.shape, arr.dtype)
PY
rm -f XA-src-jpegll.dcm

echo "Done. Sources ready in $(pwd)"
