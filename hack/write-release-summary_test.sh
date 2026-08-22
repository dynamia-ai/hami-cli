#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

dist_dir="$test_dir/dist"
mkdir -p "$dist_dir"
touch \
  "$dist_dir/hami-cli_v1.2.3_darwin_amd64.tar.gz" \
  "$dist_dir/hami-cli_v1.2.3_darwin_arm64.tar.gz" \
  "$dist_dir/hami-cli_v1.2.3_linux_amd64.tar.gz" \
  "$dist_dir/hami-cli_v1.2.3_linux_arm64.tar.gz" \
  "$dist_dir/hami-cli_v1.2.3_windows_amd64.tar.gz" \
  "$dist_dir/checksums.txt" \
  "$dist_dir/hami-cli-image.tar"

summary="$test_dir/summary.md"
ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL=https://oss.example.test/hami-cli/ \
S3_PUBLIC_DOWNLOAD_BASE_URL=https://s3.example.test/hami-cli/ \
ACTIONS_ARTIFACT_NAME=release-binaries-v1.2.3 \
ACTIONS_ARTIFACT_RETENTION_DAYS=7 \
ACTIONS_ARTIFACT_URL=https://github.example.test/actions/runs/123/artifacts/456 \
GITHUB_STEP_SUMMARY="$summary" \
  bash "$repo_root/hack/write-release-summary.sh" \
    v1.2.3 "$dist_dir" ghcr.io/example/hami-cli \
    sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

grep -Fqx '### Published artifacts' "$summary"
grep -Fqx '| `hami-cli_v1.2.3_linux_amd64.tar.gz` | [OSS](https://oss.example.test/hami-cli/v1.2.3/hami-cli_v1.2.3_linux_amd64.tar.gz) · [S3](https://s3.example.test/hami-cli/v1.2.3/hami-cli_v1.2.3_linux_amd64.tar.gz) |' "$summary"
grep -Fqx -- '- Reference: `ghcr.io/example/hami-cli:v1.2.3`' "$summary"
grep -Fqx -- '- Immutable reference: `ghcr.io/example/hami-cli@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`' "$summary"
grep -Fqx -- '- Digest: `sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`' "$summary"
grep -Fqx -- '- [release-binaries-v1.2.3](https://github.example.test/actions/runs/123/artifacts/456) requires GitHub sign-in and is retained for 7 days.' "$summary"
test "$(grep -c '^| `' "$summary")" -eq 6
if grep -Fq 'hami-cli-image.tar' "$summary"; then
  echo 'private image archive must not be listed as a public download' >&2
  exit 1
fi

touch "$dist_dir/image-digest.txt"
stable_summary="$test_dir/stable-summary.md"
ALIYUN_OSS_PUBLIC_DOWNLOAD_BASE_URL=https://oss.example.test/hami-cli \
S3_PUBLIC_DOWNLOAD_BASE_URL=https://s3.example.test/hami-cli \
ACTIONS_ARTIFACT_NAME=release-binaries-v1.2.3 \
ACTIONS_ARTIFACT_RETENTION_DAYS=30 \
ACTIONS_ARTIFACT_URL=https://github.example.test/actions/runs/789/artifacts/012 \
GITHUB_STEP_SUMMARY="$stable_summary" \
  bash "$repo_root/hack/write-release-summary.sh" \
    v1.2.3 "$dist_dir" ghcr.io/example/hami-cli \
    sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

grep -Fq '`image-digest.txt`' "$stable_summary"
test "$(grep -c '^| `' "$stable_summary")" -eq 7
