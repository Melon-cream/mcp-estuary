#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" = "" ]; then
  echo "usage: $0 <tag-or-version> [changelog-path]" >&2
  exit 1
fi

version="${1#v}"
changelog_path="${2:-CHANGELOG.md}"

if [ ! -f "${changelog_path}" ]; then
  echo "changelog not found: ${changelog_path}" >&2
  exit 1
fi

awk -v version="${version}" '
  $0 ~ "^## \\[" version "\\]" { in_section=1; next }
  in_section && $0 ~ "^## \\[" { exit }
  in_section { print }
' "${changelog_path}" | sed '/^[[:space:]]*$/N;/^\n$/D'
