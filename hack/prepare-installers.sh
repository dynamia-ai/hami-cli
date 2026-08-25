#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dist_dir="${1:?usage: prepare-installers.sh DIST_DIR}"
source_installer="$repo_root/install.sh"
oss_base_url=https://storage.dynamia.ai/hami-cli
r2_base_url=https://public.hami.run/hami-cli

test "$(grep -Fxc "default_base_url=\"$oss_base_url\"" "$source_installer")" -eq 1
test "$(grep -Fxc "default_fallback_base_url=\"$r2_base_url\"" "$source_installer")" -eq 1
mkdir -p "$dist_dir"

install -m 0755 "$source_installer" "$dist_dir/install-oss.sh"
sed \
  -e "s|^default_base_url=\"$oss_base_url\"$|default_base_url=\"$r2_base_url\"|" \
  -e "s|^default_fallback_base_url=\"$r2_base_url\"$|default_fallback_base_url=\"$oss_base_url\"|" \
  "$source_installer" > "$dist_dir/install-r2.sh"
chmod 0755 "$dist_dir/install-r2.sh"

test "$(grep -Fxc "default_base_url=\"$r2_base_url\"" "$dist_dir/install-r2.sh")" -eq 1
test "$(grep -Fxc "default_fallback_base_url=\"$oss_base_url\"" "$dist_dir/install-r2.sh")" -eq 1
