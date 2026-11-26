#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

export GITHUB_REPOSITORY=example/hami-cli
export GITHUB_OUTPUT="$test_dir/output"
export PATH="$test_dir/bin:$PATH"
mkdir "$test_dir/bin"

cat > "$test_dir/bin/gh" <<'MOCK'
#!/usr/bin/env bash

set -euo pipefail

printf '%s\n' "$*" >> "$mock_dir/log"
release="$mock_dir/release.json"
case "$*" in
  'release create '*)
    [[ " $* " = *' --verify-tag '* && " $* " = *' --draft '* && " $* " = *' --generate-notes '* ]]
    prerelease=false
    if [[ " $* " = *' --prerelease '* ]]; then prerelease=true; fi
    jq -n --arg tag "$3" --argjson prerelease "$prerelease" '{
      tag_name: $tag, draft: true, prerelease: $prerelease, assets: [],
      html_url: ("https://github.com/example/hami-cli/releases/tag/" + $tag)
    }' > "$release"
    ;;
  'release upload '*)
    shift 3
    for file in "$@"; do
      [[ -f "$file" ]] || continue
      cp "$file" "$mock_dir/assets/"
      digest="$(shasum -a 256 "$file" | awk '{ print $1 }')"
      if [[ "${MOCK_UPLOAD_MISMATCH:-}" = "${file##*/}" ]]; then digest=wrong; fi
      jq --arg name "${file##*/}" --arg digest "sha256:$digest" '
        .assets |= (map(select(.name != $name)) + [{name: $name, digest: $digest, state: "uploaded"}])
      ' "$release" > "$mock_dir/updated.json"
      mv "$mock_dir/updated.json" "$release"
    done
    ;;
  'api --method PATCH '*)
    prerelease=false
    if [[ " $* " = *' prerelease=true '* ]]; then prerelease=true; fi
    if [[ "$prerelease" = true ]]; then
      [[ "$*" = *'make_latest=false' ]]
    else
      [[ "$*" = *'make_latest=legacy' ]]
    fi
    jq --argjson prerelease "$prerelease" '.draft = false | .prerelease = $prerelease' \
      "$release" > "$mock_dir/updated.json"
    mv "$mock_dir/updated.json" "$release"
    cat "$release"
    ;;
  'api graphql '*)
    if [[ -f "$release" ]]; then
      jq '{data: {repository: {release: {databaseId: 1}, latestRelease:
        (if .prerelease then null else {isDraft: .draft, isPrerelease: .prerelease} end)
      }}}' "$release"
    else
      echo '{"data":{"repository":{"release":null,"latestRelease":null}}}'
    fi
    ;;
  'api repos/example/hami-cli/releases/1')
    cat "$release"
    ;;
  *) exit 99 ;;
esac
MOCK
chmod +x "$test_dir/bin/gh"

prepare_case() {
  local tag="$1"
  export mock_dir="$test_dir/$tag"
  dist="$mock_dir/dist"
  mkdir -p "$mock_dir/assets" "$dist"
  for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64; do
    name="hami-cli_${tag}_$platform"
    binary=hami-cli
    [[ "$platform" = windows_* ]] && binary=hami-cli.exe
    mkdir "$mock_dir/$name"
    touch "$mock_dir/$name/$binary"
    tar -czf "$dist/$name.tar.gz" -C "$mock_dir" "$name"
    shasum -a 256 "$dist/$name.tar.gz" | awk '{ print $1 }' > "$dist/$name.tar.gz.sha256"
  done
  printf 'sha256:%064d\n' 0 > "$dist/image-digest.txt"
}

publish() {
  BASH_ENV=/dev/null bash "$script_dir/publish-github-release.sh" "$1" "$dist"
}

tag=v1.2.3
prepare_case "$tag"
mv "$dist/image-digest.txt" "$mock_dir/"
if publish "$tag" >/dev/null 2>&1 || [[ -e "$mock_dir/log" ]]; then
  echo "publisher contacted GitHub without a complete payload" >&2
  exit 1
fi
mv "$mock_dir/image-digest.txt" "$dist/"

if MOCK_UPLOAD_MISMATCH=image-digest.txt publish "$tag" >"$mock_dir/result" 2>&1; then
  echo "publisher accepted a mismatched GitHub asset" >&2
  exit 1
fi
if ! grep -Fq 'missing or mismatched GitHub Release asset: image-digest.txt' "$mock_dir/result"; then
  cat "$mock_dir/result" >&2
  exit 1
fi
jq -e '.draft and (.prerelease | not)' "$mock_dir/release.json" >/dev/null

publish "$tag"
jq -e '(.draft | not) and (.prerelease | not)' "$mock_dir/release.json" >/dev/null
(
  cd "$mock_dir/assets"
  test "$(wc -l < checksums.txt)" -eq 5
  shasum -a 256 -c checksums.txt
  for artifact in "$dist"/*; do
    cmp "$artifact" "${artifact##*/}"
  done
)
test "$(grep -c '^release create ' "$mock_dir/log")" -eq 1
grep -q "^release upload $tag [^ ]*/image-digest.txt --repo $GITHUB_REPOSITORY --clobber$" "$mock_dir/log"
grep -Fxq "url=https://github.com/$GITHUB_REPOSITORY/releases/tag/$tag" "$GITHUB_OUTPUT"

: > "$mock_dir/log"
publish "$tag"
if grep -Eq '^(release (create|upload)|api --method PATCH)' "$mock_dir/log"; then
  echo "publisher changed an intact release on retry" >&2
  exit 1
fi

tag=v1.2.4-rc.1
prepare_case "$tag"
publish "$tag"
jq -e '(.draft | not) and .prerelease' "$mock_dir/release.json" >/dev/null

for tag in v1.2 v01.2.3 v1.2.3-01 v1.2.3-RC.1 v1.2.3+metadata; do
  if bash "$script_dir/classify-release-tag.sh" "$tag" >/dev/null 2>&1; then
    echo "accepted unsupported release tag: $tag" >&2
    exit 1
  fi
done
