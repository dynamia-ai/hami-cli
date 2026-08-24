#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

write_checksum() {
  local directory="$1"
  local archive="$2"
  if command -v sha256sum >/dev/null; then
    (
      cd "$directory"
      sha256sum "$archive" | awk '{ print $1 }' > "$archive.sha256"
    )
  else
    (
      cd "$directory"
      shasum -a 256 "$archive" | awk '{ print $1 }' > "$archive.sha256"
    )
  fi
}

mkdir -p "$test_dir/valid/hami-cli_v1.2.3_linux_amd64"
touch "$test_dir/valid/hami-cli_v1.2.3_linux_amd64/hami-cli"
tar -czf "$test_dir/valid/hami-cli_v1.2.3_linux_amd64.tar.gz" \
  -C "$test_dir/valid" hami-cli_v1.2.3_linux_amd64
write_checksum "$test_dir/valid" hami-cli_v1.2.3_linux_amd64.tar.gz
bash "$repo_root/hack/verify-release-archives.sh" "$test_dir/valid"

mkdir -p "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64"
touch \
  "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64/hami-cli" \
  "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64/unexpected.txt"
tar -czf "$test_dir/invalid/hami-cli_v1.2.3_linux_amd64.tar.gz" \
  -C "$test_dir/invalid" hami-cli_v1.2.3_linux_amd64
write_checksum "$test_dir/invalid" hami-cli_v1.2.3_linux_amd64.tar.gz
if bash "$repo_root/hack/verify-release-archives.sh" "$test_dir/invalid" >/dev/null 2>&1; then
  echo "archive verification accepted an extra file" >&2
  exit 1
fi

mkdir -p "$test_dir/missing-checksum"
cp "$test_dir/valid/hami-cli_v1.2.3_linux_amd64.tar.gz" "$test_dir/missing-checksum/"
if bash "$repo_root/hack/verify-release-archives.sh" "$test_dir/missing-checksum" >/dev/null 2>&1; then
  echo "archive verification accepted a missing checksum" >&2
  exit 1
fi

mkdir -p "$test_dir/invalid-checksum"
cp "$test_dir/valid/hami-cli_v1.2.3_linux_amd64.tar.gz" "$test_dir/invalid-checksum/"
printf '%064d\n' 0 \
  > "$test_dir/invalid-checksum/hami-cli_v1.2.3_linux_amd64.tar.gz.sha256"
if bash "$repo_root/hack/verify-release-archives.sh" "$test_dir/invalid-checksum" >/dev/null 2>&1; then
  echo "archive verification accepted an invalid checksum" >&2
  exit 1
fi
