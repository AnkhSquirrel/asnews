#!/usr/bin/env sh
# Verify rules/cases.tsv against Miniflux's real filter code at a given tag.
# Usage: tools/minifluxcheck/run.sh [tag]   (default: MINIFLUX_VERSION from deploy/.env.example)
# Needs: git, Go >= 1.24 (filter.go uses strings.SplitSeq).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
tag=${1:-$(sed -n 's/^MINIFLUX_VERSION=//p' "$root/deploy/.env.example")}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$tag" --filter=blob:none --sparse \
  https://github.com/miniflux/v2 "$tmp/miniflux"
git -C "$tmp/miniflux" sparse-checkout set internal/reader/filter

mkdir -p "$here/filter"
sed 's#"miniflux.app/v2/internal/model"#"minifluxcheck/model"#' \
  "$tmp/miniflux/internal/reader/filter/filter.go" > "$here/filter/filter.go"

echo "miniflux $tag: $(git -C "$tmp/miniflux" rev-parse --short HEAD)"
cd "$here" && go run . "$root"
