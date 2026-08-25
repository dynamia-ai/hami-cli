#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="${1:?usage: publish-oss.sh stable|mutable VERSION DIST_DIR}"
version="${2:?usage: publish-oss.sh stable|mutable VERSION DIST_DIR}"
dist_dir="${3:?usage: publish-oss.sh stable|mutable VERSION DIST_DIR}"

case "$mode" in
  stable|mutable)
    cache_control='no-cache,max-age=0,must-revalidate'
    ;;
  *)
    echo "unsupported publication mode: $mode" >&2
    exit 2
    ;;
esac

: "${ALIYUN_OSS_ACCESS_KEY_ID:?ALIYUN_OSS_ACCESS_KEY_ID is required}"
: "${ALIYUN_OSS_ACCESS_KEY_SECRET:?ALIYUN_OSS_ACCESS_KEY_SECRET is required}"
: "${ALIYUN_OSS_BUCKET:?ALIYUN_OSS_BUCKET is required}"
: "${ALIYUN_OSS_REGION:?ALIYUN_OSS_REGION is required}"
: "${ALIYUN_OSS_ENDPOINT:?ALIYUN_OSS_ENDPOINT is required}"

# ossutil reads these vendor-defined environment variables directly.
export OSS_ACCESS_KEY_ID="$ALIYUN_OSS_ACCESS_KEY_ID"
export OSS_ACCESS_KEY_SECRET="$ALIYUN_OSS_ACCESS_KEY_SECRET"
export OSS_REGION="$ALIYUN_OSS_REGION"
export OSS_ENDPOINT="$ALIYUN_OSS_ENDPOINT"

shopt -s nullglob
artifacts=("$dist_dir"/*.tar.gz "$dist_dir"/*.tar.gz.sha256)
if [[ "$mode" = stable ]]; then
  artifacts+=("$dist_dir/image-digest.txt")
fi
if [[ "$version" = latest ]]; then
  artifacts+=("$dist_dir/install-oss.sh")
fi
expected_count=10
[[ "$mode" = stable ]] && expected_count=11
[[ "$version" = latest ]] && expected_count=11
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
  if [[ "$name" = install-oss.sh ]]; then
    key=hami-cli/install.sh
    content_type=text/x-shellscript
  fi

  ossutil api put-object \
    --bucket "$ALIYUN_OSS_BUCKET" \
    --key "$key" \
    --body "file://$artifact" \
    --object-acl public-read \
    --cache-control "$cache_control" \
    --content-type "$content_type" >/dev/null
done
