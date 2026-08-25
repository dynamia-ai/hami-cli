#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:?usage: publish-s3.sh stable|mutable VERSION DIST_DIR}"
version="${2:?usage: publish-s3.sh stable|mutable VERSION DIST_DIR}"
dist_dir="${3:?usage: publish-s3.sh stable|mutable VERSION DIST_DIR}"

case "$mode" in
  stable|mutable)
    cache_control='no-cache,max-age=0,must-revalidate'
    ;;
  *)
    echo "unsupported publication mode: $mode" >&2
    exit 2
    ;;
esac

: "${S3_ACCESS_KEY_ID:?S3_ACCESS_KEY_ID is required}"
: "${S3_SECRET_ACCESS_KEY:?S3_SECRET_ACCESS_KEY is required}"
: "${S3_BUCKET:?S3_BUCKET is required}"
: "${S3_ENDPOINT:?S3_ENDPOINT is required}"
: "${S3_REGION:?S3_REGION is required}"

export AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$S3_SECRET_ACCESS_KEY"
export AWS_DEFAULT_REGION="$S3_REGION"
export AWS_REGION="$S3_REGION"
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required
export AWS_RESPONSE_CHECKSUM_VALIDATION=when_required

aws_args=(--endpoint-url "$S3_ENDPOINT" --region "$S3_REGION" --no-cli-pager)

shopt -s nullglob
artifacts=("$dist_dir"/*.tar.gz "$dist_dir"/*.tar.gz.sha256)
if [[ "$mode" = stable ]]; then
  artifacts+=("$dist_dir/image-digest.txt")
fi
expected_count=10
[[ "$mode" = stable ]] && expected_count=11
test "${#artifacts[@]}" -eq "$expected_count"
for archive in "$dist_dir"/*.tar.gz; do
  [[ "${archive##*/}" = "hami-cli_${version}_"*.tar.gz ]]
done
bash "$script_dir/verify-release-archives.sh" "$dist_dir"

for artifact in "${artifacts[@]}"; do
  name="${artifact##*/}"
  key="hami-cli/$version/$name"
  content_type=application/gzip
  [[ "$name" = *.sha256 || "$name" = *.txt ]] && content_type=text/plain

  aws s3api put-object "${aws_args[@]}" \
    --bucket "$S3_BUCKET" \
    --key "$key" \
    --body "$artifact" \
    --cache-control "$cache_control" \
    --content-type "$content_type" >/dev/null
done
