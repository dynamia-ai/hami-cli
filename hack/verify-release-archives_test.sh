#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

mkdir -p "$test_dir/valid/hami-cli_v1.2.3_linux_amd64"
touch "$test_dir/valid/hami-cli_v1.2.3_linux_amd64/hami-cli"
tar -czf "$test_dir/valid/hami-cli_v1.2.3_linux_amd64.tar.gz" \
  -C "$test_dir/valid" hami-cli_v1.2.3_linux_amd64
bash "$repo_root/hack/verify-release-archives.sh" "$test_dir/valid"

mkdir -p "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64"
touch \
  "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64/hami-cli" \
  "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64/unexpected.txt"
tar -czf "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64.tar.gz" \
  -C "$test_dir/invalid" hami-cli_v1.2.3_linux_amd64
if bash "$repo_root/hack/verify-release-archives.sh" "$test_dir/invalid" >/dev/null 2>&1; then
  echo "archive verification accepted an extra file" >&2
  exit 1
fi
