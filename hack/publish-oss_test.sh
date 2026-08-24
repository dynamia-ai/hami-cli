#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

dist_dir="$test_dir/dist"
mkdir -p "$dist_dir"
for platform in \
  darwin_amd64 \
  darwin_arm64 \
  linux_amd64 \
  linux_arm64 \
  windows_amd64; do
  printf '%s\n' "$platform" >"$dist_dir/hami-cli_v1.2.3_${platform}.tar.gz"
done
(
  cd "$dist_dir"
  sha256sum hami-cli_v1.2.3_*.tar.gz >checksums.txt
)
printf '%s\n' 'sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef' \
  >"$dist_dir/image-digest.txt"

ossutil() {
  printf '%q ' "$@" >>"$OSSUTIL_CALL_LOG"
  printf '\n' >>"$OSSUTIL_CALL_LOG"
  case "$1 ${2:-}" in
    "api get-bucket-versioning")
      printf '{\n  "Status": "%s"\n}\n\n0.123456(s) elapsed\n' \
        "$OSSUTIL_VERSIONING_STATUS"
      ;;
    stat\ *)
      echo '404 NoSuchKey' >&2
      return 1
      ;;
    "api put-object")
      return 0
      ;;
    *)
      echo "unexpected ossutil invocation: $*" >&2
      return 98
      ;;
  esac
}
export -f ossutil

export OSSUTIL_CALL_LOG="$test_dir/ossutil.log"
export OSSUTIL_VERSIONING_STATUS=Disabled
export ALIYUN_OSS_ACCESS_KEY_ID=test-access-key
export ALIYUN_OSS_ACCESS_KEY_SECRET=test-secret-key
export ALIYUN_OSS_BUCKET=test-bucket
export ALIYUN_OSS_REGION=cn-hangzhou
export ALIYUN_OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com

bash "$repo_root/hack/publish-oss.sh" immutable v1.2.3 "$dist_dir"

test "$(grep -c 'get-bucket-versioning' "$OSSUTIL_CALL_LOG")" -eq 1
test "$(grep -c '^stat ' "$OSSUTIL_CALL_LOG")" -eq 7
test "$(grep -c '^api put-object ' "$OSSUTIL_CALL_LOG")" -eq 7
test "$(grep -c -- '--forbid-overwrite=true' "$OSSUTIL_CALL_LOG")" -eq 7

export OSSUTIL_CALL_LOG="$test_dir/versioning-enabled-ossutil.log"
export OSSUTIL_VERSIONING_STATUS=Enabled
if bash "$repo_root/hack/publish-oss.sh" immutable v1.2.3 "$dist_dir" \
  >"$test_dir/versioning-enabled.log" 2>&1; then
  echo 'immutable publication must reject a versioning-enabled OSS bucket' >&2
  exit 1
fi
grep -Fq \
  'x-oss-forbid-overwrite is ineffective when versioning is Enabled' \
  "$test_dir/versioning-enabled.log"
test "$(grep -c 'get-bucket-versioning' "$OSSUTIL_CALL_LOG")" -eq 1
! grep -Eq '^(stat|api put-object) ' "$OSSUTIL_CALL_LOG"

export OSSUTIL_CALL_LOG="$test_dir/mutable-ossutil.log"
bash "$repo_root/hack/publish-oss.sh" mutable v1.2.3 "$dist_dir"

! grep -q 'get-bucket-versioning' "$OSSUTIL_CALL_LOG"
! grep -q '^stat ' "$OSSUTIL_CALL_LOG"
test "$(grep -c '^api put-object ' "$OSSUTIL_CALL_LOG")" -eq 6
! grep -q -- '--forbid-overwrite=true' "$OSSUTIL_CALL_LOG"
