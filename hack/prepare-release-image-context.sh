#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:?usage: prepare-release-image-context.sh DIST_DIR OUTPUT_DIR}"
output_dir="${2:?usage: prepare-release-image-context.sh DIST_DIR OUTPUT_DIR}"
artifacts_file="$dist_dir/artifacts.json"

test -f "$artifacts_file"
mkdir -p "$output_dir/linux/amd64" "$output_dir/linux/arm64"
cp Dockerfile.goreleaser "$output_dir/Dockerfile"

for arch in amd64 arm64; do
  mapfile -t binaries < <(
    jq -r --arg arch "$arch" '
      .[]
      | select(.type == "Binary" and .goos == "linux" and .goarch == $arch)
      | .path
    ' "$artifacts_file"
  )
  test "${#binaries[@]}" -eq 1
  test -x "${binaries[0]}"
  cp -p "${binaries[0]}" "$output_dir/linux/$arch/hami-cli"
done
