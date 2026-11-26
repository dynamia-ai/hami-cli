#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:?usage: publish-s3.sh tagged|mutable VERSION DIST_DIR}"
version="${2:?usage: publish-s3.sh tagged|mutable VERSION DIST_DIR}"
dist_dir="${3:?usage: publish-s3.sh tagged|mutable VERSION DIST_DIR}"

case "$mode" in
  tagged|mutable)
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

bash "$script_dir/verify-release-archives.sh" "$dist_dir" "$version"
artifacts=("$dist_dir"/*.tar.gz "$dist_dir"/*.tar.gz.sha256)
if [[ "$mode" = tagged ]]; then
  test -s "$dist_dir/image-digest.txt"
  artifacts+=("$dist_dir/image-digest.txt")
fi
if [[ "$version" = latest ]]; then
  test -s "$dist_dir/install-r2.sh"
  artifacts+=("$dist_dir/install-r2.sh")
fi

for artifact in "${artifacts[@]}"; do
  name="${artifact##*/}"
  key="hami-cli/$version/$name"
  content_type=application/gzip
  [[ "$name" = *.sha256 || "$name" = *.txt ]] && content_type=text/plain
  if [[ "$name" = install-r2.sh ]]; then
    key=hami-cli/install.sh
    content_type=text/x-shellscript
  fi

  aws s3api put-object "${aws_args[@]}" \
    --bucket "$S3_BUCKET" \
    --key "$key" \
    --body "$artifact" \
    --cache-control "$cache_control" \
    --content-type "$content_type" >/dev/null
done
