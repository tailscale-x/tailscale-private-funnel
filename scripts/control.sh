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
LOGIN_SERVER=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || { echo "--version requires a value" >&2; exit 2; }; VERSION=$2; shift 2 ;;
        --install-dir) [ "$#" -ge 2 ] || { echo "--install-dir requires a value" >&2; exit 2; }; INSTALL_DIR=$2; shift 2 ;;
        --oauth-client-id-file) [ "$#" -ge 2 ] || { echo "--oauth-client-id-file requires a value" >&2; exit 2; }; OAUTH_ID_FILE=$2; shift 2 ;;
        --oauth-client-secret-file) [ "$#" -ge 2 ] || { echo "--oauth-client-secret-file requires a value" >&2; exit 2; }; OAUTH_SECRET_FILE=$2; shift 2 ;;
        --tailnet) [ "$#" -ge 2 ] || { echo "--tailnet requires a value" >&2; exit 2; }; TAILNET=$2; shift 2 ;;
        --state-dir) [ "$#" -ge 2 ] || { echo "--state-dir requires a value" >&2; exit 2; }; STATE_DIR=$2; shift 2 ;;
        --data-dir) [ "$#" -ge 2 ] || { echo "--data-dir requires a value" >&2; exit 2; }; DATA_DIR=$2; shift 2 ;;
        --login-server) [ "$#" -ge 2 ] || { echo "--login-server requires a value" >&2; exit 2; }; LOGIN_SERVER=$2; shift 2 ;;
        -h|--help)
            echo "Usage: curl -fsSL https://raw.githubusercontent.com/$REPOSITORY/main/scripts/control.sh | sh -s -- --tailnet example.ts.net --oauth-client-id-file ./ts-client-id --oauth-client-secret-file ./ts-client-secret [--version vX.Y.Z]"
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

if [ -n "$VERSION" ]; then
    curl -fsSL "https://raw.githubusercontent.com/$REPOSITORY/main/scripts/install.sh" | sh -s -- --version "$VERSION" --install-dir "$INSTALL_DIR"
else
    curl -fsSL "https://raw.githubusercontent.com/$REPOSITORY/main/scripts/install.sh" | sh -s -- --install-dir "$INSTALL_DIR"
fi

binary="$INSTALL_DIR/tailscale-private-funnel"
set -- control join --non-interactive --tailnet "$TAILNET" --oauth-client-id-file "$OAUTH_ID_FILE" --oauth-client-secret-file "$OAUTH_SECRET_FILE"
[ -n "$STATE_DIR" ] && set -- "$@" --state-dir "$STATE_DIR"
[ -n "$DATA_DIR" ] && set -- "$@" --data-dir "$DATA_DIR"
[ -n "$LOGIN_SERVER" ] && set -- "$@" --login-server "$LOGIN_SERVER"
"$binary" "$@"

if [ -n "$STATE_DIR" ] && [ -n "$DATA_DIR" ]; then
    exec "$binary" control serve --state-dir "$STATE_DIR" --data-dir "$DATA_DIR"
elif [ -n "$STATE_DIR" ]; then
    exec "$binary" control serve --state-dir "$STATE_DIR"
elif [ -n "$DATA_DIR" ]; then
    exec "$binary" control serve --data-dir "$DATA_DIR"
else
    exec "$binary" control serve
fi
