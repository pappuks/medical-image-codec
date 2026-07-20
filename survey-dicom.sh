#!/bin/bash
# DICOM multi-frame & volumetric dataset survey for PACS viewer prototype
set -e

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
PROMPT="I need a comprehensive survey of ALL DICOM multi-frame and volumetric dataset candidates in this repo for use as test data for a PACS viewer prototype. Do the following:

1. List every .dcm file anywhere in the repository, grouped by directory hierarchy.

2. For each .dcm file (or group from the same series), extract these key attributes using python with pydicom (or pure Python struct if pydicom is unavailable):
   - Modality (CT, MR, CR, DX, US, OT, NM, PT, etc.)
   - PatientName/Study/Series identifiers (if present)
   - Rows x Columns (image dimensions per slice/frame)
   - Bits Allocated / Pixel Representation
   - Photometric Interpretation (MONOCHROME2 is preferred for PACS demos)
   - Number of frames (for multi-frame DICOM, attribute (7FE0,0009))

3. Check the testdata/expanded/ directory — these are .bin files derived from DICOMs. List them all with dimensions and format info.

4. Check the testdata/multiframe/ directory specifically for cine/multi-frame sequences. What modalities do they cover?

5. Check any other testdata subdirectories for additional DICOM series.

6. For EACH distinct modality found (CT, MR, CR, DX, PET, US, etc.), recommend which specific file(s) to use for PACS viewer demos and why.

7. Also check web/testdata/ for pre-compressed mic variants and what codecs they cover.

Present the final output as a structured markdown table with recommendations prioritizing:
- Multi-frame / volumetric datasets (3D volume rendering, MIP, reformation demos)
- Cross-modality coverage (at least one from CT, MR, CR/DX, Ultrasound/US if available)
- High resolution for detail demonstration
- Clinical relevance"

# Write prompt to temp file since it's too long for shell quoting
TMPPROMPT=$(mktemp)
echo "$PROMPT" > "$TMPPROMPT"

claude -c --max-turns 30 < "$TMPPROMPT"

rm -f "$TMPPROMPT"
