# IEEE TMI Submission Portal — Fields to Fill

Reference sheet for submitting **mic-paper-simplified-ieee.pdf** to *IEEE Transactions on
Medical Imaging* via the IEEE Author Portal / ScholarOne Manuscript Central
(<https://mc.manuscriptcentral.com/tmi-ieee>).

Values below are pre-filled from the manuscript. Items marked **[ACTION]** need a
decision or a portal-specific selection. Items marked **[CONFIRM]** are statements
you tick/agree to.

---

## 1. Manuscript type
- **Select:** Regular Paper (Full Paper).
- Initial submission limit is **10 pages including references** — the PDF is exactly 10 pages, so do **not** add anything before upload.

## 2. Title
```
A 16-Bit-Native Lossless Compression Pipeline for Medical Images
```

## 3. Running / short title (if requested)
```
A 16-Bit-Native Lossless Codec for Medical Images
```

## 4. Abstract (≤ 250 words — current: 232)
Paste as a single paragraph, no equations/tables/citations:

```
Medical images must be stored and transmitted losslessly, since a radiologist must
see exactly what the scanner produced, and they are "wide," carrying 10-16 bits per
pixel rather than the 8 bits of an ordinary photo. That wide format is where most
compression tools perform poorly, because nearly all of them were adapted from
photographic and general-purpose imaging, where pixels are 8 bits, rather than
designed for medical data. This paper describes MIC (Medical Image Codec), a
lossless codec designed natively for medical imaging that works directly on 16-bit
data instead of splitting each pixel into two bytes. MIC chains three simple stages,
a spatial predictor, a 16-bit run-length encoder, and a large-alphabet entropy coder
called Finite State Entropy (FSE, a fast table-driven form of asymmetric numeral
systems), and adds a multi-state decoder that keeps the CPU more fully utilized to
speed up decompression. On 39 real DICOM images across eleven modalities, MIC
compresses about 10% better on average (better on 34 of the 39 images) than a strong
general-purpose baseline (Delta + Zstandard), reaches a geometric-mean compression
ratio of 3.36x (within about 2% of High-Throughput JPEG 2000 at 3.42x and about 88%
of JPEG-LS at 3.84x), and is the fastest decoder of the four tested codecs on all 39
images on ARM64 and 36 of 39 on AMD64. A 20 KB pure-JavaScript decoder lets the
format be decoded directly in a web browser.
```
> Note: TMI prefers abstracts "self-contained without abbreviations." This one defines
> its acronyms inline (FSE/ANS) and uses standard ones (DICOM, JPEG 2000, JPEG-LS).
> Usually accepted, but a strict editor could ask you to spell them out. Keep the
> manuscript abstract and this portal abstract identical.

## 5. Keywords — **[ACTION]** select ≥ 2 from EACH of 3 dropdown categories (min. 6 total)
The exact dropdown terms appear only in the portal. Map to the closest available:

- **Imaging Modalities** (≥2): Computed Tomography (CT); Magnetic Resonance Imaging (MRI);
  X-ray / Radiography; (also present in dataset: mammography, PET, nuclear medicine,
  fluoroscopy, tomosynthesis — add if listed).
- **Object of Interest** (≥2): the paper is anatomy-agnostic; dataset spans Brain, Breast,
  Lung/Chest, Prostate, Pancreas, Bone. Pick the two closest available, or an
  "Other / Whole body / Not applicable" option if offered.
- **General Methodologies** (≥2): **Image Compression / Coding** (primary); plus a second
  such as Performance Evaluation, Visualization, or Image Storage & Transmission.

> If the dropdown options are unclear, copy them here and I'll map them precisely.

## 6. Authors & affiliation
| Field | Value |
|---|---|
| Author (sole) | Kuldeep Singh |
| Corresponding author | Yes |
| Affiliation | Innovaccer Inc. |
| Email | kuldeep.singh@innovaccer.com |
| ORCID | 0009-0004-8476-0118 (required; must be linked to your IEEE account) |
| IEEE membership rank | **[ACTION]** enter if applicable (else "Non-member") |
| Country | **[ACTION]** enter institution country |

## 7. Cover letter
- **Skip.** TMI strongly discourages cover letters for fresh submissions (no prior review,
  no conference extension). Do not upload one.

## 8. Suggested / opposed reviewers
- **Suggested reviewers: leave blank.** TMI instructs authors **not** to suggest reviewers.
- **Opposed reviewers — [ACTION] optional:** you may list the authors of the comparison
  libraries as a potential conflict, at your discretion:
  - Aous Naman (OpenJPH / HTJ2K)
  - CharLS maintainers (JPEG-LS)

## 9. Suggested subject area / Associate Editor (if a field exists)
```
Medical Image Compression; Medical Image Storage and Transmission;
Medical Imaging Informatics Systems
```

## 10. Declarations (portal form fields / tick boxes)
- **[CONFIRM] Originality / dual submission:** Original work; not submitted to or published
  in any other journal; no prior conference or workshop version.
- **[CONFIRM] Prior review disclosure:** None — manuscript has not been reviewed/submitted
  elsewhere. (TMI auto-rejects undisclosed prior submissions.)
- **[CONFIRM] Conference extension:** Not applicable (no conference paper to upload).
- **[CONFIRM] Conflict of interest:** The author declares no competing financial interests
  or personal relationships that could have influenced this work.
- **Funding / financial support:** **[ACTION]** state grant(s) or "None / no external
  funding." (Must match the first-page footnote in the manuscript.)
- **Ethics / human subjects:** No human-subjects research. Evaluation uses publicly
  available, de-identified DICOM images (NEMA WG-04 compsamples, NEMA 1997 CD, public
  breast-tomosynthesis case, public GDCM/TCIA grayscale slices). No institutional ethics
  approval required per DICOM PS 3.15 Appendix E.
- **[CONFIRM] Page-limit / format:** ≤10 pages incl. references; double-column IEEEtran
  journal style; 250-word abstract limit respected.
- **[CONFIRM] Similarity check (iThenticate/CrossCheck):** Acknowledge automated similarity
  screening.

## 11. Data availability / reproducibility
```
Complete implementation, benchmark harness, and test-data references are open source at
https://github.com/pappuks/medical-image-codec . All results are reproducible via the
commands documented in the repository.
```
- **[ACTION]** You may also upload the repo/code as a **Supporting Document** (allowed:
  code, datasets, media) — optional.

## 12. Files to upload
- **Main manuscript PDF:** `mic-paper-simplified-ieee.pdf` (10 pages, ~282 KB, well under
  the 40 MB limit). Text, tables, and references embedded. No separate figure files needed
  (this edition is tables-only).
- **Graphical abstract (optional):** none prepared. **[ACTION]** add later if desired.
- **Supporting documents (optional):** source code / benchmark scripts.
- **Do NOT upload:** cover letter; high-resolution figure source files (not wanted at initial
  submission).

---

## Quick pre-submit checklist
- [ ] Manuscript type = Regular Paper
- [ ] Title + running title entered
- [ ] Abstract pasted (single paragraph, identical to PDF)
- [ ] ≥2 keywords in each of the 3 categories
- [ ] ORCID linked to IEEE account
- [ ] Funding statement decided (matches footnote)
- [ ] Originality / no-prior-review / no-COI boxes ticked
- [ ] Ethics statement entered (public de-identified data)
- [ ] PDF (10 pp, <40 MB) uploaded; no cover letter; no reviewer suggestions
- [ ] Optional: code uploaded as supporting document
