#!/bin/sh
# Verify packaged bytes for each platform without emulation or starting a node.
set -eu
image=${1:?Usage: verify-image.sh IMAGE [linux/amd64 linux/arm64]}
shift
if [ "$#" -eq 0 ]; then set -- linux/amd64; fi
cd "$(dirname "$0")/.."
scratch=$(mktemp -d)
container_id=
trap 'if [ -n "$container_id" ]; then docker rm "$container_id" >/dev/null; fi; rm -rf "$scratch"' EXIT HUP INT TERM
for platform do
  case "$platform" in linux/amd64|linux/arm64) ;; *) echo "Unsupported platform: $platform" >&2; exit 1 ;; esac
  artifact="dist/tailscale-private-funnel-$(printf '%s' "$platform" | tr / -)"
  test -f "$artifact"
  if [ "${FUNNEL_IMAGE_LOCAL_ONLY:-}" != 1 ]; then docker pull --platform "$platform" "$image" >&2; fi
  container_id=$(docker create --platform "$platform" "$image" version)
  docker cp "$container_id:/usr/local/bin/tailscale-private-funnel" "$scratch/binary"
  cmp "$artifact" "$scratch/binary"
  test -x "$scratch/binary"
  docker rm "$container_id" >/dev/null
  container_id=
  printf 'Verified %s binary bytes: %s\n' "$platform" "$image"
done
