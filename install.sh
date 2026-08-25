#!/bin/sh

set -eu

version="${HAMI_CLI_VERSION:-latest}"
install_dir="${HAMI_CLI_INSTALL_DIR:-/usr/local/bin}"
primary_base_url="${HAMI_CLI_BASE_URL:-https://storage.dynamia.ai/hami-cli}"
fallback_base_url="https://public.hami.run/hami-cli"

fail() {
  printf 'hami-cli installer: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

case "$version" in
  '' | *[!a-z0-9._-]* | [._-]*) fail "invalid version: $version" ;;
esac

case "$primary_base_url" in
  https://*) ;;
  *) fail "HAMI_CLI_BASE_URL must use HTTPS" ;;
esac
primary_base_url=${primary_base_url%/}

require_command curl
require_command install
require_command mktemp
require_command tar
require_command uname

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported operating system: $(uname -s); use a release archive for manual installation" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

platform="${os}_${arch}"
archive="hami-cli_${version}_${platform}.tar.gz"
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' 0
trap 'exit 1' HUP INT TERM

if command -v sha256sum >/dev/null 2>&1; then
  checksum_command=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  checksum_command='shasum -a 256'
else
  fail "sha256sum or shasum is required"
fi

download_release() {
  for base_url in "$primary_base_url" "$fallback_base_url"; do
    rm -f "$work_dir/$archive" "$work_dir/$archive.sha256"
    printf 'Downloading hami-cli %s for %s from %s\n' "$version" "$platform" "$base_url"
    if ! curl --connect-timeout 15 --max-time 180 \
      --proto '=https' --proto-redir '=https' --tlsv1.2 --location --fail --silent --show-error \
      --output "$work_dir/$archive" "$base_url/$version/$archive" \
      || ! curl --connect-timeout 15 --max-time 180 \
        --proto '=https' --proto-redir '=https' --tlsv1.2 --location --fail --silent --show-error \
        --output "$work_dir/$archive.sha256" "$base_url/$version/$archive.sha256"; then
      printf 'Download failed from %s; trying the next mirror\n' "$base_url" >&2
      continue
    fi

    expected_digest=$(tr -d '[:space:]' < "$work_dir/$archive.sha256")
    case "$expected_digest" in
      *[!0-9a-f]* | '')
        printf 'Invalid checksum from %s; trying the next mirror\n' "$base_url" >&2
        continue
        ;;
    esac
    if [ "${#expected_digest}" -ne 64 ]; then
      printf 'Invalid checksum from %s; trying the next mirror\n' "$base_url" >&2
      continue
    fi

    # checksum_command contains only one of the two fixed commands selected above.
    # shellcheck disable=SC2086
    actual_digest=$($checksum_command "$work_dir/$archive" | awk '{ print $1 }')
    if [ "$actual_digest" != "$expected_digest" ]; then
      printf 'Checksum mismatch from %s; trying the next mirror\n' "$base_url" >&2
      continue
    fi
    return 0
  done
  return 1
}

download_release || fail "could not download and verify $archive from either public mirror"

mkdir "$work_dir/extracted"
archive_root="hami-cli_${version}_${platform}"
tar -tzf "$work_dir/$archive" > "$work_dir/archive-entries"
if ! awk -v root="$archive_root" \
  '$0 != root "/" && $0 != root "/hami-cli" { exit 1 }' \
  "$work_dir/archive-entries" \
  || [ "$(awk -v file="$archive_root/hami-cli" '$0 == file { count++ } END { print count + 0 }' \
    "$work_dir/archive-entries")" -ne 1 ]; then
  fail "archive contains unexpected files"
fi
tar -xzf "$work_dir/$archive" -C "$work_dir/extracted"
binary="$work_dir/extracted/$archive_root/hami-cli"
[ -f "$binary" ] || fail "archive does not contain the expected hami-cli binary"

install_binary() {
  if [ ! -d "$install_dir" ]; then
    mkdir -p "$install_dir" 2>/dev/null || true
  fi
  if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
    install -m 0755 "$binary" "$install_dir/hami-cli"
    return
  fi
  if [ "$(id -u)" -eq 0 ]; then
    install -m 0755 "$binary" "$install_dir/hami-cli"
    return
  fi
  command -v sudo >/dev/null 2>&1 \
    || fail "cannot write to $install_dir; set HAMI_CLI_INSTALL_DIR to a writable directory"
  sudo mkdir -p "$install_dir"
  sudo install -m 0755 "$binary" "$install_dir/hami-cli"
}

install_binary
installed_binary="$install_dir/hami-cli"
printf 'Installed hami-cli to %s\n' "$installed_binary"

set -- skill install --scope user --yes
if [ -n "${HAMI_CLI_AGENT:-}" ]; then
  set -- "$@" --agent "$HAMI_CLI_AGENT"
fi
if ! "$installed_binary" "$@"; then
  fail "the binary was installed, but Skill installation failed; set HAMI_CLI_AGENT to a supported agent id and retry"
fi

"$installed_binary" version
printf 'hami-cli and its bundled Skill are ready.\n'
