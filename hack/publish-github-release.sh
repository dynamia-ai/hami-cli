#!/usr/bin/env bash

set -euo pipefail

tag="${1:?usage: publish-github-release.sh TAG DIST_DIR PUBLIC_URL IMAGE}"
dist_dir="${2:?usage: publish-github-release.sh TAG DIST_DIR PUBLIC_URL IMAGE}"
public_url="${3:?usage: publish-github-release.sh TAG DIST_DIR PUBLIC_URL IMAGE}"
image="${4:?usage: publish-github-release.sh TAG DIST_DIR PUBLIC_URL IMAGE}"

if ! gh release view "$tag" >/dev/null 2>&1; then
  notes="$(mktemp)"
  printf '%s\n\n%s\n\n%s\n' \
    "Public binary downloads: $public_url/$tag/" \
    "Container image: $image:$tag" \
    'This private-repository release is an authenticated mirror; OSS is the public binary distribution endpoint.' \
    > "$notes"
  gh release create "$tag" --verify-tag --title "$tag" --latest=false --notes-file "$notes"
  rm -f "$notes"
fi

shopt -s nullglob
artifacts=("$dist_dir"/*.tar.gz "$dist_dir/checksums.txt" "$dist_dir/image-digest.txt")
test "${#artifacts[@]}" -eq 7
for archive in "$dist_dir"/*.tar.gz; do
  [[ "${archive##*/}" = "hami-cli_${tag}_"*.tar.gz ]]
done
existing_assets="$(gh release view "$tag" --json assets --jq '.assets[].name')"

for artifact in "${artifacts[@]}"; do
  name="${artifact##*/}"
  if grep -Fxq "$name" <<< "$existing_assets"; then
    download_dir="$(mktemp -d)"
    gh release download "$tag" --pattern "$name" --dir "$download_dir"
    if ! cmp "$artifact" "$download_dir/$name"; then
      echo "refusing to overwrite immutable GitHub Release asset: $name" >&2
      rm -rf "$download_dir"
      exit 1
    fi
    rm -rf "$download_dir"
  else
    gh release upload "$tag" "$artifact"
  fi
done
