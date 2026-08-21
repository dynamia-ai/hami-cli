#!/usr/bin/env bash

set -euo pipefail

mode="${1:?usage: publish-oss.sh immutable|mutable VERSION DIST_DIR}"
version="${2:?usage: publish-oss.sh immutable|mutable VERSION DIST_DIR}"
dist_dir="${3:?usage: publish-oss.sh immutable|mutable VERSION DIST_DIR}"

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

: "${OSS_ACCESS_KEY_ID:?OSS_ACCESS_KEY_ID is required}"
: "${OSS_ACCESS_KEY_SECRET:?OSS_ACCESS_KEY_SECRET is required}"
: "${OSS_BUCKET:?OSS_BUCKET is required}"
: "${OSS_REGION:?OSS_REGION is required}"
: "${OSS_ENDPOINT:?OSS_ENDPOINT is required}"

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

if [[ "$mode" = immutable ]]; then
  versioning="$(ossutil api get-bucket-versioning \
    --bucket "$OSS_BUCKET" --output-format json)"
  if ! jq -e . <<< "$versioning" >/dev/null; then
    echo "failed to parse OSS bucket versioning state" >&2
    exit 1
  fi
  versioning_status="$(jq -r \
    '[.. | objects | .Status? // empty] | unique | join(",")' <<< "$versioning")"
  case "$versioning_status" in
    ""|Disabled) ;;
    *)
      echo "immutable publication requires OSS bucket versioning to be disabled" >&2
      exit 1
      ;;
  esac
fi

download_and_compare() {
  local object="$1"
  local artifact="$2"
  local existing
  existing="$(mktemp)"
  ossutil cp "$object" "$existing" --force >/dev/null
  if ! cmp "$artifact" "$existing"; then
    echo "refusing to overwrite immutable OSS object: $object" >&2
    rm -f "$existing"
    return 1
  fi
  rm -f "$existing"
}

for artifact in "${artifacts[@]}"; do
  name="${artifact##*/}"
  key="hami-cli/$version/$name"
  object="oss://$OSS_BUCKET/$key"
  content_type=application/gzip
  [[ "$name" = *.txt ]] && content_type=text/plain

  if [[ "$mode" = immutable ]]; then
    lookup_error="$(mktemp)"
    if ossutil stat "$object" >/dev/null 2>"$lookup_error"; then
      rm -f "$lookup_error"
      download_and_compare "$object" "$artifact"
      continue
    fi
    if ! grep -Eqi '404|NoSuchKey|not exist' "$lookup_error"; then
      echo "failed to determine whether immutable OSS object exists: $object" >&2
      sed -n '1,20p' "$lookup_error" >&2
      rm -f "$lookup_error"
      exit 1
    fi
    rm -f "$lookup_error"

    upload_error="$(mktemp)"
    if ossutil api put-object \
      --bucket "$OSS_BUCKET" \
      --key "$key" \
      --body "file://$artifact" \
      --forbid-overwrite=true \
      --object-acl public-read \
      --cache-control "$cache_control" \
      --content-type "$content_type" >/dev/null 2>"$upload_error"; then
      rm -f "$upload_error"
      continue
    fi
    if grep -Eqi '409|FileAlreadyExists' "$upload_error"; then
      rm -f "$upload_error"
      download_and_compare "$object" "$artifact"
      continue
    fi
    echo "failed to create immutable OSS object: $object" >&2
    sed -n '1,20p' "$upload_error" >&2
    rm -f "$upload_error"
    exit 1
  fi

  ossutil api put-object \
    --bucket "$OSS_BUCKET" \
    --key "$key" \
    --body "file://$artifact" \
    --object-acl public-read \
    --cache-control "$cache_control" \
    --content-type "$content_type" >/dev/null
done
