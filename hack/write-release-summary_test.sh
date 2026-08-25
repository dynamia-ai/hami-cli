#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

dist_dir="$test_dir/dist"
mkdir -p "$dist_dir"
archive="$dist_dir/hami-cli_v1.2.3_linux_amd64.tar.gz"
touch "$archive" "$archive.sha256" "$dist_dir/hami-cli-image.tar"

summary="$test_dir/summary.md"
ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL=https://oss.example.test/hami-cli/ \
S3_PUBLIC_DOWNLOAD_BASE_URL=https://s3.example.test/hami-cli/ \
GITHUB_STEP_SUMMARY="$summary" \
  bash "$repo_root/hack/write-release-summary.sh" \
    v1.2.3 "$dist_dir" ghcr.io/example/hami-cli \
    sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

grep -Fq 'hami-cli_v1.2.3_linux_amd64.tar.gz' "$summary"
grep -Fq 'hami-cli_v1.2.3_linux_amd64.tar.gz.sha256' "$summary"
grep -Fq 'https://oss.example.test/hami-cli/v1.2.3/hami-cli_v1.2.3_linux_amd64.tar.gz' "$summary"
grep -Fq 'https://s3.example.test/hami-cli/v1.2.3/hami-cli_v1.2.3_linux_amd64.tar.gz.sha256' "$summary"
grep -Fq 'ghcr.io/example/hami-cli@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' "$summary"
if grep -Fq 'hami-cli-image.tar' "$summary"; then
  echo 'private image archive must not be listed as a public download' >&2
  exit 1
fi

touch "$dist_dir/image-digest.txt"
stable_summary="$test_dir/stable-summary.md"
ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL=https://oss.example.test/hami-cli \
S3_PUBLIC_DOWNLOAD_BASE_URL=https://s3.example.test/hami-cli \
GITHUB_STEP_SUMMARY="$stable_summary" \
  bash "$repo_root/hack/write-release-summary.sh" \
    v1.2.3 "$dist_dir" ghcr.io/example/hami-cli \
    sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

grep -Fq '`image-digest.txt`' "$stable_summary"
