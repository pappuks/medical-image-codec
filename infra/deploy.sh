#!/usr/bin/env bash
# Deploy the MIC PACS public demo to AWS.
#
# Two stacks (design §10):
#   1. mic-pacs-demo-waf  (us-east-1)  — WAFv2 WebACL, CLOUDFRONT scope
#   2. mic-pacs-demo      (us-west-1)  — app bucket, studies bucket policy,
#                                        Lambda + Function URL, CloudFront
#
# After the stacks deploy, sync web/ static assets to the app bucket and
# invalidate the CloudFront cache.
#
# Prereqs:
#   - AWS SAM CLI installed (`brew install aws-sam-cli`)
#   - AWS credentials configured (scoped IAM role, NOT root keys — see
#     docs/pacs-demo-roadmap.md §Security). Use AWS_PROFILE or env vars.
#   - Node 20.x installed (for the Lambda build; SAM uses it for nodejs20.x).
#
# Usage:
#   bash infra/deploy.sh                  # deploy both stacks + sync web
#   bash infra/deploy.sh --no-sync        # stacks only, skip web/ sync
#   STACK_PREFIX=myorg bash infra/deploy.sh
set -euo pipefail

STACK_PREFIX="${STACK_PREFIX:-mic-pacs-demo}"
WAF_STACK="${STACK_PREFIX}-waf"
APP_STACK="${STACK_PREFIX}"
REGION="${AWS_REGION:-us-west-1}"
WAF_REGION="us-east-1"
WEB_DIR="$(cd "$(dirname "$0")/.." && pwd)/web"

echo "==> Deploying WAF stack: ${WAF_STACK} (${WAF_REGION})"
sam deploy \
  --template-file "$(dirname "$0")/waf.yaml" \
  --stack-name "${WAF_STACK}" \
  --region "${WAF_REGION}" \
  --capabilities CAPABILITY_IAM \
  --no-fail-on-empty-changeset \
  --confirm-changeset

WEBACL_ARN=$(aws cloudformation describe-stacks \
  --stack-name "${WAF_STACK}" --region "${WAF_REGION}" \
  --query 'Stacks[0].Outputs[?OutputKey==`WebACLArn`].OutputValue' \
  --output text)
echo "    WebACL ARN: ${WEBACL_ARN}"

echo "==> Deploying app stack: ${APP_STACK} (${REGION})"
sam deploy \
  --template-file "$(dirname "$0")/template.yaml" \
  --stack-name "${APP_STACK}" \
  --region "${REGION}" \
  --capabilities CAPABILITY_IAM \
  --no-fail-on-empty-changeset \
  --confirm-changeset \
  --parameter-overrides "WebACLArn=${WEBACL_ARN}"

DIST_DOMAIN=$(aws cloudformation describe-stacks \
  --stack-name "${APP_STACK}" --region "${REGION}" \
  --query 'Stacks[0].Outputs[?OutputKey==`DistributionDomainName`].OutputValue' \
  --output text)
APP_BUCKET=$(aws cloudformation describe-stacks \
  --stack-name "${APP_STACK}" --region "${REGION}" \
  --query 'Stacks[0].Outputs[?OutputKey==`AppBucketName`].OutputValue' \
  --output text)
DIST_ID=$(aws cloudformation describe-stacks \
  --stack-name "${APP_STACK}" --region "${REGION}" \
  --query 'Stacks[0].Outputs[?OutputKey==`DistributionId`].OutputValue' \
  --output text)

echo "    CloudFront domain: ${DIST_DOMAIN}"
echo "    App bucket:        ${APP_BUCKET}"

if [[ "${1:-}" == "--no-sync" ]]; then
  echo "==> Skipping web/ sync (--no-sync). Run:"
  echo "    aws s3 sync ${WEB_DIR} s3://${APP_BUCKET}/ \\"
  echo "      --exclude node_modules --exclude testdata --exclude tests \\"
  echo "      --exclude results --exclude test-results --exclude '*.min.js'"
  echo "    aws cloudfront create-invalidation --distribution-id ${DIST_ID} --paths '/*'"
  exit 0
fi

echo "==> Syncing web/ to s3://${APP_BUCKET}/"
aws s3 sync "${WEB_DIR}" "s3://${APP_BUCKET}/" \
  --exclude 'node_modules' \
  --exclude 'testdata' \
  --exclude 'tests' \
  --exclude 'results' \
  --exclude 'test-results' \
  --exclude '*.log' \
  --exclude 'package.json' \
  --exclude 'package-lock.json' \
  --exclude 'playwright.config.mjs' \
  --exclude 'serve.py' \
  --exclude 'serve.json' \
  --delete

echo "==> Invalidating CloudFront cache"
aws cloudfront create-invalidation \
  --distribution-id "${DIST_ID}" \
  --paths '/*' >/dev/null

echo
echo "==> Done. Open: https://${DIST_DOMAIN}/pacs-dashboard.html?source=s3"
echo "    (the demo dashboard in S3-backed mode; see docs/pacs-lambda-service-design.md §7)"