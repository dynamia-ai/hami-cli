#!/usr/bin/env bash

set -euo pipefail

version="${1:?usage: verify-public-artifacts.sh VERSION DIST_DIR BACKEND=BASE_URL...}"
dist_dir="${2:?usage: verify-public-artifacts.sh VERSION DIST_DIR BACKEND=BASE_URL...}"
shift 2

if (($# == 0)); then
  echo "at least one public artifact backend is required" >&2
  exit 2
fi

if [[ ! "$version" =~ ^[a-z0-9][a-z0-9._-]{0,127}$ ]]; then
  echo "invalid release version: $version" >&2
  exit 2
fi

attempts="${PUBLIC_ARTIFACT_VERIFY_ATTEMPTS:-8}"
retry_delay="${PUBLIC_ARTIFACT_VERIFY_RETRY_DELAY_SECONDS:-5}"
if [[ ! "$attempts" =~ ^[1-9][0-9]*$ ]]; then
  echo "PUBLIC_ARTIFACT_VERIFY_ATTEMPTS must be a positive integer: $attempts" >&2
  exit 2
fi
if [[ ! "$retry_delay" =~ ^[0-9]+$ ]]; then
  echo "PUBLIC_ARTIFACT_VERIFY_RETRY_DELAY_SECONDS must be a non-negative integer: $retry_delay" >&2
  exit 2
fi

if command -v sha256sum >/dev/null; then
  checksum_command=(sha256sum)
elif command -v shasum >/dev/null; then
  checksum_command=(shasum -a 256)
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

shopt -s nullglob
archives=("$dist_dir"/*.tar.gz)
if ((${#archives[@]} == 0)); then
  echo "no release archives found in $dist_dir" >&2
  exit 1
fi

artifacts=()
for archive in "${archives[@]}"; do
  checksum="$archive.sha256"
  if [[ ! -f "$checksum" ]]; then
    echo "missing release archive checksum: $checksum" >&2
    exit 1
  fi
  artifacts+=("$archive" "$checksum")
done
if [[ -f "$dist_dir/image-digest.txt" ]]; then
  artifacts+=("$dist_dir/image-digest.txt")
fi
if [[ "$version" = latest ]]; then
  if [[ ! -f "$dist_dir/install.sh" ]]; then
    echo "missing one-line installer: $dist_dir/install.sh" >&2
    exit 1
  fi
  artifacts+=("$dist_dir/install.sh")
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

print_response_headers() {
  local headers="$1"
  if [[ ! -s "$headers" ]]; then
    echo "response_headers=unavailable"
    return
  fi
  tr -d '\r' < "$headers" \
    | grep -Ei '^(HTTP/|age:|cache-control:|cf-cache-status:|content-length:|content-type:|etag:|last-modified:|location:|server:|via:|x-amz-|x-cache:|x-oss-|x-r2-)' \
    || true
}

append_failure_summary() {
  local backend="$1"
  local name="$2"
  local url="$3"
  local expected_digest="$4"
  local actual_digest="$5"
  local curl_exit="$6"
  local failure_reason="$7"
  local metadata="$8"
  local headers="$9"

  [[ -n "${GITHUB_STEP_SUMMARY:-}" ]] || return 0
  {
    echo "### Public artifact verification failed"
    echo
    printf -- '- Backend: `%s`\n' "$backend"
    printf -- '- Artifact: `%s`\n' "$name"
    printf -- '- URL: `%s`\n' "$url"
    printf -- '- Expected SHA-256: `%s`\n' "$expected_digest"
    printf -- '- Actual SHA-256: `%s`\n' "$actual_digest"
    printf -- '- curl exit code: `%s`\n' "$curl_exit"
    printf -- '- Failure reason: `%s`\n' "$failure_reason"
    echo
    echo '<details><summary>HTTP diagnostics</summary>'
    echo
    echo '```text'
    cat "$metadata"
    print_response_headers "$headers"
    echo '```'
    echo '</details>'
  } >> "$GITHUB_STEP_SUMMARY"
}

for endpoint in "$@"; do
  if [[ "$endpoint" != *=* ]]; then
    echo "invalid public artifact backend, expected BACKEND=BASE_URL: $endpoint" >&2
    exit 2
  fi
  backend="${endpoint%%=*}"
  configured_base_url="${endpoint#*=}"
  if [[ ! "$backend" =~ ^[a-z0-9][a-z0-9._-]*$ ]]; then
    echo "invalid public artifact backend name: $backend" >&2
    exit 2
  fi
  base_url="${configured_base_url%/}"
  if [[ ! "$base_url" =~ ^https://[^[:space:]]+$ ]]; then
    echo "invalid HTTPS public artifact base URL for $backend: $configured_base_url" >&2
    exit 2
  fi

  printf 'verifying public artifacts: backend=%s base_url=%s version=%s artifacts=%d\n' \
    "$backend" "$base_url" "$version" "${#artifacts[@]}"

  for artifact in "${artifacts[@]}"; do
    name="${artifact##*/}"
    url="$base_url/$version/$name"
    [[ "$name" = install.sh ]] && url="$base_url/install.sh"
    expected_digest="$("${checksum_command[@]}" "$artifact" | awk '{ print $1 }')"
    download="$workdir/$backend-$name"
    headers="$workdir/$backend-$name.headers"
    metadata="$workdir/$backend-$name.metadata"

    for ((attempt = 1; attempt <= attempts; attempt++)); do
      rm -f "$download" "$headers" "$metadata"
      curl_exit=0
      curl --connect-timeout 15 --max-time 180 --location --silent --show-error --fail \
        --output "$download" \
        --dump-header "$headers" \
        --write-out $'http_status=%{http_code}\neffective_url=%{url_effective}\nremote_ip=%{remote_ip}\ndownload_bytes=%{size_download}\ntime_seconds=%{time_total}\n' \
        "$url" > "$metadata" || curl_exit=$?

      actual_digest=unavailable
      if [[ -f "$download" ]]; then
        actual_digest="$("${checksum_command[@]}" "$download" | awk '{ print $1 }')"
      fi

      if ((curl_exit == 0)) && [[ "$actual_digest" = "$expected_digest" ]]; then
        http_status="$(sed -n 's/^http_status=//p' "$metadata")"
        printf 'verified public artifact: backend=%s artifact=%s sha256=%s http_status=%s attempt=%d/%d\n' \
          "$backend" "$name" "$expected_digest" "$http_status" "$attempt" "$attempts"
        break
      fi

      failure_reason=sha256_mismatch
      ((curl_exit == 0)) || failure_reason=download_failed

      printf 'public artifact verification attempt failed: backend=%s artifact=%s reason=%s attempt=%d/%d curl_exit=%d expected_sha256=%s actual_sha256=%s url=%s\n' \
        "$backend" "$name" "$failure_reason" "$attempt" "$attempts" "$curl_exit" \
        "$expected_digest" "$actual_digest" "$url" >&2
      cat "$metadata" >&2
      print_response_headers "$headers" >&2

      if ((attempt < attempts)); then
        printf 'retrying public artifact verification in %s seconds\n' "$retry_delay" >&2
        sleep "$retry_delay"
        continue
      fi

      append_failure_summary "$backend" "$name" "$url" "$expected_digest" \
        "$actual_digest" "$curl_exit" "$failure_reason" "$metadata" "$headers"
      exit 1
    done
  done
done
