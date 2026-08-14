// pacs-dashboard.mjs — DOM controller for the PACS benchmark. Owns the
// controls, runs the benchmark via pacs-runner.mjs, renders the environment
// panel, per-image tables, study-level simulation, and (via pacs-charts.mjs)
// charts. Exposes a ?headless=1 machine hook for the Playwright runner.

import {
  IMAGES, QUICK_IMAGE_NAMES, NETWORK_PROFILES, DEFAULT_PROFILE_NAMES,
  CODEC_REGISTRY, STUDIES, simulateStudy,
  CINE_DATASETS, QUICK_CINE_IDS,
  fmtMs, fmtKB, fmtRatio, timeToDisplayMs,
} from './pacs-model.mjs';
import { runBenchmark, ChallengeExpiredError } from './pacs-runner.mjs';
import { listStudies, loadStudy, studyCineFrameImages } from './pacs-study-source.mjs';

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const HEADLESS = params.get('headless') === '1';
const S3_MODE = params.get('source') === 's3';
// In S3 mode, /data/* is served by CloudFront from the studies bucket.
// In dev mode, /data/ is absent — the dashboard uses testdata/ via baseUrl.
const S3_DATA_BASE = '/data/';

// ---------------------------------------------------------------------------
// Controls
// ---------------------------------------------------------------------------
function readOpts() {
  const imgSet = $('imageset').value;
  const quick = imgSet === 'quick';
  const cineOnly = imgSet === 'cine';
  const images = cineOnly
    ? []
    : (quick ? IMAGES.filter((i) => QUICK_IMAGE_NAMES.includes(i.name)) : IMAGES);
  const cine = quick
    ? CINE_DATASETS.filter((d) => QUICK_CINE_IDS.includes(d.id))
    : CINE_DATASETS; // 'full' and 'cine' both run every cine dataset
  return {
    images,
    cine,
    iterations: Math.max(1, parseInt($('iterations').value, 10) || 15),
    warmup: Math.max(0, parseInt($('warmup').value, 10) || 3),
    verify: $('verify').checked,
  };
}

function activeProfiles() {
  return $('allprofiles').checked
    ? NETWORK_PROFILES
    : NETWORK_PROFILES.filter((p) => DEFAULT_PROFILE_NAMES.includes(p.name));
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------
function renderEnv(env) {
  const rows = [
    ['User agent', env.userAgent ?? '—'],
    ['Logical CPUs', env.hardwareConcurrency ?? '—'],
    ['crossOriginIsolated', String(env.crossOriginIsolated ?? '—')],
    ['PICS decode mode', env.picsSabMode == null ? '—' : (env.picsSabMode ? 'SharedArrayBuffer (zero-copy)' : 'transferable fallback')],
  ];
  $('env').innerHTML = rows.map(([k, v]) => `<div><span>${k}:</span> ${escapeHtml(String(v))}</div>`).join('');
}

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function recordsByImage(records) {
  const m = new Map();
  for (const r of records) {
    if (!m.has(r.image)) m.set(r.image, []);
    m.get(r.image).push(r);
  }
  return m;
}

// Active image set for the per-image section. Defaults to the local IMAGES
// (dev/CI); set to the S3 study's images when running in S3 mode, so
// renderResults iterates the actual images that produced records.
let activeImages = IMAGES;

function renderResults(result) {
  const profiles = activeProfiles();
  const byImage = recordsByImage(result.records);
  const parts = [];

  for (const img of activeImages) {
    const recs = byImage.get(img.name);
    if (!recs || recs.every((r) => r.compressedBytes == null)) continue;
    const rawMB = (img.w * img.h * 2 / 1024 / 1024).toFixed(2);

    const head = `<tr>
      <th>Codec</th><th>Size</th><th>Ratio</th><th>Decode</th>
      ${profiles.map((p) => `<th title="${escapeHtml(p.note)}">${escapeHtml(p.name)}</th>`).join('')}
      ${result.verify ? '<th>Verified</th>' : ''}
    </tr>`;

    const body = recs.map((r) => {
      const live = r.liveDecode;
      const sizeCell = r.compressedBytes != null ? fmtKB(r.compressedBytes) : '—';
      const ratioCell = r.ratio != null ? fmtRatio(r.ratio) : '—';
      const decodeCell = r.decodeMs != null ? fmtMs(r.decodeMs) : '—';
      const netCells = profiles.map((p) => {
        if (r.compressedBytes == null || r.decodeMs == null) return '<td>—</td>';
        return `<td>${fmtMs(timeToDisplayMs(r.compressedBytes, r.decodeMs, p))}</td>`;
      }).join('');
      let verifiedCell = '';
      if (result.verify) {
        if (r.pixelsVerified === true) verifiedCell = '<td class="v-ok">✔</td>';
        else if (r.pixelsVerified === false) verifiedCell = `<td class="v-bad" title="${escapeHtml(r.note || '')}">✗</td>`;
        else verifiedCell = '<td>—</td>';
      }
      const badge = live
        ? '<span class="badge live">live</span>'
        : '<span class="badge ref">ref</span>';
      return `<tr class="${live ? '' : 'ref'}">
        <td>${escapeHtml(r.label)} ${badge}</td>
        <td>${sizeCell}</td><td>${ratioCell}</td><td>${decodeCell}</td>
        ${netCells}${verifiedCell}
      </tr>`;
    }).join('');

    parts.push(`<details open>
      <summary>${escapeHtml(img.name)} — ${escapeHtml(img.modality)}, ${img.w}×${img.h}, raw ${rawMB} MB</summary>
      <div class="panel"><div class="tbl-wrap"><table><thead>${head}</thead><tbody>${body}</tbody></table></div></div>
    </details>`);
  }

  // Cine / multi-frame section (every frame decoded independently).
  parts.push(renderCine(result, profiles));

  // Study-level simulation for the two headline MIC variants.
  parts.push(renderStudies(result, profiles));

  $('results').innerHTML = parts.join('');
}

// Format frames-per-second: a whole number when fast, one decimal when slow.
function fmtFps(fps) {
  if (fps == null) return '—';
  return fps >= 10 ? `${fps.toFixed(0)} fps` : `${fps.toFixed(1)} fps`;
}

// renderCine renders the multi-frame section: per dataset, one row per codec
// with total compressed size, ratio, pure decode loop time, frames/s, and the
// full cine-loop time-to-display (download all frames + decode all) per network
// profile. Each frame was fetched and decoded as an independent single-frame
// image, so every codec — MIC 1/4/8-state, PICS, HTJ2K, JPEG-LS, JPEG-XL — is
// exercised per frame exactly as in the single-frame tables.
function renderCine(result, profiles) {
  const cineRecords = result.cineRecords || [];
  if (!cineRecords.length) return '';

  const byCine = new Map();
  for (const r of cineRecords) {
    if (!byCine.has(r.cine)) byCine.set(r.cine, []);
    byCine.get(r.cine).push(r);
  }

  const blocks = [];
  // In S3 mode the cine datasets come from the loaded study, not the local
  // CINE_DATASETS. Build the list from the records themselves so any cine ID
  // is rendered, then enrich with metadata from CINE_DATASETS when available.
  const cineMeta = new Map(CINE_DATASETS.map((d) => [d.id, d]));
  for (const r of cineRecords) {
    if (!cineMeta.has(r.cine)) {
      cineMeta.set(r.cine, {
        id: r.cine, label: r.datasetLabel || r.cine, modality: r.modality,
        frames: r.frames, w: r.width, h: r.height, bits: r.bits,
      });
    }
  }
  for (const ds of cineMeta.values()) {
    const recs = byCine.get(ds.id);
    if (!recs || recs.every((r) => r.compressedBytesTotal == null)) continue;
    const totalRawMB = (ds.w * ds.h * 2 * ds.frames / 1024 / 1024).toFixed(2);

    const head = `<tr>
      <th>Codec</th><th>Total size</th><th>Ratio</th>
      <th title="Pure decode time for all frames (no network)">Loop decode</th>
      <th title="Frames per second (decode only)">Frames/s</th>
      ${profiles.map((p) => `<th title="${escapeHtml(p.note)}">${escapeHtml(p.name)}</th>`).join('')}
      ${result.verify ? '<th>Verified</th>' : ''}
    </tr>`;

    const body = recs.map((r) => {
      const live = r.liveDecode;
      const sizeCell = r.compressedBytesTotal != null ? fmtKB(r.compressedBytesTotal) : '—';
      const ratioCell = r.ratio != null ? fmtRatio(r.ratio) : '—';
      const loopCell = r.loopMs != null
        ? `<span title="${r.msPerFrame != null ? r.msPerFrame.toFixed(2) + ' ms/frame' : ''}">${fmtMs(r.loopMs)}</span>`
        : '—';
      const fpsCell = fmtFps(r.fps);
      const netCells = profiles.map((p) => {
        if (r.compressedBytesTotal == null || r.loopMs == null) return '<td>—</td>';
        return `<td>${fmtMs(timeToDisplayMs(r.compressedBytesTotal, r.loopMs, p))}</td>`;
      }).join('');
      let verifiedCell = '';
      if (result.verify) {
        if (r.pixelsVerified === true) verifiedCell = '<td class="v-ok">✔</td>';
        else if (r.pixelsVerified === false) verifiedCell = `<td class="v-bad" title="${escapeHtml(r.note || '')}">✗</td>`;
        else verifiedCell = '<td>—</td>';
      }
      const badge = live ? '<span class="badge live">live</span>' : '<span class="badge ref">ref</span>';
      return `<tr class="${live ? '' : 'ref'}">
        <td>${escapeHtml(r.label)} ${badge}</td>
        <td>${sizeCell}</td><td>${ratioCell}</td><td>${loopCell}</td><td>${fpsCell}</td>
        ${netCells}${verifiedCell}
      </tr>`;
    }).join('');

    blocks.push(`<details open>
      <summary>${escapeHtml(ds.label)} — ${escapeHtml(ds.modality)}, ${ds.frames} frames × ${ds.w}×${ds.h} (${ds.bits}-bit), raw ${totalRawMB} MB</summary>
      <div class="panel"><div class="tbl-wrap"><table><thead>${head}</thead><tbody>${body}</tbody></table></div>
      <div class="note">Network columns: full cine-loop time-to-display (download all ${ds.frames} frames + decode all, no pipelining).</div></div>
    </details>`);
  }
  if (!blocks.length) return '';
  return `<h2 class="section">Cine / multi-frame — every frame decoded independently</h2>${blocks.join('')}`;
}

function renderStudies(result, profiles) {
  const byImage = recordsByImage(result.records);
  const findRec = (imgName, codecId) => (byImage.get(imgName) || []).find((r) => r.codecId === codecId);
  const codecs = ['mic-4state', 'mic-8state', 'pics-8'];

  const blocks = codecs.map((codecId) => {
    const label = CODEC_REGISTRY.find((c) => c.id === codecId)?.label ?? codecId;
    const studyTables = STUDIES.map((study) => {
      const recs = study.images.map((n) => findRec(n, codecId));
      if (recs.some((r) => !r || r.compressedBytes == null || r.decodeMs == null)) {
        return `<div class="note">${escapeHtml(study.name)} — codec data unavailable, skipped</div>`;
      }
      const sizes = recs.map((r) => r.compressedBytes);
      const decodes = recs.map((r) => r.decodeMs);
      const totalMB = (sizes.reduce((a, b) => a + b, 0) / 1024 / 1024).toFixed(1);
      const rows = profiles.map((p) => {
        const sim = simulateStudy(sizes, decodes, p.mbps, p.rttMs);
        return `<tr><td>${escapeHtml(p.name)}</td>
          <td>${fmtMs(sim.firstImageMs)}</td>
          <td>${fmtMs(sim.pipelinedMs)}</td>
          <td>${fmtMs(sim.worstCaseMs)}</td></tr>`;
      }).join('');
      return `<div style="margin:10px 0">
        <div style="font-size:13px; margin-bottom:4px">${escapeHtml(study.name)} — ${study.images.length} images, ${totalMB} MB</div>
        <div class="tbl-wrap"><table><thead><tr>
          <th>Network</th><th>1st image</th><th>Full (pipelined)</th><th>Full (no workers)</th>
        </tr></thead><tbody>${rows}</tbody></table></div></div>`;
    }).join('');
    return `<details><summary>Study-load simulation — ${escapeHtml(label)}</summary>
      <div class="panel">${studyTables}</div></details>`;
  }).join('');

  return blocks;
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------
let running = false;

// WAF Challenge (token expired / not yet minted) mid-run: abort the run,
// discard the partial results (do NOT render a half-finished benchmark
// table — this dashboard's numbers get quoted, and unlabelled-partial
// rows would be worse than no rows), then reload the page. A document
// navigation carries Accept: text/html, gets the interstitial, and
// re-mints the token. See docs/pacs-access-control-design.md §4.
function handleChallengeExpired(e, origin) {
  running = false;
  $('start').disabled = false;
  $('cancel').disabled = true;
  $('status').textContent = 'Re-verifying your browser, reloading…';
  if (HEADLESS) {
    window.__pacsBenchError = `${origin}: ${e.message}`;
    window.__pacsBenchDone = true;
    return;
  }
  // Reload to re-mint the token — a document navigation carries
  // Accept: text/html and gets the interstitial. Cap the attempts: if
  // verification keeps failing (e.g. the interstitial is blocked by
  // require-corp, design §5), an unbounded retry would trap the user in a
  // reload loop with no way to read the error. Two tries, then stop and say so.
  let tries = 0;
  try { tries = Number(sessionStorage.getItem('wafReloadTries') || 0); } catch { /* private mode */ }
  if (tries >= 2) {
    try { sessionStorage.removeItem('wafReloadTries'); } catch { /* ignore */ }
    $('status').textContent =
      'Browser verification keeps failing. Please reload the page manually; '
      + 'if it persists the demo may be misconfigured.';
    return;
  }
  try { sessionStorage.setItem('wafReloadTries', String(tries + 1)); } catch { /* ignore */ }
  setTimeout(() => location.reload(), 1500); // legible before reload
}

async function start() {
  if (running) return;
  running = true;
  $('start').disabled = true;
  $('cancel').disabled = false;
  $('bar').style.width = '0%';

  const opts = readOpts();
  opts.onProgress = ({ done, total, label }) => {
    const pct = total ? Math.round((done / total) * 100) : 0;
    $('bar').style.width = pct + '%';
    $('status').textContent = `${done} / ${total} — ${label}`;
  };

  // S3-backed mode: load the selected study's manifests + path resolver and
  // pass them through to runBenchmark. The timing/verify/adapter logic in
  // pacs-runner.mjs is unchanged — only the path-resolution layer is swapped.
  if (S3_MODE) {
    const studyId = $('studyselect').value;
    if (!studyId) {
      $('status').textContent = 'Select an S3 study first.';
      running = false; $('start').disabled = false; $('cancel').disabled = true;
      return;
    }
    $('status').textContent = `Loading study ${studyId}…`;
    let study;
    try {
      study = await loadStudy(studyId, { dataBaseUrl: S3_DATA_BASE });
    } catch (e) {
      if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e, 'loadStudy'); return; }
      $('status').textContent = 'Error loading study: ' + e.message;
      running = false; $('start').disabled = false; $('cancel').disabled = true;
      if (HEADLESS) { window.__pacsBenchError = e.message; window.__pacsBenchDone = true; }
      return;
    }
    renderAttribution(study.study);
    opts.images = study.images;
    activeImages = study.images;
    opts.cine = study.cine;
    opts.cineFrameFn = studyCineFrameImages;
    opts.resolvePath = study.resolvePath;
    opts.rawManifest = study.rawManifest;
    opts.refManifest = study.refManifest;
    opts.baseUrl = S3_DATA_BASE; // resolvePath emits <id>/<dir>/...; baseUrl roots at /data/
  }

  let result;
  try {
    result = await runBenchmark(opts);
  } catch (e) {
    if (e instanceof ChallengeExpiredError) { handleChallengeExpired(e, 'runBenchmark'); return; }
    $('status').textContent = 'Error: ' + e.message;
    running = false; $('start').disabled = false; $('cancel').disabled = true;
    if (HEADLESS) { window.__pacsBenchError = e.message; window.__pacsBenchDone = true; }
    return;
  }

  // A completed run proves verification is working — reset the reload counter
  // so a later challenge in the same session still gets its two attempts.
  try { sessionStorage.removeItem('wafReloadTries'); } catch { /* private mode */ }

  renderEnv(result.env);
  renderResults(result);
  await renderChartsIfAvailable(result);

  $('status').textContent = `Done — ${result.records.length} image + ${(result.cineRecords || []).length} cine measurements`
    + `, iterations=${result.iterations}, warmup=${result.warmup}`
    + (result.verify ? ', verify=on' : '');
  $('foot').innerHTML = footNote(result);

  running = false;
  $('start').disabled = false;
  $('cancel').disabled = true;

  if (HEADLESS) {
    window.__pacsBenchResult = result;
    window.__pacsBenchDone = true;
  }
}

function footNote(result) {
  const bits = [];
  bits.push(`Generated ${escapeHtml(result.generatedAt)}.`);
  bits.push(`Testdata manifest ${result.manifestPresent ? 'present' : 'missing'}; reference-codec manifest ${result.refManifestPresent ? 'present' : 'missing'}.`);
  bits.push('<span class="badge live">live</span> = decoded in this browser now. <span class="badge ref">ref</span> = informational native-C reference (JPEG-XL: no lossless-16-bit browser decoder).');
  return bits.join(' ');
}

async function renderChartsIfAvailable(result) {
  try {
    const mod = await import('./pacs-charts.mjs');
    if (mod?.renderCharts) {
      $('charts-panel').hidden = false;
      mod.renderCharts($('charts'), result, { profiles: activeProfiles() });
    }
  } catch {
    // charts are optional; tables already rendered.
  }
}

// ---------------------------------------------------------------------------
// S3 mode — study picker + attribution banner
// ---------------------------------------------------------------------------
function fmtStudyLabel(s) {
  const rep = s.representative || {};
  const dim = rep.cols && rep.rows ? `${rep.cols}×${rep.rows}` : '—';
  const frames = rep.frames ? ` × ${rep.frames}f` : '';
  const mod = s.modalityLabel || s.modality || '';
  const tier = s.tier ? ` [tier ${s.tier}]` : '';
  return `${s.id} — ${mod} ${dim}${frames}${tier}`;
}

function renderAttribution(study) {
  const panel = $('attribution-panel');
  const box = $('attribution');
  if (!study) { panel.hidden = true; return; }
  const rep = study.representative || {};
  const bits = [];
  if (study.license) bits.push(`<strong>License:</strong> ${escapeHtml(study.license)}`);
  if (study.attribution) bits.push(`<strong>Attribution:</strong> ${escapeHtml(study.attribution)}`);
  if (study.tier) bits.push(`<strong>Tier:</strong> ${escapeHtml(study.tier)}${study.tier === 'A' ? ' (lossless ground truth)' : ' (lossy source — demo only)'}`);
  if (rep.transferSyntaxName) bits.push(`<strong>Transfer syntax:</strong> ${escapeHtml(rep.transferSyntaxName)}`);
  if (study.note) bits.push(`<strong>Note:</strong> ${escapeHtml(study.note)}`);
  box.innerHTML = bits.join(' · ');
  panel.hidden = false;
}

async function initS3Mode() {
  $('studyctl').hidden = false;
  const sel = $('studyselect');
  sel.innerHTML = '<option value="">Loading studies…</option>';
  try {
    const { studies, source } = await listStudies({ dataBaseUrl: S3_DATA_BASE });
    if (!studies.length) {
      sel.innerHTML = '<option value="">No studies found (is /data/ served?)</option>';
      $('status').textContent = 'S3 mode: no studies available. Ensure the CloudFront /data/* origin is reachable.';
      return;
    }
    // Tier A first (lossless ground truth), then Tier B (demo only), then by id.
    studies.sort((a, b) => {
      const ta = (a.tier || 'Z').localeCompare(b.tier || 'Z');
      if (ta) return ta;
      return (a.id || '').localeCompare(b.id || '');
    });
    sel.innerHTML = studies.map((s) =>
      `<option value="${escapeHtml(s.id)}">${escapeHtml(fmtStudyLabel(s))}</option>`).join('');
    // If ?study=<id> is in the URL, preselect it.
    const pre = params.get('study');
    if (pre && studies.some((s) => s.id === pre)) sel.value = pre;
    $('status').textContent = `S3 mode: ${studies.length} studies available (${source} source). Select a study and click Start.`;
  } catch (e) {
    sel.innerHTML = '<option value="">Error loading studies</option>';
    $('status').textContent = 'S3 mode error: ' + e.message;
  }
}

// ---------------------------------------------------------------------------
// Wire up
// ---------------------------------------------------------------------------
$('start').addEventListener('click', start);
$('cancel').addEventListener('click', () => { location.reload(); });

// Reflect URL params into controls (headless + shareable links).
if (params.has('iterations')) $('iterations').value = params.get('iterations');
if (params.has('warmup')) $('warmup').value = params.get('warmup');
if (['quick', 'full', 'cine'].includes(params.get('images'))) $('imageset').value = params.get('images');
if (params.get('verify') === '1') $('verify').checked = true;
if (params.get('allprofiles') === '1') $('allprofiles').checked = true;

// S3 mode: swap the local image-set dropdown for the S3 study picker. The
// benchmark timing/verify pipeline is shared; only the data source differs.
if (S3_MODE) {
  $('imageset').parentElement.hidden = true;
  initS3Mode();
}

// Initial environment snapshot (before any run).
renderEnv({
  userAgent: navigator.userAgent,
  hardwareConcurrency: navigator.hardwareConcurrency,
  crossOriginIsolated: typeof crossOriginIsolated !== 'undefined' ? crossOriginIsolated : undefined,
});

if (HEADLESS) {
  window.__pacsBenchDone = false;
  start();
}
