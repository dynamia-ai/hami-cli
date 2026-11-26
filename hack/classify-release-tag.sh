#!/usr/bin/env bash

set -euo pipefail

tag="${1:?usage: classify-release-tag.sh TAG}"
number='(0|[1-9][0-9]*)'
# Lowercase SemVer identifiers work in both container tags and install.sh.
identifier="(${number}|[0-9]*[a-z-][0-9a-z-]*)"
if ((${#tag} <= 128)) && [[ "$tag" =~ ^v${number}\.${number}\.${number}(-${identifier}(\.${identifier})*)?$ ]]; then
  if [[ "$tag" = *-* ]]; then
    echo prerelease
  else
    echo stable
  fi
else
  echo "unsupported release tag: $tag (expected vX.Y.Z or vX.Y.Z-prerelease)" >&2
  exit 1
fi
