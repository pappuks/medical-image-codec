// mic-pacs-refgen generates reference-codec artifacts (HTJ2K, JPEG-LS, JPEG-XL)
// for PACS studies. This is a separate, cgo-tagged binary from mic-pacs-encode.
//
// Phase 4 (grayscale only): processes single-frame and series studies,
// generating per-frame reference artifacts.
//
// Requires: libopenjph, libcharls, libjxl installed (see CLAUDE.md).
//
// Usage:
//
//	go build -tags cgo_ojph ./cmd/mic-pacs-refgen
//	./mic-pacs-refgen [-workers 4]
//
//go:build cgo_ojph

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/suyashkumar/dicom"
)

type manifestEntry struct {
	ID   string `json:"id"`
	Tier string `json:"tier"`
}

type manifest struct {
	Entries []manifestEntry `json:"entries"`
}

func main() {
	workers := flag.Int("workers", 4, "number of worker goroutines")
	flag.Parse()

	manifestData, err := os.ReadFile("pacs-data/manifest.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to read manifest: %v\n", err)
		os.Exit(1)
	}

	var m manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to parse manifest: %v\n", err)
		os.Exit(1)
	}

	var tierA []manifestEntry
	for _, e := range m.Entries {
		if e.Tier == "A" {
			tierA = append(tierA, e)
		}
	}

	fmt.Fprintf(os.Stderr, "Reference codec encoding not yet implemented (Phase 4)\n")
	fmt.Fprintf(os.Stderr, "Would process %d studies with %d workers\n", len(tierA), *workers)

	// Placeholder: just verify dicom library loads
	_ = dicom.ParseFile
}
