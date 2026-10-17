#!/bin/sh
set -eu

# One-line control bootstrap. OAuth credentials are read from protected local
# files, encrypted into the control database, and never written to the service.
REPOSITORY="tailscale-x/tailscale-private-funnel"
VERSION=""
INSTALL_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
OAUTH_ID_FILE="${TAILSCALE_PRIVATE_FUNNEL_OAUTH_CLIENT_ID_FILE:-}"
OAUTH_SECRET_FILE="${TAILSCALE_PRIVATE_FUNNEL_OAUTH_CLIENT_SECRET_FILE:-}"
TAILNET="${TAILSCALE_PRIVATE_FUNNEL_TAILNET:-}"
STATE_DIR=""
DATA_DIR=""
RESOURCE_NAME="main"
CONTROL_HOSTNAME=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --name) [ "$#" -ge 2 ] || exit 2; RESOURCE_NAME=$2; shift 2 ;;
        --hostname) [ "$#" -ge 2 ] || exit 2; CONTROL_HOSTNAME=$2; shift 2 ;;
        --version) [ "$#" -ge 2 ] || { echo "--version requires a value" >&2; exit 2; }; VERSION=$2; shift 2 ;;
        --install-dir) [ "$#" -ge 2 ] || { echo "--install-dir requires a value" >&2; exit 2; }; INSTALL_DIR=$2; shift 2 ;;
        --oauth-client-id-file) [ "$#" -ge 2 ] || { echo "--oauth-client-id-file requires a value" >&2; exit 2; }; OAUTH_ID_FILE=$2; shift 2 ;;
        --oauth-client-secret-file) [ "$#" -ge 2 ] || { echo "--oauth-client-secret-file requires a value" >&2; exit 2; }; OAUTH_SECRET_FILE=$2; shift 2 ;;
        --tailnet) [ "$#" -ge 2 ] || { echo "--tailnet requires a value" >&2; exit 2; }; TAILNET=$2; shift 2 ;;
        --state-dir) [ "$#" -ge 2 ] || { echo "--state-dir requires a value" >&2; exit 2; }; STATE_DIR=$2; shift 2 ;;
        --data-dir) [ "$#" -ge 2 ] || { echo "--data-dir requires a value" >&2; exit 2; }; DATA_DIR=$2; shift 2 ;;
        -h|--help)
            echo "Usage: curl -fsSL https://raw.githubusercontent.com/$REPOSITORY/main/scripts/control.sh | sh -s -- --tailnet example.ts.net --oauth-client-id-file ./ts-client-id --oauth-client-secret-file ./ts-client-secret --version VERSION [--name main] [--hostname funnel-control]"
            exit 0
            ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done

[ -n "$TAILNET" ] || { echo "--tailnet or TAILSCALE_PRIVATE_FUNNEL_TAILNET is required" >&2; exit 2; }
[ -n "$OAUTH_ID_FILE" ] || { echo "--oauth-client-id-file is required" >&2; exit 2; }
[ -n "$OAUTH_SECRET_FILE" ] || { echo "--oauth-client-secret-file is required" >&2; exit 2; }
[ -f "$OAUTH_ID_FILE" ] || { echo "OAuth client ID file does not exist: $OAUTH_ID_FILE" >&2; exit 2; }
[ -f "$OAUTH_SECRET_FILE" ] || { echo "OAuth client secret file does not exist: $OAUTH_SECRET_FILE" >&2; exit 2; }

[ -n "$VERSION" ] || { echo "--version is required; use a matching pinned artifact" >&2; exit 2; }
# Prefer the checked-out installer so an unreleased development interface never
# downloads an older installer's behavior from main.
installer=""
case "$0" in
    */control.sh) script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd); installer="$script_dir/install.sh" ;;
esac
if [ -n "$installer" ] && [ -f "$installer" ]; then
    sh "$installer" --version "$VERSION" --install-dir "$INSTALL_DIR"
else
    case "$VERSION" in
        v*) curl -fsSL "https://raw.githubusercontent.com/$REPOSITORY/$VERSION/scripts/install.sh" | sh -s -- --version "$VERSION" --install-dir "$INSTALL_DIR" ;;
        *) echo "Run the development control installer from its matching checkout" >&2; exit 2 ;;
    esac
fi

binary="$INSTALL_DIR/tailscale-private-funnel"
set -- native controls create "$RESOURCE_NAME" --non-interactive --tailnet "$TAILNET" --oauth-client-id-file "$OAUTH_ID_FILE" --oauth-client-secret-file "$OAUTH_SECRET_FILE"
[ -n "$CONTROL_HOSTNAME" ] && set -- "$@" --hostname "$CONTROL_HOSTNAME"
[ -n "$STATE_DIR" ] && set -- "$@" --state-dir "$STATE_DIR"
[ -n "$DATA_DIR" ] && set -- "$@" --data-dir "$DATA_DIR"
[ -n "$VERSION" ] && set -- "$@" --version "$VERSION"
exec "$binary" "$@" --wait
