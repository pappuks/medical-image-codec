// pacs-charts.mjs — SVG charts for the PACS dashboard. Zero runtime deps
// (matches the repo convention). Colors are from the validated data-viz
// reference palette (blue/orange categorical pair for the 2-series stack;
// single blue/aqua hues for magnitude bars), theme-aware via prefers-color-scheme.
//
// Three charts, all for a user-selected image + network profile:
//   1. Time-to-display (stacked: network transfer+RTT vs decode) — the "money"
//      chart: shows visually when the network dominates vs. when decode does.
//   2. Decode time per codec (single-hue magnitude bars).
//   3. Compression ratio per codec (single-hue magnitude bars).

import { transferMs, fmtMs, fmtRatio, fmtKB, CINE_DATASETS } from './pacs-model.mjs';

const PALETTE = {
  light: {
    surface: '#fcfcfb', ink: '#0b0b0b', ink2: '#52514e', muted: '#898781',
    grid: '#e1e0d9', axis: '#c3c2b7',
    network: '#2a78d6', decode: '#eb6834', mag: '#256abf', mag2: '#1baf7a',
    ref: '#898781',
  },
  dark: {
    surface: '#1a1a19', ink: '#ffffff', ink2: '#c3c2b7', muted: '#898781',
    grid: '#2c2c2a', axis: '#383835',
    network: '#3987e5', decode: '#d95926', mag: '#3987e5', mag2: '#199e70',
    ref: '#898781',
  },
};

function palette() {
  const dark = typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches;
  return dark ? PALETTE.dark : PALETTE.light;
}

const SVGNS = 'http://www.w3.org/2000/svg';
function el(name, attrs = {}, text) {
  const n = document.createElementNS(SVGNS, name);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text != null) n.textContent = text;
  return n;
}

// Horizontal bar chart core. rows: [{label, value, segments?:[{value,color,name}], color, note, valueText}].
// segments -> stacked; else single bar with row.color.
function barChart(title, rows, opts = {}) {
  const p = palette();
  const width = opts.width ?? 460;
  const rowH = 26, gap = 10, padL = 130, padR = 64, padT = 8, padB = 26;
  const plotW = width - padL - padR;
  const height = padT + rows.length * (rowH + gap) + padB;
  const maxVal = Math.max(1e-9, ...rows.map((r) => r.value));
  const niceMax = niceCeil(maxVal);
  const x = (v) => padL + (v / niceMax) * plotW;

  const svg = el('svg', {
    viewBox: `0 0 ${width} ${height}`, width, height,
    role: 'img', 'aria-label': title,
    // Responsive sizing lives in CSS (SVG width/height attrs must be lengths,
    // not "auto"); the viewBox drives the aspect ratio.
    style: 'width:100%; height:auto; font: 12px system-ui, -apple-system, sans-serif;',
  });

  // gridlines + x ticks
  const ticks = 4;
  for (let i = 0; i <= ticks; i++) {
    const tv = (niceMax / ticks) * i;
    const tx = x(tv);
    svg.appendChild(el('line', { x1: tx, y1: padT, x2: tx, y2: height - padB, stroke: p.grid, 'stroke-width': 1 }));
    svg.appendChild(el('text', {
      x: tx, y: height - padB + 14, fill: p.muted, 'text-anchor': 'middle',
      'font-variant-numeric': 'tabular-nums',
    }, opts.tickFmt ? opts.tickFmt(tv) : String(Math.round(tv))));
  }

  rows.forEach((r, i) => {
    const y = padT + i * (rowH + gap);
    // codec label (left)
    svg.appendChild(el('text', {
      x: padL - 8, y: y + rowH / 2 + 4, fill: p.ink2, 'text-anchor': 'end',
    }, r.label));

    if (r.segments) {
      let cx = padL;
      for (const seg of r.segments) {
        const w = (seg.value / niceMax) * plotW;
        if (w > 0.5) {
          svg.appendChild(el('rect', {
            x: cx + (cx > padL ? 2 : 0), y, width: Math.max(0, w - (cx > padL ? 2 : 0)),
            height: rowH, rx: 4, fill: seg.color,
          }));
        }
        cx += w;
      }
      // total label at end
      svg.appendChild(el('text', {
        x: Math.min(cx + 6, width - 4), y: y + rowH / 2 + 4, fill: p.ink,
        'text-anchor': cx + 6 > width - 40 ? 'end' : 'start', 'font-variant-numeric': 'tabular-nums',
      }, r.valueText));
    } else {
      const w = (r.value / niceMax) * plotW;
      svg.appendChild(el('rect', { x: padL, y, width: Math.max(1, w), height: rowH, rx: 4, fill: r.color }));
      svg.appendChild(el('text', {
        x: padL + w + 6, y: y + rowH / 2 + 4, fill: p.ink, 'text-anchor': 'start',
        'font-variant-numeric': 'tabular-nums',
      }, r.valueText));
    }
  });

  // baseline
  svg.appendChild(el('line', { x1: padL, y1: height - padB, x2: width - padR, y2: height - padB, stroke: p.axis, 'stroke-width': 1 }));
  return svg;
}

function niceCeil(v) {
  if (v <= 0) return 1;
  const mag = Math.pow(10, Math.floor(Math.log10(v)));
  const n = v / mag;
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10;
  return step * mag;
}

function card(titleText, subtitle, legendItems) {
  const wrap = document.createElement('div');
  wrap.className = 'chart-card panel';
  const h = document.createElement('h3');
  h.textContent = titleText;
  wrap.appendChild(h);
  if (subtitle) {
    const s = document.createElement('div');
    s.className = 'note'; s.style.marginBottom = '8px'; s.textContent = subtitle;
    wrap.appendChild(s);
  }
  if (legendItems) {
    const leg = document.createElement('div');
    leg.style.cssText = 'display:flex; gap:16px; margin-bottom:8px; font-size:12px;';
    for (const it of legendItems) {
      const item = document.createElement('span');
      item.style.cssText = 'display:inline-flex; align-items:center; gap:6px;';
      item.innerHTML = `<span style="width:12px;height:12px;border-radius:3px;background:${it.color};display:inline-block"></span>${it.name}`;
      leg.appendChild(item);
    }
    wrap.appendChild(leg);
  }
  return wrap;
}

// renderCharts(container, result, { profiles }) — public entry.
export function renderCharts(container, result, { profiles }) {
  container.innerHTML = '';

  // Image + profile pickers driving all three charts.
  const images = [...new Set(result.records.filter((r) => r.compressedBytes != null).map((r) => r.image))];
  const cineIds = [...new Set((result.cineRecords || [])
    .filter((r) => r.compressedBytesTotal != null).map((r) => r.cine))];
  const cineLabel = (id) => CINE_DATASETS.find((d) => d.id === id)?.label ?? id;

  const bigDefault = images.includes('CR') ? 'img:CR'
    : images.includes('MG1') ? 'img:MG1'
    : images.length ? `img:${images[images.length - 1]}`
    : cineIds.length ? `cine:${cineIds[0]}`
    : null;

  const chartImageOptions = [
    ...images.map((n) => ({ value: `img:${n}`, text: n })),
    ...cineIds.map((id) => ({ value: `cine:${id}`, text: `${cineLabel(id)} (cine)` })),
  ];

  const controls = document.createElement('div');
  controls.style.cssText = 'grid-column:1/-1; display:flex; gap:16px; flex-wrap:wrap; align-items:end;';
  controls.innerHTML = `
    <div class="ctl"><label>Chart image</label>
      <select id="chart-image">${chartImageOptions.map((o) => `<option value="${o.value}" ${o.value === bigDefault ? 'selected' : ''}>${o.text}</option>`).join('')}</select></div>
    <div class="ctl"><label>Network profile</label>
      <select id="chart-profile">${profiles.map((p, i) => `<option value="${i}" ${p.name === 'Cellular (4G/LTE)' ? 'selected' : ''}>${p.name}</option>`).join('')}</select></div>`;
  container.appendChild(controls);

  const mount = document.createElement('div');
  mount.style.cssText = 'grid-column:1/-1; display:grid; grid-template-columns:1fr; gap:18px;';
  if (window.matchMedia && matchMedia('(min-width:900px)').matches) mount.style.gridTemplateColumns = '1fr 1fr';
  container.appendChild(mount);

  const p = palette();
  const draw = () => {
    mount.innerHTML = '';
    const [kind, key] = container.querySelector('#chart-image').value.split(':');
    const isCine = kind === 'cine';
    const profIdx = parseInt(container.querySelector('#chart-profile').value, 10) || 0;
    const profile = profiles[profIdx];

    const recs = isCine
      ? (result.cineRecords || [])
          .filter((r) => r.cine === key && r.compressedBytesTotal != null && r.loopMs != null)
          .map((r) => ({ label: r.label, liveDecode: r.liveDecode,
                          compressedBytes: r.compressedBytesTotal, decodeMs: r.loopMs, ratio: r.ratio }))
      : result.records
          .filter((r) => r.image === key && r.compressedBytes != null && r.decodeMs != null)
          .map((r) => ({ label: r.label, liveDecode: r.liveDecode,
                          compressedBytes: r.compressedBytes, decodeMs: r.decodeMs, ratio: r.ratio }));
    const subject = isCine ? `${cineLabel(key)} (cine)` : key;

    // 1. Time-to-display stacked (network = rtt + transfer, decode)
    const ttdRows = recs.map((r) => {
      const net = profile.rttMs + transferMs(r.compressedBytes, profile.mbps);
      const dec = r.decodeMs;
      return {
        label: r.label, value: net + dec, valueText: fmtMs(net + dec),
        segments: [
          { value: net, color: p.network, name: 'Network' },
          { value: dec, color: r.liveDecode ? p.decode : p.ref, name: 'Decode' },
        ],
      };
    });
    const c1 = card(`Time to display — ${subject} over ${profile.name}`,
      `${profile.mbps} Mbps, ${profile.rttMs} ms RTT. Stacked: network transfer+RTT vs. decode.`,
      [{ name: 'Network (transfer+RTT)', color: p.network }, { name: 'Decode', color: p.decode }]);
    c1.appendChild(barChart('Time to display', ttdRows, { tickFmt: (v) => (v >= 1000 ? (v / 1000).toFixed(1) + 's' : Math.round(v) + 'ms') }));
    mount.appendChild(c1);

    // 2. Decode time per codec
    const decRows = recs.map((r) => ({
      label: r.label, value: r.decodeMs, valueText: fmtMs(r.decodeMs),
      color: r.liveDecode ? p.mag : p.ref,
    }));
    const c2 = card(`Decode time — ${subject}`,
      isCine
        ? 'Live browser decode (blue) vs. informational native-C reference (grey). Cine: total loop time for all frames, not per-frame.'
        : 'Live browser decode (blue) vs. informational native-C reference (grey).');
    c2.appendChild(barChart('Decode time', decRows, { tickFmt: (v) => (v >= 1000 ? (v / 1000).toFixed(1) + 's' : Math.round(v) + 'ms') }));
    mount.appendChild(c2);

    // 3. Compression ratio per codec
    const ratioRows = recs.map((r) => ({
      label: r.label, value: r.ratio, valueText: fmtRatio(r.ratio) + '  ' + fmtKB(r.compressedBytes),
      color: p.mag2,
    }));
    const c3 = card(`Compression ratio — ${subject}`, 'Higher is smaller on the wire.');
    c3.appendChild(barChart('Compression ratio', ratioRows, { tickFmt: (v) => v.toFixed(1) + 'x' }));
    mount.appendChild(c3);
  };

  container.querySelector('#chart-image').addEventListener('change', draw);
  container.querySelector('#chart-profile').addEventListener('change', draw);
  draw();
}
