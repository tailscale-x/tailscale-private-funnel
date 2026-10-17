#!/bin/sh
set -eu

REPOSITORY="tailscale-x/tailscale-private-funnel"
VERSION=""
INSTALL_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"

usage() {
  cat <<'EOF'
Usage: install.sh [--version VERSION] [--install-dir DIR]

Downloads the matching checksummed release artifact for this host and installs it
as tailscale-private-funnel. The release must contain SHA256SUMS. From a matching checkout, dev-* versions
use the same digest-verified OCI extractor as generated remote installers.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || { echo "--version requires a value" >&2; exit 2; }
      VERSION=$2
      shift 2
      ;;
    --install-dir)
      [ "$#" -ge 2 ] || { echo "--install-dir requires a value" >&2; exit 2; }
      INSTALL_DIR=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$VERSION" in
  dev-*)
    command -v python3 >/dev/null || { echo 'Development installation requires Python 3' >&2; exit 1; }
    script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
    extractor="$script_dir/../internal/provision/assets/install-oci.py"
    [ -f "$extractor" ] || { echo 'Install development artifacts from their matching checkout' >&2; exit 1; }
    XDG_BIN_HOME="$INSTALL_DIR" exec python3 "$extractor" "ghcr.io/tailscale-x/tailscale-private-funnel:$VERSION"
    ;;
esac

if [ -z "$VERSION" ]; then
  latest_url=$(curl -fsSIL -o /dev/null -w '%{url_effective}' "https://github.com/$REPOSITORY/releases/latest")
  VERSION=${latest_url##*/}
fi

case "$(uname -s)" in
  Linux) target_os=linux ;;
  Darwin) target_os=darwin ;;
  *) echo "unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) target_arch=amd64 ;;
  aarch64|arm64) target_arch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="tailscale-private-funnel-${target_os}-${target_arch}"
base_url="https://github.com/$REPOSITORY/releases/download/$VERSION"
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/tailscale-private-funnel.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

curl -fL --proto '=https' --tlsv1.2 -o "$tmp_dir/$asset" "$base_url/$asset"
curl -fL --proto '=https' --tlsv1.2 -o "$tmp_dir/SHA256SUMS" "$base_url/SHA256SUMS"

expected=$(awk -v name="dist/$asset" '$2 == name { print $1; exit }' "$tmp_dir/SHA256SUMS")
if [ -z "$expected" ]; then
  expected=$(awk -v name="$asset" '$2 == name { print $1; exit }' "$tmp_dir/SHA256SUMS")
fi
[ -n "$expected" ] || { echo "no checksum found for $asset" >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
  printf '%s  %s\n' "$expected" "$tmp_dir/$asset" | sha256sum --check -
else
  command -v shasum >/dev/null 2>&1 || { echo "sha256sum or shasum is required" >&2; exit 1; }
  printf '%s  %s\n' "$expected" "$tmp_dir/$asset" | shasum -a 256 -c -
fi

mkdir -p "$INSTALL_DIR"
target="$INSTALL_DIR/tailscale-private-funnel"
restore_bind_cap=0
if [ -x "$target" ] && command -v getcap >/dev/null 2>&1 && getcap "$target" 2>/dev/null | grep -q cap_net_bind_service; then
  restore_bind_cap=1
fi
staged="$INSTALL_DIR/.tailscale-private-funnel.$$"
cp "$tmp_dir/$asset" "$staged"
chmod 0755 "$staged"
mv "$staged" "$target"
if [ "$restore_bind_cap" -eq 1 ]; then
  command -v sudo >/dev/null 2>&1 || { echo "sudo is required to restore CAP_NET_BIND_SERVICE" >&2; exit 1; }
  sudo setcap cap_net_bind_service=+ep "$target"
fi

echo "installed $target ($VERSION, $target_os/$target_arch)"
case ":${PATH:-}:" in
  *:"$INSTALL_DIR":*) ;;
  *) echo "add $INSTALL_DIR to PATH before running tailscale-private-funnel" ;;
esac
