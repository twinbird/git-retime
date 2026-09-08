#!/usr/bin/env bash
set -euo pipefail

tag=${1:?Usage: release-build.sh vX.Y.Z OUTPUT_DIR}
output=${2:?Usage: release-build.sh vX.Y.Z OUTPUT_DIR}
if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]]; then
  echo "Expected vX.Y.Z or vX.Y.Z-prerelease, got: $tag" >&2
  exit 1
fi

mkdir -p "$output"
output=$(cd "$output" && pwd)
if [[ -n "$(ls -A "$output")" ]]; then
  echo "Output directory must be empty: $output" >&2
  exit 1
fi
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for target_os in darwin linux; do
  for target_arch in amd64 arm64; do
    name="git-retime_${tag#v}_${target_os}_${target_arch}"
    mkdir -p "$stage/$name"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
      go build -trimpath -ldflags='-s -w' -o "$stage/$name/git-retime" ./cmd/git-retime
    cp README.md "$stage/$name/README.md"
    COPYFILE_DISABLE=1 tar -czf "$output/$name.tar.gz" -C "$stage" "$name"
  done
done

(cd "$output" && shasum -a 256 ./*.tar.gz > checksums.txt)
