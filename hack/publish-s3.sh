#!/usr/bin/env bash

set -euo pipefail

mode="${1:?usage: publish-s3.sh immutable|mutable VERSION DIST_DIR}"
version="${2:?usage: publish-s3.sh immutable|mutable VERSION DIST_DIR}"
dist_dir="${3:?usage: publish-s3.sh immutable|mutable VERSION DIST_DIR}"

case "$mode" in
  immutable)
    cache_control='public,max-age=31536000,immutable'
    ;;
  mutable)
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
artifacts=("$dist_dir"/*.tar.gz "$dist_dir/checksums.txt")
if [[ "$mode" = immutable ]]; then
  artifacts+=("$dist_dir/image-digest.txt")
fi
expected_count=6
[[ "$mode" = immutable ]] && expected_count=7
test "${#artifacts[@]}" -eq "$expected_count"
for archive in "$dist_dir"/*.tar.gz; do
  [[ "${archive##*/}" = "hami-cli_${version}_"*.tar.gz ]]
done
(
  cd "$dist_dir"
  sha256sum --check checksums.txt
)

download_and_compare() {
  local key="$1"
  local artifact="$2"
  local existing
  existing="$(mktemp)"
  aws s3api get-object "${aws_args[@]}" \
    --bucket "$S3_BUCKET" --key "$key" "$existing" >/dev/null
  if ! cmp "$artifact" "$existing"; then
    echo "refusing to overwrite immutable S3 object: s3://$S3_BUCKET/$key" >&2
    rm -f "$existing"
    return 1
  fi
  rm -f "$existing"
}

for artifact in "${artifacts[@]}"; do
  name="${artifact##*/}"
  key="hami-cli/$version/$name"
  object="s3://$S3_BUCKET/$key"
  content_type=application/gzip
  [[ "$name" = *.txt ]] && content_type=text/plain

  if [[ "$mode" = immutable ]]; then
    lookup_error="$(mktemp)"
    if aws s3api head-object "${aws_args[@]}" \
      --bucket "$S3_BUCKET" --key "$key" >/dev/null 2>"$lookup_error"; then
      rm -f "$lookup_error"
      download_and_compare "$key" "$artifact"
      continue
    fi
    if ! grep -Eqi '\(404\)|Not Found|NoSuchKey' "$lookup_error"; then
      echo "failed to determine whether immutable S3 object exists: $object" >&2
      sed -n '1,20p' "$lookup_error" >&2
      rm -f "$lookup_error"
      exit 1
    fi
    rm -f "$lookup_error"

    for attempt in {1..5}; do
      upload_error="$(mktemp)"
      if aws s3api put-object "${aws_args[@]}" \
        --bucket "$S3_BUCKET" \
        --key "$key" \
        --body "$artifact" \
        --if-none-match '*' \
        --cache-control "$cache_control" \
        --content-type "$content_type" >/dev/null 2>"$upload_error"; then
        rm -f "$upload_error"
        continue 2
      fi
      if grep -Eqi 'PreconditionFailed|\(412\)' "$upload_error"; then
        rm -f "$upload_error"
        download_and_compare "$key" "$artifact"
        continue 2
      fi
      if grep -Eqi 'ConditionalRequestConflict|\(409\)' "$upload_error" \
        && [[ "$attempt" -lt 5 ]]; then
        rm -f "$upload_error"
        sleep "$attempt"
        continue
      fi
      echo "failed to create immutable S3 object: $object" >&2
      sed -n '1,20p' "$upload_error" >&2
      rm -f "$upload_error"
      exit 1
    done
  fi

  aws s3api put-object "${aws_args[@]}" \
    --bucket "$S3_BUCKET" \
    --key "$key" \
    --body "$artifact" \
    --cache-control "$cache_control" \
    --content-type "$content_type" >/dev/null
done
