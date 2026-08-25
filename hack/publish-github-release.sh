#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tag="${1:?usage: publish-github-release.sh TAG DIST_DIR OSS_PUBLIC_URL S3_PUBLIC_URL IMAGE}"
dist_dir="${2:?usage: publish-github-release.sh TAG DIST_DIR OSS_PUBLIC_URL S3_PUBLIC_URL IMAGE}"
oss_public_url="${3:?usage: publish-github-release.sh TAG DIST_DIR OSS_PUBLIC_URL S3_PUBLIC_URL IMAGE}"
s3_public_url="${4:?usage: publish-github-release.sh TAG DIST_DIR OSS_PUBLIC_URL S3_PUBLIC_URL IMAGE}"
image="${5:?usage: publish-github-release.sh TAG DIST_DIR OSS_PUBLIC_URL S3_PUBLIC_URL IMAGE}"

if ! gh release view "$tag" >/dev/null 2>&1; then
  notes="$(mktemp)"
  printf '%s\n%s\n\n%s\n\n%s\n' \
    "Public binary downloads (OSS): $oss_public_url/$tag/" \
    "Public binary downloads (S3): $s3_public_url/$tag/" \
    "Container image: $image:$tag" \
    'This private-repository release is an authenticated mirror; OSS and S3 are the public binary distribution endpoints.' \
    > "$notes"
  gh release create "$tag" --verify-tag --title "$tag" --latest=false --notes-file "$notes"
  rm -f "$notes"
fi

shopt -s nullglob
artifacts=("$dist_dir"/*.tar.gz "$dist_dir"/*.tar.gz.sha256 "$dist_dir/image-digest.txt")
test "${#artifacts[@]}" -eq 11
for archive in "$dist_dir"/*.tar.gz; do
  [[ "${archive##*/}" = "hami-cli_${tag}_"*.tar.gz ]]
done
bash "$script_dir/verify-release-archives.sh" "$dist_dir"
gh release upload "$tag" "${artifacts[@]}" --clobber
