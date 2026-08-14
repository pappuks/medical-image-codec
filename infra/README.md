# MIC PACS Demo — AWS Infrastructure

Hosts the MIC PACS web-viewer dashboard as a **public, always-on online demo**
backed by the real S3 dataset. Design source of truth:
[`docs/pacs-lambda-service-design.md`](../docs/pacs-lambda-service-design.md).

## Architecture

One CloudFront distribution, one public hostname, three origins:

```
Browser ──► CloudFront (COOP/COEP/CORP + WAF, one hostname)
              ├── /*        → app S3 bucket      (HTML/JS/WASM, private via OAC)
              ├── /data/*   → studies S3 bucket  (17 GB dataset, private via OAC)
              └── /api/*    → Lambda Function URL (SigV4 via OAC, not directly callable)
```

The single-hostname layout makes every subresource same-origin to the
document, which dissolves the COEP `require-corp` per-object header problem
(design §4.2). Lambda is only the thin study-listing API; blob bytes never
touch it.

## Files

| File | What |
|------|------|
| `template.yaml` | SAM stack (us-west-1): app bucket + policy, studies bucket policy, S3 + Lambda OACs, Response Headers Policy, cache policies, Lambda + Function URL, CloudFront distribution, access-logs bucket, CloudFront egress alarm. |
| `waf.yaml` | SAM stack (us-east-1): WAFv2 WebACL (CLOUDFRONT scope) — rate-based rule (2000 req/5min per IP) + AWS managed `CommonRuleSet` and `KnownBadInputsRuleSet`. |
| `api/index.js` | Lambda handler — `/api/health` + `/api/studies` (manifest-proxy, read-only). |
| `api/package.json` | Lambda deps (`@aws-sdk/client-s3`). |
| `deploy.sh` | One-command deploy: WAF stack → app stack → `aws s3 sync web/` → CloudFront invalidation. |

## Prerequisites

1. **Rotate the AWS keys.** The ingest pipeline used root-account keys in
   plaintext (see `docs/pacs-demo-roadmap.md` §Security). Replace with a
   bucket-scoped IAM role before deploying anything publicly.
2. SAM CLI: `brew install aws-sam-cli` (or see [AWS docs](https://docs.aws.amazon.com/serverless-application-model/latest/developerguide/install-sam-cli.html)).
3. AWS credentials configured via `AWS_PROFILE` or env vars, with permission
   to create CloudFront distributions, S3 buckets, Lambda, IAM roles, and WAF.
4. Node 20.x (for the Lambda runtime build).

## Deploy

```bash
bash infra/deploy.sh
```

This runs in three stages:

1. **WAF stack** (`us-east-1`): creates the `CLOUDFRONT`-scope WebACL. AWS
   requires this to live in `us-east-1` even though the distribution's origins
   are in `us-west-1` (design §9).
2. **App stack** (`us-west-1`): creates everything else and passes the WebACL
   ARN in as a parameter.
3. **Static sync**: `aws s3 sync web/` to the app bucket (excluding
   `node_modules`, `testdata`, `tests`, `results`, etc.) + a CloudFront
   invalidation.

At the end it prints the public URL:

```
https://<distribution>.cloudfront.net/pacs-dashboard.html?source=s3
```

`?source=s3` switches the dashboard into S3-backed mode (see
`web/pacs-study-source.mjs`). Without it, the dashboard stays in its existing
`testdata/`-backed dev/CI mode, unchanged.

To deploy the stacks without syncing web assets (e.g. infra-only iteration):

```bash
bash infra/deploy.sh --no-sync
```

## Security & DDoS posture

| Concern | Control |
|---------|---------|
| Bucket stays private | All four public-access-block flags `true` on both buckets; reads only via CloudFront OAC (bucket policy scoped to `AWS:SourceArn` = this distribution). |
| Lambda not directly callable | Function URL auth `AWS_IAM` + CloudFront OAC-for-Lambda (SigV4 signing). The raw `*.lambda-url.*.on.aws` endpoint rejects unauthenticated calls. |
| Least-privilege IAM | Execution role scoped to `s3:GetObject` on `manifest.json`, `*/mic-manifest.json`, `*/ref-manifest.json` only. No `ListBucket`, no writes, no other buckets. |
| DDoS / abuse | WAF rate-based rule (2000 req/5min per IP) + AWS managed baseline rule groups. CloudFront caching absorbs repeat views. CloudWatch alarm on `BytesDownloaded` (50 GB / 5 min) as a cost-mirror tripwire. |
| COOP/COEP for SharedArrayBuffer | Response Headers Policy injects `same-origin` / `require-corp` / `cross-origin` on all three behaviors — the production equivalent of `web/serve.py`. |
| Egress cost cap | Long TTL on `/data/*` codec blobs (immutable), short TTL on manifests (so re-ingests surface in ~5 min), CloudFront `PriceClass_100` (US/EU only). |
| HTTPS-only | `ViewerProtocolPolicy: redirect-to-https` on every behavior; HSTS preload via the Response Headers Policy. |

## Cost

Roughly **$15–55/month** at light-to-moderate demo traffic, dominated by
CloudFront data transfer out. See design §11 for the full breakdown and §12
for the guardrails that bound it.

## Updating after a re-ingest

The studies bucket is independent of this stack. After running
`scripts/pacs-ingest/upload_s3.py` again:

- The root `manifest.json` is overwritten in S3. The Lambda re-fetches it
  within 60s (in-process cache TTL) and CloudFront re-fetches within 5 min
  (`ManifestCacheTTL`, configurable in `template.yaml`).
- No redeploy needed unless the manifest schema itself changes.

To update the **front-end** (after editing `web/`):

```bash
aws s3 sync web/ s3://<app-bucket>/ \
  --exclude node_modules --exclude testdata --exclude tests \
  --exclude results --exclude test-results --exclude '*.log'
aws cloudfront create-invalidation --distribution-id <dist-id> --paths '/*'
```

## Tear down

```bash
aws cloudformation delete-stack --stack-name mic-pacs-demo --region us-west-1
aws cloudformation delete-stack --stack-name mic-pacs-demo-waf --region us-east-1
# Empty and delete the app + logs buckets manually (CFN won't delete non-empty buckets):
aws s3 rm s3://<app-bucket> --recursive
aws s3 rb s3://<app-bucket>
```

The studies bucket is **not** managed by this stack — only its bucket policy
was attached. Deleting the stack removes the policy but leaves the bucket and
its 17 GB of data intact.