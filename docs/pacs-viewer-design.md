# PACS Web Viewer — Design

The half of the demo that shows the pictures. Loads studies from the S3 dataset,
decodes them in the browser, paints them on a canvas, and reports what each
decode cost.

Status: **design, not built.** Companion to
[pacs-lambda-service-design.md](pacs-lambda-service-design.md) (hosting) and
[pacs-access-control-design.md](pacs-access-control-design.md) (bot mitigation).

---

## 1. Why this exists

`pacs-dashboard.html` is a **benchmark harness**: it decodes every image with
every codec and prints tables of milliseconds, MB/s, and compression ratios. It
contains no canvas drawing at all. `index.html` *is* a viewer — window/level,
canvas rendering, cine playback — but it reads only from `testdata/`, which the
deploy script excludes from the upload, and it has no notion of the S3 dataset.

So the deployed demo currently measures decoding without ever showing it. This
page closes that gap: **one screen where you pick a study, watch it render, and
see the decode cost of the frame you are looking at.**

## 2. Shape

A new page — `web/pacs-viewer.html` + `web/pacs-viewer.mjs` — rather than a
retrofit of `index.html`. Three reasons: `index.html` is the working local-file
demo and is linked from the docs, so changing it risks a flow that currently
works; the S3 viewer needs a different top-level UI (study picker, codec
selector, frame transport); and a separate page keeps the local demo usable
offline with no S3 dependency.

Almost nothing is new code. The page is mostly wiring between parts that already
exist:

| Need | Reused from |
|------|-------------|
| Study list, per-study manifests, S3 path resolution | `pacs-study-source.mjs` — `listStudies`, `loadStudy`, `makeS3PathResolver` |
| Decoding, every codec | `codecs/index.mjs` — `makeAdapter`, the documented adapter contract |
| Codec list, checksums, challenge guard | `pacs-model.mjs` — `CODEC_REGISTRY`, `fnv1a32Hex`, `throwIfChallenged` |
| 16-bit → canvas, window/level | the `renderImage` / `autoWindowLevel` approach in `index.html` |

## 3. Layout

A viewport with the image, a control rail, and a live stats panel.

```
┌───────────────────────────────┬──────────────────────┐
│                               │  Study   [picker  ▾] │
│                               │  Codec   [picker  ▾] │
│         canvas                │ ──────────────────── │
│      (decoded frame)          │  Decode      12.4 ms │
│                               │  Throughput 384 MB/s │
│                               │  Transfer    31.2 ms │
│                               │  Compressed  412 KB  │
│                               │  Raw        1.98 MB  │
│                               │  Ratio        4.92×  │
│                               │  Pixels     verified │
├───────────────────────────────┤ ──────────────────── │
│ ◀ ▶  ▶play  [────●──────] 12/69│  Window  [────●────] │
└───────────────────────────────┴──────────────────────┘
```

The stats describe **the frame currently on screen**, and update on every frame
during cine playback. That is the point of the page: the number and the picture
it came from are visible at the same time.

## 4. Decisions worth stating

**Window/level is computed once per study, not per frame.** `autoWindowLevel`
scans for min/max; recomputing it per frame makes a cine loop flicker as the
mapping shifts under the eye. Compute from the first decoded frame, hold it for
the study, and expose manual sliders. Recompute only when the study changes.

**Compressed bytes are cached; decodes are not.** Cine playback re-fetching the
same frame each loop would measure the network, not the codec. Cache fetched
bytes per (frame, codec) and decode fresh every time — decode cost is what the
page is claiming to show. A "re-time this frame" control re-decodes in place.

**Adapters initialise lazily, one at a time.** `init()` spins up WASM modules and,
for PICS, a real Web Worker pool. Initialising all twelve registry entries at
load would be slow and memory-hungry for a page showing one codec at a time.
Init on selection, `dispose()` the previous one.

**Codecs that cannot decode live are excluded from the picker,** not shown
disabled. `CODEC_REGISTRY` marks these with `liveDecodeSupported === false`
(JPEG-XL, whose browser path cannot preserve 16-bit grayscale). A viewer that
offers a codec and then refuses to show a picture is worse than one that doesn't
offer it.

**Single-frame studies hide the transport entirely** rather than showing a
disabled slider reading "1/1".

**Timing uses the same warmup discipline as the benchmark** — but only for the
explicit "re-time" action, where a median of several decodes is meaningful.
During navigation and playback the displayed figure is a single real decode,
labelled as such. Presenting a one-shot number in the same style as a median
would overstate its precision.

## 5. Cross-cutting requirements

**Challenge handling.** Every fetch goes through `throwIfChallenged`. On
`ChallengeExpiredError` the viewer must redirect to
`/bootstrap.html?next=<here>` — never reload in place, because this page is
served with `COEP: require-corp` and the AWS interstitial's cross-origin script
is blocked under it (see the access-control design §5). Same rule as the
dashboard.

**Cross-origin isolation.** The page needs `SharedArrayBuffer` for PICS workers
and the WASM decoders, so it must be served by the default response-headers
policy that sets COOP/COEP — i.e. it must **not** be added to the
`/bootstrap.html` no-COEP behavior.

**Missing artifacts are normal.** Not every codec has every frame — reference
encoders sample deep series. A frame with no artifact for the selected codec
shows "not available for this codec" in the stats panel and leaves the previous
image up, rather than erroring or blanking.

## 6. What this does not do

No measurement claims beyond the single displayed decode. No multi-codec
side-by-side — the dashboard already does that properly, with warmup and
medians, and duplicating it here would invite comparing a one-shot number
against a median. The link between the two pages is a plain hyperlink.

No DICOM parsing in the browser. The viewer reads the codec artifacts produced
by `cmd/mic-pacs-encode` and `cmd/mic-pacs-refgen`, not raw DICOM.
