#!/bin/sh
# Build each release binary once; the Dockerfile packages these exact artifacts.
set -eu
version=${1:?Usage: build-artifacts.sh VERSION [os-arch ...]}
shift
case "$version" in *[!A-Za-z0-9._-]*) echo 'Invalid build version' >&2; exit 1 ;; esac
if [ "$#" -eq 0 ]; then set -- linux-amd64; fi
cd "$(dirname "$0")/.."
mkdir -p dist
for target do
  case "$target" in linux-amd64|linux-arm64|darwin-arm64) ;; *) echo "Unsupported target: $target" >&2; exit 1 ;; esac
  target_os=${target%-*}
  target_arch=${target#*-}
  GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 go build -tags caddy -trimpath -ldflags="-s -w -X main.version=$version" -o "dist/tailscale-private-funnel-$target" ./cmd/tailscale-private-funnel
  if [ "$target_os" = linux ] && command -v readelf >/dev/null; then
    if readelf -l "dist/tailscale-private-funnel-$target" | grep -q INTERP; then
      echo "Binary requires a dynamic interpreter: $target" >&2
      exit 1
    fi
  fi
done
