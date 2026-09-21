// pacs-study-source.mjs — S3-backed study source for the PACS dashboard.
//
// The indirection that lets the same decode/verify/benchmark machinery in
// pacs-runner.mjs point at the real S3 dataset instead of the local
// web/testdata/ set. Design source of truth:
// docs/pacs-lambda-service-design.md §7 (Front-End Change).
//
// Activation: the dashboard loads this module only when ?source=s3 is present,
// so the existing testdata/ dev/CI/Playwright flow is completely untouched.
//
// What this module does:
//   1. listStudies()        — GET /api/studies (the thin Lambda; private bucket
//                              can't be listed by a browser directly).
//   2. loadStudy(id)        — GET /data/<id>/mic-manifest.json + ref-manifest.json
//                              and project them into the IMAGES / CINE_DATASETS
//                              shapes pacs-runner.mjs already consumes, plus a
//                              resolvePath() closure that mirrors candidatePaths()
//                              but emits <id>/<codec-dir>/<imgName><suffix>.<ext>.
//   3. buildManifests()     — re-shape the per-study mic/ref manifests into the
//                              { images: { [imgName]: { checksum } } } and
//                              { images: { [imgName]: { [codec]: { bytes } } } }
//                              lookup tables measureOne() already expects, so
//                              pixel verification and informational reference
//                              codecs work without touching pacs-runner.mjs.
//
// Blob bytes never go through /api/* — the dashboard fetches /data/<id>/.../
// directly, served by CloudFront → S3 (OAC). The Lambda is only for listing.

import { fnv1a32Hex } from './pacs-model.mjs';
import { ChallengeExpiredError } from './pacs-runner.mjs';

// Codec kind -> S3 sub-directory under <id>/.
function codecDir(codec) {
  switch (codec.kind) {
    case 'mic':
    case 'micwasm':
    case 'miccwasm':
      return 'mic';
    case 'pics':
    case 'picscwasm':
      return 'pics';
    case 'wasm':
      return codec.manifestKey; // 'htj2k' | 'jls' | 'jxl'
    default:
      throw new Error(`unknown codec kind for S3 path: ${codec.kind}`);
  }
}

// Frame number -> the _fNNN suffix used by mic-pacs-encode / mic-pacs-refgen.
// MUST match cmd/mic-pacs-encode main.go's per-frame naming and pacs-model.mjs
// cineFrameName().
const frameSuffix = (i) => `_f${String(i).padStart(3, '0')}`;
const frameImgName = (studyId, i) => `${studyId}${frameSuffix(i)}`;

// Recover the OUTPUT frame index from an artifact's key/filename.
//
// Manifests record two different numbers and they are NOT interchangeable:
// `frame` is the index of the slice in the original DICOM series, while the
// filename carries the index within the encoded output. They coincide only
// when every slice was encoded. Deep series are sampled (16 frames out of a
// 116-slice MR), so the manifest says frame=108 while the file is _f015.
// Paths must always be built from this, never from `frame`.
function outFrameFromPath(pathOrKey) {
  if (!pathOrKey) return null;
  const m = /_f(\d+)/.exec(String(pathOrKey).split('/').pop());
  return m ? parseInt(m[1], 10) : null;
}

// Build the path resolver closure for one study. Same signature as
// pacs-runner.mjs's candidatePaths(codec, imgName) — returns an array of
// candidate paths (first existing wins in fetchBytes). S3 layout is exact, so
// always exactly one path.
//
// `nameToFrame` maps the runner's image name to its frame index (-1 for
// single-frame). Reference codecs (htj2k/jls/jxl) are always emitted as
// per-frame files (<id>_fNNN.<ext>) even when the MIC image name is the bare
// study id, so the resolver needs the frame index to build the correct
// reference filename.
export function makeS3PathResolver(studyId, nameToFrame = new Map()) {
  return (codec, imgName) => {
    const dir = codecDir(codec);
    const ext = codec.kind === 'wasm' ? codec.ext : 'mic';
    const suffix = codec.suffix || '';
    if (codec.kind === 'wasm') {
      // Reference codecs: always per-frame filename <id>_fNNN.<ext>.
      const fr = nameToFrame.get(imgName);
      const baseName = fr != null && fr >= 0
        ? frameImgName(studyId, fr)
        : (imgName && imgName !== studyId ? imgName : frameImgName(studyId, 0));
      return [`${studyId}/${dir}/${baseName}.${ext}`];
    }
    return [`${studyId}/${dir}/${imgName}${suffix}.${ext}`];
  };
}

// Fetch JSON, return null on any failure (404, network, parse) — matches
// pacs-runner.mjs fetchJSON semantics so callers don't need try/catch.
// Exception: a WAF Challenge (202 + x-amzn-waf-action: challenge) is
// re-thrown as ChallengeExpiredError so the dashboard can abort + reload.
// Inert in local dev (python3 serve.py never returns 202). See design §4.
async function fetchJSON(url, fetchFn) {
  try {
    const resp = await fetchFn(url);
    if (resp.status === 202 && resp.headers.get('x-amzn-waf-action') === 'challenge') {
      throw new ChallengeExpiredError(url);
    }
    if (!resp.ok) return null;
    return await resp.json();
  } catch (e) {
    if (e instanceof ChallengeExpiredError) throw e;
    return null;
  }
}

// Resolve a possibly-relative base URL to an absolute one, using the page's
// location as the base (mirrors pacs-runner.mjs's baseUrl default). In Node
// (no location), the caller must pass an absolute base.
function absBase(b) {
  if (/^https?:\/\//.test(b) || /^file:\/\//.test(b)) return b;
  if (typeof location !== 'undefined') return new URL(b, location.href).href;
  return b; // let the subsequent new URL() throw with a clear message if still relative
}

// List studies for the picker. Tries /api/studies first (the Lambda); if the
// API is unreachable (e.g. running locally without the stack deployed), falls
// back to the root manifest.json served straight from /data/ — which works in
// dev too, since python3 serve.py serves web/testdata/manifest.json.
export async function listStudies({ apiBaseUrl = '/api/', dataBaseUrl = '/data/', fetchFn } = {}) {
  const f = fetchFn ?? fetch;
  const apiStudies = await fetchJSON(new URL('studies', absBase(apiBaseUrl)).href, f);
  if (apiStudies && Array.isArray(apiStudies.studies)) {
    return { studies: apiStudies.studies, source: 'api' };
  }
  // Fallback: read the root manifest directly from the data origin.
  const manifest = await fetchJSON(new URL('manifest.json', absBase(dataBaseUrl)).href, f);
  if (manifest && Array.isArray(manifest.entries)) {
    return { studies: manifest.entries.map(projectStudyEntry), source: 'manifest' };
  }
  return { studies: [], source: 'none' };
}

// Project a raw manifest entry into the picker shape (mirrors the Lambda's
// projectStudy in infra/api/index.js so both paths produce identical shapes).
function projectStudyEntry(e) {
  const rep = e.representative || {};
  return {
    id: e.id,
    modality: rep.modality || null,
    modalityLabel: e.modality_label || null,
    tier: e.tier || null,
    license: e.license || null,
    attribution: e.attribution || null,
    note: e.note || null,
    files: e.files ?? null,
    bytes: e.bytes ?? null,
    representative: {
      rows: rep.rows ?? null,
      cols: rep.cols ?? null,
      frames: rep.frames ?? null,
      bits: rep.bits ?? null,
      photometric: rep.photometric || null,
      transferSyntaxName: rep.transfer_syntax_name || null,
      lossy: !!rep.lossy,
    },
  };
}

// Load one study: fetch its mic-manifest.json + ref-manifest.json and build
// everything runBenchmark() needs — the images/cine arrays, the path resolver,
// and the manifest lookups for verification + informational codecs.
//
// Returns:
//   {
//     study,            // picker-shape entry (id, modality, license, ...)
//     images,           // IMAGES-shape array: one entry per frame (or one for
//                       //   single-frame studies)
//     cine,             // CINE_DATASETS-shape array: [] for single-frame, or
//                       //   [one entry] for multiframe/series
//     resolvePath,      // (codec, imgName) -> [s3Path]  (candidatePaths shape)
//     rawManifest,      // { images: { [imgName]: { checksum } } } for verify
//     refManifest,      // { images: { [imgName]: { [codecKey]: { bytes } } } }
//                       //   for informational codecs (JXL)
//     micManifest,      // raw per-study mic-manifest.json (for debugging/UI)
//     refManifestRaw,   // raw per-study ref-manifest.json
//   }
export async function loadStudy(studyId, {
  dataBaseUrl = '/data/', fetchFn,
} = {}) {
  const f = fetchFn ?? fetch;
  const base = absBase(dataBaseUrl);

  const [micManifest, refManifestRaw, rootManifest] = await Promise.all([
    fetchJSON(new URL(`${studyId}/mic-manifest.json`, base).href, f),
    fetchJSON(new URL(`${studyId}/ref-manifest.json`, base).href, f),
    fetchJSON(new URL('manifest.json', base).href, f),
  ]);

  if (!micManifest) {
    throw new Error(`study "${studyId}" has no mic-manifest.json under ${dataBaseUrl}`);
  }

  // Study-level metadata: prefer the root manifest entry (has license/
  // attribution/tier), fall back to the mic-manifest's top-level fields.
  const rootEntry = rootManifest?.entries?.find((e) => e.id === studyId) || {};
  const study = {
    ...projectStudyEntry({ ...rootEntry, id: studyId }),
    ...(micManifest ? {
      tier: micManifest.tier ?? rootEntry.tier,
      license: micManifest.license ?? rootEntry.license,
      attribution: micManifest.attribution ?? rootEntry.attribution,
    } : {}),
  };

  const rep = study.representative || {};
  const w = rep.cols ?? 0;
  const h = rep.rows ?? 0;
  const bits = rep.bits ?? 16;
  const modality = study.modality || (study.modalityLabel ? study.modalityLabel.split(' ')[0] : 'OT');

  // Discover the frame set from the mic-manifest artifacts. frame === -1 (or
  // absent) means single-frame OR the multi-frame MIC2 container artifact;
  // frame >= 0 means per-frame MIC1 (multiframe or series-as-frames). We take
  // the union of frames seen across all artifacts.
  //
  // CRITICAL: the artifact's `frame` field is the SOURCE slice index, but the
  // file on disk is numbered by OUTPUT index. Deep series are sampled — a
  // 116-slice MR yields 16 encoded frames, so source frames 0,7,14…108 are
  // written as _f000…_f015. Naming images from `frame` asks for _f108, which
  // has never existed, and every fetch 404s ("no artifact for this codec", for
  // every codec and every frame). Read the output index off the artifact's own
  // key, which is authoritative.
  const frameSet = new Set();
  for (const a of micManifest.artifacts || []) {
    const fr = outFrameFromPath(a.key) ?? a.frame ?? -1;
    frameSet.add(fr);
  }
  // If per-frame artifacts exist, drop the frame=-1 container from the per-
  // image set — the dashboard decodes one frame at a time, and the container
  // (rawBytes = sum of all frames) would either error or only yield frame 0
  // when decoded as a single image. The container is only useful for a
  // "decode whole volume" mode the dashboard doesn't have.
  if (frameSet.has(-1) && frameSet.size > 1) frameSet.delete(-1);
  const frames = [...frameSet].sort((a, b) => a - b);
  const isMulti = frames.some((fr) => fr >= 0);

  // Build the IMAGES-shape array. For single-frame: one entry with
  // name = studyId. For multiframe/series: one entry per frame with
  // name = `${studyId}_f${NNN}` — exactly what makeS3PathResolver expects
  // and what cineFrameName() would produce.
  const nameToFrame = new Map();
  const images = frames.map((fr) => {
    const name = fr < 0 ? studyId : frameImgName(studyId, fr);
    nameToFrame.set(name, fr);
    return {
      name,
      modality,
      w,
      h,
      frame: fr,
      studyId,
    };
  });

  // Build the CINE_DATASETS-shape array. Only multiframe/series studies get a
  // cine entry; single-frame studies get [] (the dashboard runs them in the
  // per-image section only).
  let cine = [];
  if (isMulti) {
    const positiveFrames = frames.filter((fr) => fr >= 0);
    cine = [{
      id: studyId,
      label: study.modalityLabel || `${modality} series`,
      modality,
      frames: positiveFrames.length,
      w,
      h,
      bits,
      pics: 8, // preferred strip count for large images; resolver ignores this
      _frameIndices: positiveFrames, // used by our cineFrameImages override below
    }];
  }

  // Pixel-verification manifest: { images: { [imgName]: { checksum } } }.
  // The mic-manifest carries one pixelChecksum per artifact; all variants of
  // the same frame share the same checksum, so take the first per frame.
  const rawImages = {};
  for (const a of micManifest.artifacts || []) {
    // Output index, not a.frame — see outFrameFromPath. Keying by the source
    // index would file every checksum under a name no image ever has, so
    // verification would silently report "no checksum" for the whole study.
    const fr = outFrameFromPath(a.key) ?? a.frame ?? -1;
    const imgName = fr < 0 ? studyId : frameImgName(studyId, fr);
    if (rawImages[imgName]) continue; // first wins
    let checksum = a.pixelChecksum;
    // MIC2 multi-frame container stores per-frame checksums as an array;
    // the per-frame MIC1 artifacts (also present) carry scalars, so this is
    // only hit for the container artifact itself (imgName = studyId).
    if (Array.isArray(checksum)) {
      // frame -1 container with per-frame checksums: use frame 0's for the
      // study-level entry (the container isn't decoded as a single image in
      // the per-image table; the per-frame entries handle each frame).
      checksum = checksum[0] ?? null;
    }
    if (checksum) rawImages[imgName] = { checksum };
  }
  const rawManifest = { images: rawImages };

  // Reference-codec manifest: { images: { [imgName]: { [codecKey]: { bytes } } } }.
  // The ref-manifest is per-frame: frames[].{htj2k,jls,jxl}.{bytes,file,...}.
  // Naming asymmetry: single-frame MIC uses frame=-1 (imgName = studyId), but
  // the reference encoders always emit per-frame files (frame=0 ->
  // <id>_f000). So for a single-frame study the ref entry lives under
  // <id>_f000 while the runner's image name is <id>. Map both so whichever
  // name the runner uses resolves.
  const refImages = {};
  if (refManifestRaw && Array.isArray(refManifestRaw.frames)) {
    for (const fr of refManifestRaw.frames) {
      // Same source-vs-output skew as the mic manifest: fr.frame is the source
      // slice index while fr.<codec>.file carries the output index. Prefer the
      // filename from whichever codec recorded one.
      const outIdx = outFrameFromPath(fr.htj2k?.file || fr.jls?.file || fr.jxl?.file)
        ?? fr.frame;
      const frameName = frameImgName(studyId, outIdx);
      const entry = {};
      for (const key of ['htj2k', 'jls', 'jxl']) {
        const c = fr[key];
        if (c && c.bytes != null) entry[key] = { bytes: c.bytes };
      }
      refImages[frameName] = entry;
      // Single-frame study (mic frame=-1 -> imgName=studyId): also key the ref
      // entry under the bare studyId so the runner's image name resolves.
      if (outIdx === 0 && !isMulti) {
        refImages[studyId] = entry;
      }
    }
  }
  const refManifest = { images: refImages };

  return {
    study,
    images,
    cine,
    resolvePath: makeS3PathResolver(studyId, nameToFrame),
    rawManifest,
    refManifest,
    micManifest,
    refManifestRaw,
  };
}

// Cine frame-image builder for S3 studies. Mirrors pacs-model.mjs
// cineFrameImages() but uses the study's actual frame indices (the mic-manifest
// may not include every frame for every variant — e.g. deep series with capped
// reference sampling). Falls back to the standard 0..N-1 range when the study
// didn't record explicit frame indices.
export function studyCineFrameImages(ds) {
  const frames = ds._frameIndices
    ? ds._frameIndices
    : Array.from({ length: ds.frames }, (_, i) => i);
  return frames.map((i) => ({
    name: frameImgName(ds.id, i),
    modality: ds.modality,
    w: ds.w,
    h: ds.h,
    cine: ds.id,
    frameIndex: i,
  }));
}