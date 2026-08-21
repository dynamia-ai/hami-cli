#!/usr/bin/env bash

# Copyright 2026 The HAMi Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

version="${VERSION:?VERSION is required}"
revision="${REVISION:?REVISION is required}"
build_date="${BUILD_DATE:?BUILD_DATE is required}"
source_date_epoch="${SOURCE_DATE_EPOCH:-}"
dist_dir="dist"

if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "VERSION must be a stable semantic version without the v prefix: ${version}" >&2
  exit 1
fi

if [[ -n "${source_date_epoch}" && ! "${source_date_epoch}" =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be a Unix timestamp: ${source_date_epoch}" >&2
  exit 1
fi

case "${dist_dir}" in
  ""|/|.)
    echo "Refusing unsafe DIST_DIR: ${dist_dir}" >&2
    exit 1
    ;;
esac

rm -rf -- "${dist_dir}"
mkdir -p "${dist_dir}"

work_dir="$(mktemp -d)"
trap 'rm -rf -- "${work_dir}"' EXIT

platforms=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
)

for platform in "${platforms[@]}"; do
  goos="${platform%/*}"
  goarch="${platform#*/}"
  binary="hami-cli"
  if [[ "${goos}" == "windows" ]]; then
    binary="hami-cli.exe"
  fi

  package="hami-cli_v${version}_${goos}_${goarch}"
  package_dir="${work_dir}/${package}"
  mkdir -p "${package_dir}"

  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" GOWORK=off \
    go build -trimpath \
    -ldflags "-s -w -X main.cliVersion=${version} -X main.cliRevision=${revision} -X main.cliBuildDate=${build_date}" \
    -o "${package_dir}/${binary}" .

  cp LICENSE "${package_dir}/LICENSE"
  if [[ -n "${source_date_epoch}" ]]; then
    if ! tar --version 2>/dev/null | grep -q 'GNU tar'; then
      echo "Reproducible release archives require GNU tar" >&2
      exit 1
    fi
    tar --sort=name --mtime="@${source_date_epoch}" \
      --owner=0 --group=0 --numeric-owner \
      -C "${work_dir}" -czf "${dist_dir}/${package}.tar.gz" "${package}"
  else
    COPYFILE_DISABLE=1 tar -C "${work_dir}" -czf "${dist_dir}/${package}.tar.gz" "${package}"
  fi
  rm -rf -- "${package_dir}"
done

(
  cd "${dist_dir}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > checksums.txt
  else
    shasum -a 256 ./*.tar.gz > checksums.txt
  fi
)
