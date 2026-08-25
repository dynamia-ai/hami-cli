#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "unsupported test operating system" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "unsupported test architecture" >&2; exit 1 ;;
esac

version=latest
platform="${os}_${arch}"
archive="hami-cli_${version}_${platform}.tar.gz"
fixture_root="$test_dir/fixtures"
fallback_fixture_root="$test_dir/fallback-fixtures"
release_dir="$fixture_root/hami-cli/$version"
fallback_release_dir="$fallback_fixture_root/hami-cli/$version"
archive_root="$test_dir/archive/hami-cli_${version}_${platform}"
mkdir -p "$release_dir" "$fallback_release_dir" "$archive_root" "$test_dir/mock-bin" "$test_dir/home"

cat > "$archive_root/hami-cli" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$HOME/hami-cli-invocations"
case "$1" in
  completion) printf '%s completion\n' "$2" ;;
  version) printf 'hami-cli test-version\n' ;;
esac
if [ "$1" = skill ] && [ "${HAMI_CLI_TEST_FAIL_SKILL:-}" = 1 ]; then
  exit 1
fi
EOF
chmod 0755 "$archive_root/hami-cli"
tar -czf "$release_dir/$archive" -C "$test_dir/archive" "hami-cli_${version}_${platform}"
if command -v sha256sum >/dev/null; then
  sha256sum "$release_dir/$archive" | awk '{ print $1 }' > "$release_dir/$archive.sha256"
else
  shasum -a 256 "$release_dir/$archive" | awk '{ print $1 }' > "$release_dir/$archive.sha256"
fi
cp "$release_dir/$archive" "$release_dir/$archive.sha256" "$fallback_release_dir/"

cat > "$test_dir/mock-bin/curl" <<'EOF'
#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    --connect-timeout | --max-time | --proto | --proto-redir) shift 2 ;;
    --tlsv1.2 | --location | --fail | --silent | --show-error) shift ;;
    *) url=$1; shift ;;
  esac
done
case "$url" in
  https://storage.dynamia.ai/*)
    root=$FIXTURE_ROOT
    path=${url#https://storage.dynamia.ai/}
    ;;
  https://fixtures.invalid/*)
    root=$FIXTURE_ROOT
    path=${url#https://fixtures.invalid/}
    ;;
  https://public.hami.run/*)
    root=$FALLBACK_FIXTURE_ROOT
    path=${url#https://public.hami.run/}
    ;;
  *) exit 22 ;;
esac
printf '%s\n' "$url" >> "$CURL_INVOCATIONS"
cp "$root/$path" "$output"
EOF
chmod 0755 "$test_dir/mock-bin/curl"

cat > "$test_dir/mock-bin/sudo" <<'EOF'
#!/bin/sh
echo 'installer unexpectedly invoked sudo' >&2
exit 1
EOF
chmod 0755 "$test_dir/mock-bin/sudo"

rendered_dir="$test_dir/rendered"
bash "$repo_root/hack/prepare-installers.sh" "$rendered_dir"
grep -Fx 'default_base_url="https://storage.dynamia.ai/hami-cli"' "$rendered_dir/install-oss.sh"
grep -Fx 'default_base_url="https://public.hami.run/hami-cli"' "$rendered_dir/install-r2.sh"

curl_invocations="$test_dir/curl-invocations"
: > "$curl_invocations"
PATH="$test_dir/mock-bin:$PATH" \
  HOME="$test_dir/home" \
  XDG_CONFIG_HOME=relative-config \
  FIXTURE_ROOT="$fixture_root" \
  FALLBACK_FIXTURE_ROOT="$fallback_fixture_root" \
  CURL_INVOCATIONS="$curl_invocations" \
  HAMI_CLI_AGENT=codex \
  sh "$rendered_dir/install-oss.sh" > "$test_dir/install.out"

test -x "$test_dir/home/.local/bin/hami-cli"
test "$(cat "$test_dir/home/.local/share/bash-completion/completions/hami-cli")" = \
  'bash completion'
test "$(cat "$test_dir/home/.local/share/zsh/site-functions/_hami-cli")" = \
  'zsh completion'
test "$(cat "$test_dir/home/.config/fish/completions/hami-cli.fish")" = \
  'fish completion'
grep -Fx "Installed hami-cli to $test_dir/home/.local/bin/hami-cli" "$test_dir/install.out"
grep -Fx "Add $test_dir/home/.local/bin to PATH before using hami-cli from a new shell." \
  "$test_dir/install.out"
test "$(sed -n '1p' "$curl_invocations")" = \
  "https://storage.dynamia.ai/hami-cli/$version/$archive"
grep -Fx 'completion bash' "$test_dir/home/hami-cli-invocations"
grep -Fx 'completion zsh' "$test_dir/home/hami-cli-invocations"
grep -Fx 'completion fish' "$test_dir/home/hami-cli-invocations"
grep -Fx 'skill install --scope user --yes --agent codex' "$test_dir/home/hami-cli-invocations"
grep -Fx 'version' "$test_dir/home/hami-cli-invocations"

failure_home="$test_dir/failure-home"
if PATH="$test_dir/mock-bin:$PATH" \
  HOME="$failure_home" \
  FIXTURE_ROOT="$fixture_root" \
  FALLBACK_FIXTURE_ROOT="$fallback_fixture_root" \
  CURL_INVOCATIONS="$curl_invocations" \
  HAMI_CLI_AGENT=codex \
  HAMI_CLI_TEST_FAIL_SKILL=1 \
  sh "$rendered_dir/install-oss.sh" > "$test_dir/skill-failure.out" 2>&1; then
  echo "installer accepted a failed Skill installation" >&2
  exit 1
fi
grep -Fx "Add $failure_home/.local/bin to PATH before using hami-cli from a new shell." \
  "$test_dir/skill-failure.out"
grep -Fi 'Skill installation failed' "$test_dir/skill-failure.out"

: > "$curl_invocations"
PATH="$test_dir/mock-bin:$test_dir/r2-bin:$PATH" \
  HOME="$test_dir/home" \
  XDG_CONFIG_HOME="$test_dir/r2-config" \
  FIXTURE_ROOT="$fixture_root" \
  FALLBACK_FIXTURE_ROOT="$fallback_fixture_root" \
  CURL_INVOCATIONS="$curl_invocations" \
  HAMI_CLI_INSTALL_DIR="$test_dir/r2-bin/" \
  HAMI_CLI_AGENT=codex \
  sh "$rendered_dir/install-r2.sh" > "$test_dir/r2.out"
test -x "$test_dir/r2-bin/hami-cli"
test "$(cat "$test_dir/r2-bin/share/bash-completion/completions/hami-cli")" = \
  'bash completion'
test "$(cat "$test_dir/r2-bin/share/zsh/site-functions/_hami-cli")" = \
  'zsh completion'
test "$(cat "$test_dir/r2-config/fish/completions/hami-cli.fish")" = \
  'fish completion'
test "$(sed -n '1p' "$curl_invocations")" = \
  "https://public.hami.run/hami-cli/$version/$archive"
if grep -F 'Add ' "$test_dir/r2.out"; then
  echo "installer reported an existing PATH entry as missing" >&2
  exit 1
fi

printf '%064d\n' 0 > "$release_dir/$archive.sha256"
PATH="$test_dir/mock-bin:$PATH" \
  HOME="$test_dir/home" \
  FIXTURE_ROOT="$fixture_root" \
  FALLBACK_FIXTURE_ROOT="$fallback_fixture_root" \
  CURL_INVOCATIONS="$curl_invocations" \
  HAMI_CLI_BASE_URL=https://fixtures.invalid/hami-cli \
  HAMI_CLI_INSTALL_DIR="$test_dir/fallback-bin" \
  HAMI_CLI_AGENT=codex \
  sh "$repo_root/install.sh"
test -x "$test_dir/fallback-bin/hami-cli"

printf '%064d\n' 0 > "$fallback_release_dir/$archive.sha256"
if PATH="$test_dir/mock-bin:$PATH" \
  HOME="$test_dir/home" \
  FIXTURE_ROOT="$fixture_root" \
  FALLBACK_FIXTURE_ROOT="$fallback_fixture_root" \
  CURL_INVOCATIONS="$curl_invocations" \
  HAMI_CLI_BASE_URL=https://fixtures.invalid/hami-cli \
  HAMI_CLI_INSTALL_DIR="$test_dir/install-bin" \
  HAMI_CLI_AGENT=codex \
  sh "$repo_root/install.sh" >"$test_dir/checksum.out" 2>&1; then
  echo "installer accepted an invalid checksum" >&2
  exit 1
fi
grep -Fi 'checksum mismatch' "$test_dir/checksum.out"
