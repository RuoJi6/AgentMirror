#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm run build:web
mkdir -p release
if [ "$#" -eq 0 ]; then
  targets=(linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64)
else
  targets=("$@")
fi
files=()
for target in "${targets[@]}"; do
  case "$target" in
    linux/amd64|linux/arm64|darwin/arm64|darwin/amd64|windows/amd64) ;;
    *) echo "Unsupported target: $target" >&2; exit 1 ;;
  esac
  target_os="${target%/*}"
  target_arch="${target#*/}"
  name="agentmirror-${target_os}-${target_arch}"
  if [ "$target_os" = windows ]; then name="${name}.exe"; fi
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath -ldflags='-s -w' -o "release/$name" ./cmd/agentmirror
  files+=("$name")
  echo "Built release/$name"
done
cd release
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${files[@]}" > SHA256SUMS
else
  shasum -a 256 "${files[@]}" > SHA256SUMS
fi
