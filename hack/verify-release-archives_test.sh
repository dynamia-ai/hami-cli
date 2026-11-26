#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

version=v1.2.3
fixtures="$test_dir/fixtures"
dist="$test_dir/dist"
mkdir -p "$fixtures" "$dist"

pack() {
  local name="$1"
  tar -czf "$dist/$name.tar.gz" -C "$fixtures" "$name"
  shasum -a 256 "$dist/$name.tar.gz" | awk '{ print $1 }' > "$dist/$name.tar.gz.sha256"
}

for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64; do
  name="hami-cli_${version}_$platform"
  binary=hami-cli
  [[ "$platform" = windows_* ]] && binary=hami-cli.exe
  mkdir -p "$fixtures/$name"
  touch "$fixtures/$name/$binary"
  pack "$name"
done

verify() {
  bash "$script_dir/verify-release-archives.sh" "$dist" "$1"
}

expect_failure() {
  if verify "$version" >/dev/null 2>&1; then
    echo "archive verification accepted $1" >&2
    exit 1
  fi
}

verify "$version"
if verify v1.2.4 >/dev/null 2>&1; then
  echo "archive verification accepted files from a different version" >&2
  exit 1
fi

name="hami-cli_${version}_linux_amd64"
mv "$dist/$name.tar.gz" "$test_dir/"
mv "$dist/$name.tar.gz.sha256" "$test_dir/"
expect_failure "a missing platform"

unknown="hami-cli_${version}_windows_arm64"
cp "$test_dir/$name.tar.gz" "$dist/$unknown.tar.gz"
cp "$test_dir/$name.tar.gz.sha256" "$dist/$unknown.tar.gz.sha256"
expect_failure "an unsupported platform in place of a supported one"
rm "$dist/$unknown.tar.gz" "$dist/$unknown.tar.gz.sha256"

mv "$test_dir/$name.tar.gz" "$dist/"
expect_failure "a missing checksum"
printf '%064d\n' 0 > "$dist/$name.tar.gz.sha256"
expect_failure "a mismatched checksum"

touch "$fixtures/$name/unexpected.txt"
pack "$name"
expect_failure "an extra file"
rm "$fixtures/$name/unexpected.txt"

mv "$fixtures/$name/hami-cli" "$fixtures/$name/hami-cli.exe"
pack "$name"
expect_failure "the Windows executable name in a Linux archive"
rm "$fixtures/$name/hami-cli.exe"
ln -s hami-cli.exe "$fixtures/$name/hami-cli"
pack "$name"
expect_failure "a symbolic link in place of the executable"
