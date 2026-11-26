#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tag="${1:?usage: publish-github-release.sh TAG DIST_DIR}"
dist_dir="${2:?usage: publish-github-release.sh TAG DIST_DIR}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
release_type="$(bash "$script_dir/classify-release-tag.sh" "$tag")"
prerelease=false
make_latest=legacy
create_flags=(--verify-tag --draft --title "$tag" --generate-notes)
if [[ "$release_type" = prerelease ]]; then
  prerelease=true
  make_latest=false
  create_flags+=(--prerelease)
fi

bash "$script_dir/verify-release-archives.sh" "$dist_dir" "$tag"
image_digest="$(cat "$dist_dir/image-digest.txt")"
[[ "$image_digest" =~ ^sha256:[0-9a-f]{64}$ ]]

if command -v sha256sum >/dev/null; then
  checksum_command=(sha256sum)
else
  checksum_command=(shasum -a 256)
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
artifacts=()
for archive in "$dist_dir"/*.tar.gz; do
  printf '%s  %s\n' "$(cat "$archive.sha256")" "${archive##*/}" >> "$workdir/checksums.txt"
  artifacts+=("$archive" "$archive.sha256")
done
artifacts+=("$dist_dir/image-digest.txt" "$workdir/checksums.txt")

release_metadata() {
  # GraphQL finds drafts by their pending tag name.
  # shellcheck disable=SC2016
  gh api graphql -f owner="${repo%/*}" -f name="${repo#*/}" -f tag="$tag" -f query='
    query($owner: String!, $name: String!, $tag: String!) {
      repository(owner: $owner, name: $name) {
        release(tagName: $tag) { databaseId }
        latestRelease { isDraft isPrerelease }
      }
    }
  '
}

metadata="$(release_metadata)"
id="$(jq -er '.data.repository | objects | .release.databaseId // ""' <<< "$metadata")"
if [[ -z "$id" ]]; then
  gh release create "$tag" --repo "$repo" "${create_flags[@]}"
  metadata="$(release_metadata)"
  id="$(jq -er '.data.repository.release.databaseId' <<< "$metadata")"
fi
[[ "$id" =~ ^[0-9]+$ ]]
release_path="repos/$repo/releases/$id"

asset_matches() {
  jq -e --arg name "$1" --arg digest "$2" '
    any(.assets[]; .name == $name and .state == "uploaded" and .digest == $digest)
  ' <<< "$release" >/dev/null
}

release="$(gh api "$release_path")"
digests=()
uploads=()
for artifact in "${artifacts[@]}"; do
  digest="$("${checksum_command[@]}" "$artifact" | awk '{ print $1 }')"
  digests+=("sha256:$digest")
  if ! asset_matches "${artifact##*/}" "sha256:$digest"; then
    uploads+=("$artifact")
  fi
done
if ((${#uploads[@]} > 0)); then
  gh release upload "$tag" "${uploads[@]}" --repo "$repo" --clobber
  release="$(gh api "$release_path")"
fi

# Verify the uploaded bytes before exposing a new release or publishing a retry.
for i in "${!artifacts[@]}"; do
  name="${artifacts[i]##*/}"
  if ! asset_matches "$name" "${digests[i]}"; then
    echo "missing or mismatched GitHub Release asset: $name" >&2
    exit 1
  fi
done

if ! jq -e --argjson prerelease "$prerelease" \
  '.draft == false and .prerelease == $prerelease' <<< "$release" >/dev/null; then
  release="$(gh api --method PATCH "$release_path" \
    -F draft=false -F "prerelease=$prerelease" -f "make_latest=$make_latest")"
fi
url="$(jq -er --arg tag "$tag" --argjson prerelease "$prerelease" '
  select(.tag_name == $tag and .draft == false and .prerelease == $prerelease) | .html_url
' <<< "$release")"

metadata="$(release_metadata)"
if ! jq -e --argjson prerelease "$prerelease" '
  .data.repository | objects | .latestRelease as $latest
  | if $latest == null then $prerelease
    else $latest.isDraft == false and $latest.isPrerelease == false
    end
' <<< "$metadata" >/dev/null; then
  echo "GitHub Latest does not point to a published stable release after publishing $tag" >&2
  exit 1
fi

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'url=%s\n' "$url" >> "$GITHUB_OUTPUT"
fi
printf '%s\n' "$url"
