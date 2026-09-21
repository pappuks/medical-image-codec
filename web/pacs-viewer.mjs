// pacs-viewer.mjs — the half of the demo that shows the pictures.
//
// pacs-dashboard.html is a benchmark harness: it decodes every image with every
// codec and prints tables of ms / MB/s / ratios, but never draws a pixel.
// index.html *is* a viewer (window/level, canvas, cine) but only reads local
// testdata/ and has no notion of the S3 dataset. This page closes that gap:
// pick a study from the S3 dataset, decode the selected frame in the browser,
// paint it on a canvas, and show the decode cost of the frame on screen.
//
// Almost nothing here is new. Study listing, per-study manifests, S3 path
// resolution, decoding, checksums, and the WAF challenge guard all live in
// modules the dashboard already uses. This file wires them together behind a
// viewer UI. Design source of truth: docs/pacs-viewer-design.md.
//
// Plan: .claude/agent-workspace/plan-viewer.md (do not redesign, do not expand
// scope). Adapter contract: web/codecs/codec-interface.md.

import {
  CODEC_REGISTRY, fnv1a32Hex,
  throwIfChallenged, ChallengeExpiredError,
} from './pacs-model.mjs';
import { listStudies, loadStudy } from './pacs-study-source.mjs';
import { makeAdapter } from './codecs/index.mjs';

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);

// /data/* is served by CloudFront → S3 (OAC) in the deployed demo. In local
// dev under python3 serve.py, /data/ is absent and listStudies falls back to
// the root manifest.json in web/testdata/ — so the page degrades to a clear
// "no studies available" state rather than throwing (plan: verification).
const DATA_BASE = '/data/';
// Absolute form of DATA_BASE. new URL(path, '/data/') throws "Invalid URL" —
// a *relative* string is not a valid base — so every frame fetch threw before
// issuing a request, and the catch in fetchFrameBytes turned it into a silent
// "not available for this codec" with no network activity to point at.
// loadStudy avoided this because pacs-study-source.mjs resolves its base
// through absBase(); this module has to do the same.
const DATA_BASE_ABS = typeof location !== 'undefined'
  ? new URL(DATA_BASE, location.href).href
  : DATA_BASE;

// Re-time discipline mirrors the dashboard (pacs-runner.mjs timeMedian): a few
// warmup decodes, then a median of several timed ones, yielding to the event
// loop between decodes so the UI stays responsive without distorting the
// number. Navigation/playback show a single real decode, labelled as such —
// presenting a one-shot as a median would overstate its precision (design §4).
const RETIME_WARMUP = 3;
const RETIME_ITERS = 9;

// ---------------------------------------------------------------------------
// Mutable state. Kept deliberately small; all decode/fetch paths reset the
// relevant pieces on study/codec/frame change.
// ---------------------------------------------------------------------------

// The loaded study: the { images, cine, resolvePath, rawManifest, refManifest,
// study } bundle returned by loadStudy(). Null before a study is selected.
let activeStudy = null;
// Adapter for the currently selected codec. Created lazily on codec select;
// disposed and replaced on change. One at a time — init() spins up WASM and,
// for PICS, a worker pool, so initialising all registry entries up front would
// be slow and memory-hungry for a page showing one codec at a time (design §4).
let activeAdapter = null;
let activeCodecEntry = null;

// Monotonic token cancelling in-flight decodes. showFrame captures it on entry
// and abandons after any await if a newer call, a codec switch, or a study
// switch has bumped it — otherwise concurrent decodes race and the slowest one
// paints last, showing the wrong frame under the right stats.
let decodeGen = 0;

// Monotonic token identifying the current playback chain, so a suspended step
// from a previous play cannot schedule a second concurrent timer chain.
let playRun = 0;

// Cached compressed bytes keyed by `<codecId>:<imgName>`. Decode cost is what
// this page claims to show, so bytes are cached (cine re-fetching the same
// frame each loop would measure the network, not the codec) but decodes are
// always fresh (design §4). A Map of Uint8Array | null (null = confirmed 404).
const byteCache = new Map();

// Window/level is computed ONCE per study from the first decoded frame and held
// for the study, then recomputed only when the study changes. Recomputing per
// frame makes a cine loop flicker as the intensity mapping shifts under the eye
// (design §4). Manual slider movement overrides; "reset" returns to the held
// study-level value.
let studyWindowLevel = null; // { window, level } computed for the study
let manualWL = false; // true once the user touches a slider

// Cine playback. Steps through frames with a short timer, prefetching the next
// frame's bytes while the current one is displayed. Stops at the end (no loop)
// — design §4 leaves loop/stop to the implementer; we stop and set the button
// state honestly to "play" so the user sees playback ended.
let playing = false;
let playTimer = null;
let currentFrameIndex = 0; // index into activeStudy.images

// The last decoded pixels + dims, kept so a window/level slider move can
// re-paint without re-decoding.
let currentPixels = null;
let currentWidth = 0;
let currentHeight = 0;

// ---------------------------------------------------------------------------
// WAF Challenge handling.
//
// Every fetch goes through throwIfChallenged() BEFORE any resp.ok check — a
// WAF challenge is HTTP 202 with x-amzn-waf-action: challenge, and resp.ok is
// TRUE for 202, so it would sail past an `if (!resp.ok) return null` guard and
// hand an empty body to a decoder (surfaces as a checksum mismatch or a
// WASM/syntax error: indistinguishable from a real codec bug). On
// ChallengeExpiredError we redirect to /bootstrap.html?next=<here>, NEVER
// location.reload() — this page is served with COEP: require-corp, which
// blocks the cross-origin script in AWS's challenge interstitial, so reloading
// in place renders blank (design §5, plan: required cross-cutting behaviour).
// Mirrors pacs-dashboard.mjs handleChallengeExpired, minus the benchmark UI.
// ---------------------------------------------------------------------------
function handleChallengeExpired(e) {
  stopPlayback();
  setStatus(`Browser verification needed, redirecting… (${e.message})`);
  // Cap attempts so a persistently failing verification can't trap the user in
  // a redirect loop with no way to read the error.
  let tries = 0;
  try { tries = Number(sessionStorage.getItem('wafReloadTries') || 0); } catch { /* private mode */ }
  if (tries >= 2) {
    try { sessionStorage.removeItem('wafReloadTries'); } catch { /* ignore */ }
    setStatus('Browser verification keeps failing. Please reload the page manually.');
    return;
  }
  try { sessionStorage.setItem('wafReloadTries', String(tries + 1)); } catch { /* ignore */ }
  const next = encodeURIComponent(location.pathname + location.search);
  setTimeout(() => location.replace(`/bootstrap.html?next=${next}`), 1200);
}

// Fetch a frame's compressed bytes for the active codec. Goes through the WAF
// guard, caches the result (including confirmed 404s as null so we don't
// re-fetch a known-missing artifact every frame), and rethrows challenge
// errors so the run boundary handler can redirect. Mirrors pacs-runner.mjs
// fetchBytes but with the byte cache layered in.
async function fetchFrameBytes(imgName) {
  const key = `${activeCodecEntry.id}:${imgName}`;
  if (byteCache.has(key)) return byteCache.get(key);
  let bytes = null;
  try {
    for (const p of activeStudy.resolvePath(activeCodecEntry, imgName)) {
      const url = new URL(p, DATA_BASE_ABS).href;
      const t0 = performance.now();
      const resp = await fetch(url);
      const transferMs = performance.now() - t0;
      throwIfChallenged(resp, url); // must precede resp.ok — 202 is "ok" (§4)
      if (!resp.ok) { continue; }
      bytes = new Uint8Array(await resp.arrayBuffer());
      // Stash the transfer time alongside so the stats panel can show it (or
      // "cached" on subsequent fetches). Stored on the cache entry via a
      // parallel map to keep the value Uint8Array | null shape clean.
      firstTransferMs.set(key, transferMs);
      break;
    }
  } catch (e) {
    if (e instanceof ChallengeExpiredError) throw e;
    // Network errors are treated as "missing" — a codec with no artifact for a
    // frame shows "not available" rather than erroring (design §5).
    bytes = null;
  }
  byteCache.set(key, bytes);
  return bytes;
}
// First-fetch transfer time per cache key, so the stats panel can show the
// real first transfer and "cached" thereafter (design: compressed bytes are
// cached, decodes are not).
const firstTransferMs = new Map();

// ---------------------------------------------------------------------------
// Rendering — ported from index.html (~lines 307–370). Copied, not imported,
// because index.html is a non-module script with no exports and the plan
// explicitly says to copy the algorithm.
// ---------------------------------------------------------------------------

function renderImage(pixels, width, height, window, level) {
  const canvas = $('viewport-canvas');
  canvas.width = width;
  canvas.height = height;
  canvas.style.display = 'block';
  $('placeholder').style.display = 'none';

  const ctx = canvas.getContext('2d');
  const imgData = ctx.createImageData(width, height);
  const data = imgData.data;

  const low = level - window / 2;
  const high = level + window / 2;
  const range = high - low || 1;

  for (let i = 0; i < pixels.length; i++) {
    const v = pixels[i];
    let gray;
    if (v <= low) gray = 0;
    else if (v >= high) gray = 255;
    else gray = ((v - low) / range * 255) | 0;
    const j = i * 4;
    data[j] = gray;
    data[j + 1] = gray;
    data[j + 2] = gray;
    data[j + 3] = 255;
  }
  ctx.putImageData(imgData, 0, 0);
}

// Auto window/level from decoded pixels. Samples every 4th value on large
// images (>100k px) for speed — same heuristic as index.html. Computed ONCE
// per study from the first decoded frame (design §4), held in
// studyWindowLevel, and not recomputed per frame (that flicker is the reason
// this is called out).
function autoWindowLevel(pixels) {
  let min = 65535, max = 0;
  const step = pixels.length > 100000 ? 4 : 1;
  for (let i = 0; i < pixels.length; i += step) {
    const v = pixels[i];
    if (v < min) min = v;
    if (v > max) max = v;
  }
  const window = max - min || 1;
  const level = (min + max) >>> 1;
  return { window, level };
}

function paintWithCurrentWL() {
  if (!currentPixels) return;
  const w = manualWL ? parseInt($('wl-window').value, 10) : studyWindowLevel.window;
  const l = manualWL ? parseInt($('wl-level').value, 10) : studyWindowLevel.level;
  $('wl-window-val').textContent = w;
  $('wl-level-val').textContent = l;
  renderImage(currentPixels, currentWidth, currentHeight, w, l);
}

// ---------------------------------------------------------------------------
// Stats panel — describes the frame currently on screen. Updates on every
// frame during cine playback so the number and the picture it came from are
// visible at the same time (design §3).
// ---------------------------------------------------------------------------

function fmtBytes(b) {
  if (b == null) return '—';
  if (b < 1024) return b + ' B';
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
  return (b / (1024 * 1024)).toFixed(2) + ' MB';
}
function fmtMs(ms) {
  if (ms == null) return '—';
  return ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(2)} s`;
}
function fmtThroughput(rawBytes, decodeMs) {
  if (!decodeMs || !rawBytes) return '—';
  const mb = rawBytes / (1024 * 1024);
  return `${(mb / (decodeMs / 1000)).toFixed(0)} MB/s`;
}
function fmtRatio(raw, comp) {
  if (!comp) return '—';
  return `${(raw / comp).toFixed(2)}×`;
}

// One frame's stats. `decodeMs` is a single real decode unless `isMedian` is
// set (re-time action), in which case the panel labels it as a median (design
// §4 — do not present a one-shot number as a median).
function renderStats({
  decodeMs, rawBytes, compressedBytes, transferMs, fromCache,
  verify, isMedian, note,
}) {
  $('st-decode').textContent = decodeMs == null ? '—' : `${fmtMs(decodeMs)}${isMedian ? ' (median)' : ' (one-shot)'}`;
  $('st-throughput').textContent = fmtThroughput(rawBytes, decodeMs);
  $('st-transfer').textContent = transferMs == null ? '—' : (fromCache ? 'cached' : fmtMs(transferMs));
  $('st-compressed').textContent = fmtBytes(compressedBytes);
  $('st-raw').textContent = fmtBytes(rawBytes);
  $('st-ratio').textContent = fmtRatio(rawBytes, compressedBytes);

  const vEl = $('st-verify');
  if (note === 'not-available') {
    vEl.textContent = 'not available for this codec';
    vEl.className = 'v v-warn';
  } else if (verify === true) {
    vEl.textContent = 'verified';
    vEl.className = 'v v-ok';
  } else if (verify === false) {
    vEl.textContent = 'MISMATCH';
    vEl.className = 'v v-bad';
  } else if (verify === null) {
    vEl.textContent = 'no checksum';
    vEl.className = 'v';
  } else {
    vEl.textContent = '—';
    vEl.className = 'v';
  }
}

function clearStats() {
  for (const id of ['st-decode', 'st-throughput', 'st-transfer', 'st-compressed', 'st-raw', 'st-ratio', 'st-verify']) {
    $(id).textContent = '—';
  }
}

// ---------------------------------------------------------------------------
// Frame decode + paint. The central action of the page: fetch the compressed
// bytes (cached), decode fresh, paint, update stats. Never re-decode for a
// window/level change (that just re-paints); always re-decode for a frame or
// codec change (that's what the page measures).
// ---------------------------------------------------------------------------

async function showFrame(frameIndex, { retime = false } = {}) {
  if (!activeStudy || !activeAdapter || !activeCodecEntry) return;
  const img = activeStudy.images[frameIndex];
  if (!img) return;
  currentFrameIndex = frameIndex;

  // Generation guard. showFrame awaits a fetch and a decode, and the user can
  // drag the slider, switch codec, or switch study during either. Without this,
  // several showFrame calls run concurrently and whichever finishes LAST paints
  // — so a slider drag from frame 5 to 8 can leave frame 5 on the canvas under
  // frame 8's stats. Capture the generation on entry and abandon after every
  // await if a newer call superseded us. selectCodec/selectStudy bump the
  // counter to cancel everything in flight.
  const gen = ++decodeGen;
  // Pin the adapter and codec for this call. Reading the module-level values
  // after an await would let a codec switch mid-flight decode codec A's bytes
  // with codec B's decoder — a format mismatch at best, and silently wrong
  // pixels labelled with the wrong codec at worst.
  const adapter = activeAdapter;
  const codec = activeCodecEntry;

  const rawBytes = img.w * img.h * 2;
  const cacheKey = `${codec.id}:${img.name}`;

  let bytes;
  let fromCache = false;
  let transferMs = null;
  try {
    bytes = await fetchFrameBytes(img.name);
  } catch (e) {
    if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e); return; }
    if (gen !== decodeGen) return;
    setStatus('Error fetching frame: ' + e.message);
    return;
  }
  if (gen !== decodeGen) return; // superseded while fetching
  if (byteCache.has(cacheKey) && firstTransferMs.has(cacheKey) && bytes === byteCache.get(cacheKey)) {
    // Served from the byte cache — transfer shows "cached" (design §4).
    fromCache = true;
  } else {
    transferMs = firstTransferMs.get(cacheKey) ?? null;
  }

  // Missing artifact for this frame+codec (404): show "not available", leave
  // the previous image on the canvas, do not throw (design §5).
  if (!bytes) {
    renderStats({ decodeMs: null, rawBytes, compressedBytes: null, transferMs, fromCache, note: 'not-available' });
    setStatus(`No artifact for ${img.name} under ${codec.label}.`);
    return;
  }

  // Decode fresh every time — decode cost is what the page claims to show.
  let decodeMs;
  let pixelsResult;
  try {
    if (retime) {
      decodeMs = await timeMedianDecode(adapter, bytes);
      // Re-time discards its results; decode once more for display, outside
      // the timing loop so it can't pollute the median. Inside the try: a
      // codec switch mid-re-time disposes the adapter, and this call would
      // otherwise reject unhandled.
      pixelsResult = await adapter.decode(bytes);
    } else {
      const t0 = performance.now();
      pixelsResult = await adapter.decode(bytes);
      decodeMs = performance.now() - t0;
    }
  } catch (e) {
    if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e); return; }
    if (gen !== decodeGen) return; // disposed out from under us; a newer call owns the UI
    setStatus('Decode error: ' + e.message);
    return;
  }
  if (gen !== decodeGen) return; // superseded while decoding

  const { pixels, width, height } = pixelsResult;
  currentPixels = pixels;
  currentWidth = width;
  currentHeight = height;

  // Pixel verification: fnv1a32 over the decoded pixel bytes compared against
  // rawManifest.images[imageName].checksum. A mismatch is visually obvious —
  // this is a correctness demo (plan: stats panel).
  let verify = null;
  const expected = activeStudy.rawManifest?.images?.[img.name]?.checksum;
  if (expected) {
    const actual = fnv1a32Hex(new Uint8Array(pixels.buffer, pixels.byteOffset, pixels.byteLength));
    verify = actual === expected;
  }

  // First decoded frame of a study: compute window/level and hold it for the
  // whole study (design §4). Don't recompute on subsequent frames — the
  // flicker is the reason this is called out.
  if (!studyWindowLevel) {
    studyWindowLevel = autoWindowLevel(pixels);
    if (!manualWL) {
      $('wl-window').value = studyWindowLevel.window;
      $('wl-level').value = studyWindowLevel.level;
    }
  }
  paintWithCurrentWL();

  renderStats({
    decodeMs, rawBytes, compressedBytes: bytes.length,
    transferMs, fromCache, verify, isMedian: retime,
  });
  setStatus(`Showing ${img.name} (${width}×${height}) via ${codec.label}.`);
  syncTransport();
}

// Warmup + median timing, mirroring pacs-runner.mjs timeMedian. Yields to the
// event loop between decodes so large images (up to 2670×3340) don't lock the
// UI during a re-time (plan: constraints). Returns the median decode ms.
const nextFrame = () => new Promise((r) => requestAnimationFrame(() => r()));
async function timeMedianDecode(adapter, bytes) {
  for (let i = 0; i < RETIME_WARMUP; i++) await adapter.decode(bytes);
  const times = [];
  for (let i = 0; i < RETIME_ITERS; i++) {
    const t0 = performance.now();
    await adapter.decode(bytes);
    times.push(performance.now() - t0);
    await nextFrame();
  }
  times.sort((a, b) => a - b);
  return times[Math.floor(times.length / 2)];
}

// ---------------------------------------------------------------------------
// Cine playback. Steps through frames, prefetching the next frame's bytes
// while the current one is displayed. Stops at the end (no loop) and sets the
// button state honestly so the user sees playback ended.
// ---------------------------------------------------------------------------

function syncTransport() {
  if (!activeStudy) { $('transport').hidden = true; return; }
  const single = activeStudy.images.length <= 1;
  $('transport').hidden = single;
  if (single) return;
  const slider = $('frame-slider');
  slider.max = String(activeStudy.images.length - 1);
  slider.value = String(currentFrameIndex);
  $('frame-label').textContent = `${currentFrameIndex + 1}/${activeStudy.images.length}`;
  $('btn-prev').disabled = playing || currentFrameIndex <= 0;
  $('btn-next').disabled = playing || currentFrameIndex >= activeStudy.images.length - 1;
  $('btn-play').textContent = playing ? '❚❚ pause' : '▶ play';
}

function stopPlayback() {
  playing = false;
  // Bump the run id so any playbackStep already past its `playing` check and
  // sitting on an await abandons instead of scheduling another tick.
  playRun++;
  if (playTimer) { clearTimeout(playTimer); playTimer = null; }
  syncTransport();
}

async function playbackStep(run) {
  if (run !== playRun || !playing || !activeStudy) return;
  const next = currentFrameIndex + 1;
  if (next >= activeStudy.images.length) { stopPlayback(); return; }

  // Prefetch the next frame's bytes while the current one is still displayed
  // — keeps playback smooth and means the next decode isn't gated on a fetch
  // (plan: behaviour 5).
  const nextImg = activeStudy.images[next];
  if (nextImg) {
    try { await fetchFrameBytes(nextImg.name); }
    catch (e) {
      if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e); return; }
    }
  }
  if (run !== playRun) return; // paused or restarted while prefetching

  await showFrame(next);
  // Yield to the event loop between decodes so large images don't lock the UI
  // (plan: constraints). A short cadence; the point is to see motion + the
  // per-frame decode cost, not real-time playback.
  //
  // The run check matters as much as `playing`: a fast pause→play leaves the
  // OLD step suspended here, and without it both the old and new chains would
  // schedule timers, giving two loops stepping the same study.
  if (run === playRun && playing) playTimer = setTimeout(() => playbackStep(run), 80);
}

function togglePlay() {
  if (!activeStudy || activeStudy.images.length <= 1) return;
  if (playing) { stopPlayback(); return; }
  // If at the end, restart from frame 0 when the user hits play again.
  if (currentFrameIndex >= activeStudy.images.length - 1) {
    currentFrameIndex = 0;
  }
  playing = true;
  const run = ++playRun;
  syncTransport();
  playbackStep(run);
}

// ---------------------------------------------------------------------------
// Codec selection. dispose() the old adapter, init() the new one, then
// re-decode the current frame so the numbers are comparable on the same image
// (plan: behaviour 4). Adapters initialise lazily, one at a time (design §4).
// ---------------------------------------------------------------------------

async function selectCodec(entry) {
  // Dispose the previous adapter first — frees the PICS worker pool / WASM
  // before instantiating the next (design §4: init on selection, dispose the
  // previous one).
  // Cancel any decode in flight before tearing down the adapter it is using.
  decodeGen++;
  // Drop the outgoing codec's cached bytes. The cache is keyed by codec, so
  // without this, stepping through all 11 codecs on a 69-frame study would
  // retain every frame of every codec — hundreds of megabytes for no benefit,
  // since the page decodes one codec at a time.
  if (activeCodecEntry) {
    const stale = `${activeCodecEntry.id}:`;
    for (const k of byteCache.keys()) if (k.startsWith(stale)) byteCache.delete(k);
    for (const k of firstTransferMs.keys()) if (k.startsWith(stale)) firstTransferMs.delete(k);
  }
  if (activeAdapter?.dispose) {
    try { activeAdapter.dispose(); } catch { /* best-effort teardown */ }
  }
  activeAdapter = null;
  activeCodecEntry = entry;

  if (!entry) return;
  const adapter = makeAdapter(entry);
  try {
    await adapter.init();
  } catch (e) {
    // WASM codecs fetch their .wasm/.js during init(), so a WAF challenge can
    // surface here. Without this branch the challenge is reported as a generic
    // init failure and the user is stranded on a dead page with no redirect —
    // the exact outcome the challenge handling exists to prevent.
    if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e); return; }
    setStatus(`Codec ${entry.label} failed to initialise: ${e.message}`);
    activeCodecEntry = null;
    return;
  }
  activeAdapter = adapter;
  updateUrl();
  setStatus(`Codec ${entry.label} ready. Loading frame…`);
  // Re-decode the current frame so the stats are comparable on the same image.
  await showFrame(currentFrameIndex);
}

// Build the codec picker filtered to LIVE decoders only. Codecs that can't
// decode live are excluded from the picker, not shown disabled — a viewer that
// offers a codec and then refuses to show a picture is worse than one that
// doesn't offer it (design §4). JPEG-XL's browser path returns 8-bit RGBA and
// can't preserve 16-bit grayscale, so it's out.
// Computed once: liveDecodeSupported is static per registry entry, and adapter
// constructors allocate nothing (worker pools and WASM come from init()), but
// rebuilding twelve throwaway adapters on every picker change is pointless.
let _liveCodecs = null;
function liveCodecs() {
  return _liveCodecs ??= CODEC_REGISTRY.filter((entry) => {
    let adapter;
    try { adapter = makeAdapter(entry); } catch { return false; }
    return adapter?.liveDecodeSupported === true;
  });
}

function populateCodecPicker() {
  const sel = $('codecselect');
  const codecs = liveCodecs();
  sel.innerHTML = codecs.map((c) =>
    `<option value="${c.id}">${c.label}</option>`).join('');
  // Setting sel.value programmatically does NOT fire the change event, so the
  // adapter must be initialised explicitly. Without this a ?codec= deep link
  // leaves activeAdapter null, showFrame returns immediately, and the study
  // loads to a blank viewport that only recovers if the user re-picks the
  // codec by hand.
  const pre = params.get('codec');
  const entry = codecs.find((c) => c.id === pre) ?? codecs[0];
  if (!entry) return;
  sel.value = entry.id;
  return selectCodec(entry); // async; caller awaits before selecting a study
}

// ---------------------------------------------------------------------------
// Study selection. loadStudy returns the bundle { images, resolvePath,
// rawManifest, ... }. Frame 0 is shown immediately; single-frame studies hide
// the transport entirely (design §4).
// ---------------------------------------------------------------------------

function renderAttribution(study) {
  const box = $('attribution');
  if (!study) { box.hidden = true; return; }
  const bits = [];
  if (study.license) bits.push(`<strong>License:</strong> ${escapeHtml(study.license)}`);
  if (study.attribution) bits.push(`<strong>Attribution:</strong> ${escapeHtml(study.attribution)}`);
  if (study.tier) bits.push(`<strong>Tier:</strong> ${escapeHtml(study.tier)}${study.tier === 'A' ? ' (lossless ground truth)' : ' (lossy source — demo only)'}`);
  const rep = study.representative || {};
  if (rep.transferSyntaxName) bits.push(`<strong>Transfer syntax:</strong> ${escapeHtml(rep.transferSyntaxName)}`);
  if (study.note) bits.push(`<strong>Note:</strong> ${escapeHtml(study.note)}`);
  box.innerHTML = bits.join(' · ');
  box.hidden = bits.length === 0;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function fmtStudyLabel(s) {
  const rep = s.representative || {};
  const dim = rep.cols && rep.rows ? `${rep.cols}×${rep.rows}` : '—';
  const frames = rep.frames ? ` × ${rep.frames}f` : '';
  const mod = s.modalityLabel || s.modality || '';
  const tier = s.tier ? ` [tier ${s.tier}]` : '';
  return `${s.id} — ${mod} ${dim}${frames}${tier}`;
}

async function selectStudy(studyId) {
  if (!studyId) return;
  stopPlayback();
  setStatus(`Loading study ${studyId}…`);
  let bundle;
  try {
    bundle = await loadStudy(studyId, { dataBaseUrl: DATA_BASE });
  } catch (e) {
    if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e); return; }
    setStatus('Error loading study: ' + e.message);
    return;
  }
  activeStudy = bundle;
  // Per-study state reset. Window/level recomputes from the first decoded
  // frame of the new study; manual override clears (design §4).
  studyWindowLevel = null;
  manualWL = false;
  currentPixels = null;
  currentFrameIndex = 0;
  byteCache.clear();
  firstTransferMs.clear();

  renderAttribution(bundle.study);
  clearStats();
  $('placeholder').style.display = 'block';
  $('viewport-canvas').style.display = 'none';
  syncTransport();

  updateUrl();
  if (!bundle.images.length) {
    setStatus('Study has no frames.');
    return;
  }
  await showFrame(0);
}

// ---------------------------------------------------------------------------
// Picker setup + URL linking.
// ---------------------------------------------------------------------------

async function populateStudyPicker() {
  const sel = $('studyselect');
  sel.innerHTML = '<option value="">Loading…</option>';
  try {
    const { studies, source } = await listStudies({ dataBaseUrl: DATA_BASE });
    if (!studies.length) {
      sel.innerHTML = '<option value="">No studies available</option>';
      setStatus('No studies available. Without the S3 backend the study list is empty — this is expected locally.');
      return;
    }
    studies.sort((a, b) => {
      const ta = (a.tier || 'Z').localeCompare(b.tier || 'Z');
      if (ta) return ta;
      return (a.id || '').localeCompare(b.id || '');
    });
    sel.innerHTML = studies.map((s) =>
      `<option value="${escapeHtml(s.id)}">${escapeHtml(fmtStudyLabel(s))}</option>`).join('');
    const pre = params.get('study');
    if (pre && studies.some((s) => s.id === pre)) {
      sel.value = pre;
      setStatus(`${studies.length} studies available (${source} source).`);
      selectStudy(pre);
    } else {
      setStatus(`${studies.length} studies available (${source} source). Select a study to begin.`);
    }
  } catch (e) {
    if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e); return; }
    sel.innerHTML = '<option value="">Error loading studies</option>';
    setStatus('Error loading studies: ' + e.message);
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
function setStatus(msg) { $('status').textContent = msg; }

// ---------------------------------------------------------------------------
// Wire up
// ---------------------------------------------------------------------------
$('studyselect').addEventListener('change', (e) => selectStudy(e.target.value));
$('codecselect').addEventListener('change', (e) => {
  const entry = liveCodecs().find((c) => c.id === e.target.value);
  selectCodec(entry);
});
$('btn-prev').addEventListener('click', () => {
  if (playing) return;
  if (currentFrameIndex > 0) showFrame(currentFrameIndex - 1);
});
$('btn-next').addEventListener('click', () => {
  if (playing) return;
  if (activeStudy && currentFrameIndex < activeStudy.images.length - 1) showFrame(currentFrameIndex + 1);
});
$('btn-play').addEventListener('click', togglePlay);
$('frame-slider').addEventListener('input', (e) => {
  if (playing) return;
  const idx = parseInt(e.target.value, 10);
  if (!Number.isNaN(idx)) showFrame(idx);
});

// Window/level sliders: manual override takes over from the study-level auto
// value. Recompute only on study change; here we just re-paint.
$('wl-window').addEventListener('input', () => {
  manualWL = true;
  paintWithCurrentWL();
});
$('wl-level').addEventListener('input', () => {
  manualWL = true;
  paintWithCurrentWL();
});
$('wl-reset').addEventListener('click', () => {
  if (!studyWindowLevel) return;
  manualWL = false;
  $('wl-window').value = studyWindowLevel.window;
  $('wl-level').value = studyWindowLevel.level;
  paintWithCurrentWL();
});

// Re-time: warmup + median, labelled as a median (design §4). Disabled during
// playback to avoid overlapping timing loops.
$('retime').addEventListener('click', async () => {
  if (playing || !activeAdapter || !activeStudy) return;
  $('retime').disabled = true;
  setStatus('Re-timing (warmup + median)…');
  try {
    await showFrame(currentFrameIndex, { retime: true });
  } finally {
    $('retime').disabled = false;
  }
});

// Reflect codec into the URL so a view is linkable (plan: behaviour 1).
function updateUrl() {
  const u = new URL(location.href);
  const study = activeStudy?.study?.id;
  const codec = activeCodecEntry?.id;
  if (study) u.searchParams.set('study', study); else u.searchParams.delete('study');
  if (codec) u.searchParams.set('codec', codec); else u.searchParams.delete('codec');
  history.replaceState(null, '', u);
}

// Boot. The codec picker must finish FIRST and be awaited: it initialises the
// adapter, and selectStudy immediately decodes frame 0, which is a no-op while
// activeAdapter is still null. Both pickers honour ?study= and ?codec= so a
// view is linkable.
(async () => {
  await populateCodecPicker();
  await populateStudyPicker();
})();