#!/usr/bin/env bash
# Build a PDF from a .tex file in this directory.
#
# Usage:
#   ./build.sh                         # builds the newest mic-paper-v*-ieee*.tex
#   ./build.sh mic-paper-v7-ieee-tip   # builds that file (with or without .tex)
#   ./build.sh -l mic-paper-v7-ieee-tip   # also runs chktex linter (if installed)
#   ./build.sh -c                      # clean aux files in this directory
#
# Engine selection (in order of preference):
#   1. tectonic  — single binary, auto bibtex + package downloads, no admin needed
#   2. latexmk   — handles bibtex + re-runs (TeX Live)
#   3. pdflatex  — manual pdflatex -> bibtex -> pdflatex -> pdflatex sequence

set -euo pipefail

cd "$(dirname "$0")"

# --- Ensure TeX Live binaries are on PATH (covers MacTeX default install) ---
for p in /Library/TeX/texbin /usr/local/texlive/2024/bin/universal-darwin /opt/homebrew/bin /usr/local/bin; do
  [[ -d "$p" ]] && PATH="$p:$PATH"
done
export PATH

# --- Parse args ---
LINT=0
CLEAN=0
TARGET=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -l|--lint)  LINT=1; shift ;;
    -c|--clean) CLEAN=1; shift ;;
    -h|--help)
      sed -n '2,12p' "$0"; exit 0 ;;
    *) TARGET="$1"; shift ;;
  esac
done

# --- Clean mode ---
if [[ $CLEAN -eq 1 ]]; then
  echo "Cleaning aux files..."
  rm -f -- *.aux *.log *.bbl *.blg *.out *.toc *.lof *.lot *.synctex.gz *.fls *.fdb_latexmk *.nav *.snm *.vrb
  echo "Done."
  exit 0
fi

# --- Resolve target file ---
if [[ -z "$TARGET" ]]; then
  TARGET=$(ls -t mic-paper-v*-ieee*.tex 2>/dev/null | head -n1 || true)
  if [[ -z "$TARGET" ]]; then
    echo "No target given and no mic-paper-v*-ieee*.tex found." >&2
    exit 1
  fi
  echo "No target specified; using newest: $TARGET"
fi
TARGET="${TARGET%.tex}"
if [[ ! -f "${TARGET}.tex" ]]; then
  echo "File not found: ${TARGET}.tex" >&2
  exit 1
fi

# --- Check for an engine ---
if ! command -v tectonic >/dev/null 2>&1 && ! command -v pdflatex >/dev/null 2>&1; then
  cat >&2 <<'EOF'
No LaTeX engine found (tectonic or pdflatex required).

Recommended (no admin rights needed):
  brew install tectonic

Alternatives:
  - TinyTeX (user-local TeX Live, no admin):
      curl -sL "https://yihui.org/tinytex/install-bin-unix.sh" | sh
  - BasicTeX (requires sudo):
      brew install --cask basictex

Open a new terminal after installing so PATH picks up the engine.
EOF
  exit 127
fi

# --- Optional lint pass ---
if [[ $LINT -eq 1 ]]; then
  if command -v chktex >/dev/null 2>&1; then
    echo "==> chktex ${TARGET}.tex"
    chktex -q -n8 -n36 "${TARGET}.tex" || true
  else
    echo "chktex not installed; skipping lint. (sudo tlmgr install chktex)" >&2
  fi
fi

# --- Build ---
echo "==> Building ${TARGET}.pdf"
if command -v tectonic >/dev/null 2>&1; then
  # Tectonic auto-loops bibtex/pdflatex and downloads missing packages.
  # --keep-logs leaves a .log file for debugging; output PDF lands next to .tex.
  tectonic --keep-logs --synctex "${TARGET}.tex"
elif command -v latexmk >/dev/null 2>&1; then
  latexmk -pdf -interaction=nonstopmode -halt-on-error -file-line-error "${TARGET}.tex"
else
  echo "(no tectonic/latexmk; falling back to manual pdflatex sequence)"
  pdflatex -interaction=nonstopmode -halt-on-error -file-line-error "${TARGET}.tex"
  if grep -q '\\bibdata' "${TARGET}.aux" 2>/dev/null && command -v bibtex >/dev/null 2>&1; then
    bibtex "${TARGET}" || true
    pdflatex -interaction=nonstopmode -halt-on-error -file-line-error "${TARGET}.tex"
  fi
  pdflatex -interaction=nonstopmode -halt-on-error -file-line-error "${TARGET}.tex"
fi

echo "==> Done: $(pwd)/${TARGET}.pdf"
