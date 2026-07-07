// pacs-dashboard.mjs — DOM controller for the PACS benchmark. Owns the
// controls, runs the benchmark via pacs-runner.mjs, renders the environment
// panel, per-image tables, study-level simulation, and (via pacs-charts.mjs)
// charts. Exposes a ?headless=1 machine hook for the Playwright runner.

import {
  IMAGES, QUICK_IMAGE_NAMES, NETWORK_PROFILES, DEFAULT_PROFILE_NAMES,
  CODEC_REGISTRY, STUDIES, simulateStudy,
  fmtMs, fmtKB, fmtRatio, timeToDisplayMs,
} from './pacs-model.mjs';
import { runBenchmark } from './pacs-runner.mjs';

const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const HEADLESS = params.get('headless') === '1';

// ---------------------------------------------------------------------------
// Controls
// ---------------------------------------------------------------------------
function readOpts() {
  const imgSet = $('imageset').value;
  const images = imgSet === 'quick'
    ? IMAGES.filter((i) => QUICK_IMAGE_NAMES.includes(i.name))
    : IMAGES;
  return {
    images,
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

function renderResults(result) {
  const profiles = activeProfiles();
  const byImage = recordsByImage(result.records);
  const parts = [];

  for (const img of IMAGES) {
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

  // Study-level simulation for the two headline MIC variants.
  parts.push(renderStudies(result, profiles));

  $('results').innerHTML = parts.join('');
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

  let result;
  try {
    result = await runBenchmark(opts);
  } catch (e) {
    $('status').textContent = 'Error: ' + e.message;
    running = false; $('start').disabled = false; $('cancel').disabled = true;
    if (HEADLESS) { window.__pacsBenchError = e.message; window.__pacsBenchDone = true; }
    return;
  }

  renderEnv(result.env);
  renderResults(result);
  await renderChartsIfAvailable(result);

  $('status').textContent = `Done — ${result.records.length} measurements`
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
// Wire up
// ---------------------------------------------------------------------------
$('start').addEventListener('click', start);
$('cancel').addEventListener('click', () => { location.reload(); });

// Reflect URL params into controls (headless + shareable links).
if (params.has('iterations')) $('iterations').value = params.get('iterations');
if (params.has('warmup')) $('warmup').value = params.get('warmup');
if (params.get('images') === 'quick' || params.get('images') === 'full') $('imageset').value = params.get('images');
if (params.get('verify') === '1') $('verify').checked = true;
if (params.get('allprofiles') === '1') $('allprofiles').checked = true;

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
