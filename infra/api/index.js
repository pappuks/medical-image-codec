// index.js — MIC PACS demo API (Lambda Function URL, Node 20.x).
//
// Thin, stateless, read-only API. Two routes:
//   GET /api/health   -> static 200 { ok: true, ... }
//   GET /api/studies  -> list studies from the bucket's root manifest.json,
//                        with optional ?modality= / ?tier= / ?id= filtering.
//
// Design source of truth: docs/pacs-lambda-service-design.md §8.
// The manifest-proxy variant is the default: fetch + filter the root
// manifest.json. No s3:ListBucket permission is required; the Lambda only
// reads manifest.json (and per-study manifest fragments if a single study is
// requested with ?detail=1).
//
// Blob bytes NEVER pass through this function — CloudFront serves /data/*
// directly from the studies bucket via OAC. The Lambda is not in the hot path.

'use strict';

const { S3Client, GetObjectCommand } = require('@aws-sdk/client-s3');
const { Readable } = require('stream');

const REGION = process.env.STUDIES_REGION || process.env.AWS_REGION || 'us-west-1';
const BUCKET = process.env.STUDIES_BUCKET;
if (!BUCKET) {
  // Fail fast at cold start if the env var is missing — every request would 500 anyway.
  throw new Error('STUDIES_BUCKET environment variable is required');
}

const s3 = new S3Client({ region: REGION });

// In-process cache of the root manifest. Short TTL: the studies dataset is
// effectively immutable between ingests, but a re-ingest should surface within
// a minute without a Lambda redeploy. Cache hit avoids one S3 GET per request.
const MANIFEST_TTL_MS = 60_000;
let manifestCache = { at: 0, value: null };

const JSON_HEADERS = {
  'Content-Type': 'application/json; charset=utf-8',
  // Same-origin to the dashboard; CORP is defense-in-depth (design §4.2).
  'Cross-Origin-Resource-Policy': 'cross-origin',
  'Cache-Control': 'public, max-age=60',
};

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

async function streamToString(stream) {
  if (!stream) return '';
  const chunks = [];
  for await (const chunk of stream) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
  return Buffer.concat(chunks).toString('utf-8');
}

async function fetchManifest() {
  const now = Date.now();
  if (manifestCache.value && now - manifestCache.at < MANIFEST_TTL_MS) {
    return manifestCache.value;
  }
  const cmd = new GetObjectCommand({ Bucket: BUCKET, Key: 'manifest.json' });
  const resp = await s3.send(cmd);
  const body = await streamToString(resp.Body);
  const json = JSON.parse(body);
  manifestCache = { at: now, value: json };
  return json;
}

async function fetchStudyManifest(id, kind) {
  // kind: 'mic-manifest.json' | 'ref-manifest.json'
  const key = `${id}/${kind}`;
  const cmd = new GetObjectCommand({ Bucket: BUCKET, Key: key });
  const resp = await s3.send(cmd);
  const body = await streamToString(resp.Body);
  return JSON.parse(body);
}

function jsonResponse(statusCode, payload) {
  return {
    statusCode,
    headers: JSON_HEADERS,
    body: JSON.stringify(payload),
  };
}

function errorResponse(statusCode, message) {
  return jsonResponse(statusCode, { error: message });
}

// Project a manifest entry into the shape the study picker needs.
// Keeps the wire format small and stable — only the fields the front-end reads.
function projectStudy(e) {
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

// ----------------------------------------------------------------------------
// Route handlers
// ----------------------------------------------------------------------------

function handleHealth() {
  return jsonResponse(200, {
    ok: true,
    service: 'mic-pacs-demo-api',
    region: REGION,
    bucket: BUCKET,
    time: new Date().toISOString(),
  });
}

async function handleStudies(event) {
  const qs = event.queryStringParameters || {};
  const modality = qs.modality ? String(qs.modality).toUpperCase() : null;
  const tier = qs.tier ? String(qs.tier).toUpperCase() : null;
  const id = qs.id ? String(qs.id) : null;
  const detail = qs.detail === '1' || qs.detail === 'true';

  const manifest = await fetchManifest();
  let entries = manifest.entries || [];

  if (id) {
    entries = entries.filter((e) => e.id === id);
    if (!entries.length) return errorResponse(404, `study not found: ${id}`);
  }
  if (tier) {
    entries = entries.filter((e) => (e.tier || '').toUpperCase() === tier);
  }
  if (modality) {
    entries = entries.filter((e) => ((e.representative || {}).modality || '').toUpperCase() === modality);
  }

  if (detail && id && entries.length === 1) {
    const e = entries[0];
    let micManifest = null;
    let refManifest = null;
    // Best-effort: per-study fragments may be absent for some studies.
    try { micManifest = await fetchStudyManifest(e.id, 'mic-manifest.json'); } catch (_) {}
    try { refManifest = await fetchStudyManifest(e.id, 'ref-manifest.json'); } catch (_) {}
    return jsonResponse(200, {
      study: projectStudy(e),
      micManifest,
      refManifest,
    });
  }

  return jsonResponse(200, {
    studies: entries.map(projectStudy),
    count: entries.length,
  });
}

// ----------------------------------------------------------------------------
// Entry
// ----------------------------------------------------------------------------

exports.handler = async (event) => {
  // Lambda Function URL events have a `rawPath` and `requestContext.http.method`.
  const method = (event.requestContext && event.requestContext.http && event.requestContext.http.method) || 'GET';
  const path = event.rawPath || (event.path || '/');

  if (method === 'OPTIONS') {
    // CloudFront handles CORS preflight via the Response Headers Policy; this
    // is a safety net if the function is ever called directly.
    return { statusCode: 204, headers: JSON_HEADERS, body: '' };
  }

  try {
    if (path === '/api/health') return handleHealth();
    if (path === '/api/studies' || path.startsWith('/api/studies')) {
      return await handleStudies(event);
    }
    return errorResponse(404, `not found: ${method} ${path}`);
  } catch (err) {
    // Never leak stack traces to the public response; log to CloudWatch only.
    console.error('handler error:', err);
    return errorResponse(500, 'internal error');
  }
};