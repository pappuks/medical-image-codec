#!/usr/bin/env bash
#
# docx-roundtrip.sh — convert the MIC paper between LaTeX and DOCX with pandoc.
#
# Workflow:
#   1. ./docx-roundtrip.sh to-docx          # tex  -> docx  (edit the .docx in Word/Pages/Google Docs)
#   2. ... edit the .docx manually ...
#   3. ./docx-roundtrip.sh to-tex           # docx -> tex   (regenerate LaTeX from your edits)
#
# By default it operates on mic-paper-v9-ieee-tmi. Pass a different basename
# (without extension) as the second argument to target another file:
#   ./docx-roundtrip.sh to-docx mic-paper-v8-ieee-tmi
#
set -euo pipefail

cd "$(dirname "$0")"

BASE="${2:-mic-paper-v9-ieee-tmi}"
TEX="${BASE}.tex"
DOCX="${BASE}.docx"

case "${1:-}" in
  to-docx)
    [ -f "$TEX" ] || { echo "error: $TEX not found" >&2; exit 1; }
    pandoc "$TEX" \
      --from latex \
      --to docx \
      --resource-path=.:figures \
      --output "$DOCX"
    echo "Wrote $DOCX  (open it in Word / Pages / Google Docs and edit freely)"
    ;;

  to-tex)
    [ -f "$DOCX" ] || { echo "error: $DOCX not found" >&2; exit 1; }
    # Regenerate LaTeX from the edited docx. We write to a NEW file by default so
    # your hand-tuned preamble in the original .tex is never clobbered silently.
    OUT="${BASE}-from-docx.tex"
    pandoc "$DOCX" \
      --from docx \
      --to latex \
      --extract-media=figures-from-docx \
      --output "$OUT"
    echo "Wrote $OUT"
    echo "Note: pandoc emits a plain LaTeX body (no IEEEtran preamble)."
    echo "      Diff it against $TEX and copy your prose edits across, or"
    echo "      splice the body between \\begin{document}...\\end{document}."
    ;;

  *)
    echo "usage: $0 {to-docx|to-tex} [basename]" >&2
    echo "  to-docx   tex  -> docx" >&2
    echo "  to-tex    docx -> tex (writes <basename>-from-docx.tex)" >&2
    exit 1
    ;;
esac
