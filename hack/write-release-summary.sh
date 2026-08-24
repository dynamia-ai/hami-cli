#!/usr/bin/env bash

set -euo pipefail

version="${1:?usage: write-release-summary.sh VERSION DIST_DIR IMAGE DIGEST}"
dist_dir="${2:?usage: write-release-summary.sh VERSION DIST_DIR IMAGE DIGEST}"
image="${3:?usage: write-release-summary.sh VERSION DIST_DIR IMAGE DIGEST}"
digest="${4:?usage: write-release-summary.sh VERSION DIST_DIR IMAGE DIGEST}"

: "${ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL:?ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL is required}"
: "${S3_PUBLIC_DOWNLOAD_BASE_URL:?S3_PUBLIC_DOWNLOAD_BASE_URL is required}"
: "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY is required}"

[[ "$version" =~ ^[a-z0-9][a-z0-9._-]{0,127}$ ]]
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]

oss_base_url="${ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL%/}"
s3_base_url="${S3_PUBLIC_DOWNLOAD_BASE_URL%/}"
[[ "$oss_base_url" =~ ^https://[^[:space:]]+$ ]]
[[ "$s3_base_url" =~ ^https://[^[:space:]]+$ ]]

shopt -s nullglob
artifacts=("$dist_dir"/*.tar.gz)
test "${#artifacts[@]}" -gt 0
artifacts+=("$dist_dir/checksums.txt")
if [[ -f "$dist_dir/image-digest.txt" ]]; then
  artifacts+=("$dist_dir/image-digest.txt")
fi

for artifact in "${artifacts[@]}"; do
  test -f "$artifact"
  [[ "${artifact##*/}" =~ ^[a-zA-Z0-9._-]+$ ]]
done

{
  echo '### Published artifacts'
  echo
  echo "Version: \`$version\`"
  echo
  echo '| Artifact | Public downloads |'
  echo '| --- | --- |'
  for artifact in "${artifacts[@]}"; do
    name="${artifact##*/}"
    echo "| \`$name\` | [OSS]($oss_base_url/$version/$name) · [S3]($s3_base_url/$version/$name) |"
  done
  echo
  echo '### Container image'
  echo
  echo "- Reference: \`$image:$version\`"
  echo "- Immutable reference: \`$image@$digest\`"
  echo "- Digest: \`$digest\`"
} >> "$GITHUB_STEP_SUMMARY"
