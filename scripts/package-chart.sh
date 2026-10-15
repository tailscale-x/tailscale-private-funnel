#!/bin/sh
# Package without modifying the checkout or publishing. CI publishes releases;
# development publication is an explicit operation.
set -eu
version=${1:?Usage: package-chart.sh VERSION OUTPUT_DIRECTORY}
output=${2:?Usage: package-chart.sh VERSION OUTPUT_DIRECTORY}
case "$version" in
  v[0-9]*) chart_version=${version#v} ;;
  dev*) chart_version=0.0.0-$version ;;
  *) echo 'Expected a v-prefixed release version or dev version' >&2; exit 1 ;;
esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$output"
output=$(CDPATH= cd -- "$output" && pwd)
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT HUP INT TERM
cp -R "$root/charts/tailscale-private-funnel" "$staging/chart"
python3 - "$staging/chart/values.yaml" "$version" <<'PY'
import pathlib, re, sys
path = pathlib.Path(sys.argv[1])
image = 'ghcr.io/tailscale-x/tailscale-private-funnel:' + sys.argv[2]
content, count = re.subn(r'^  reference: .+$', '  reference: ' + image, path.read_text(), count=1, flags=re.M)
if count != 1:
    raise SystemExit('image.reference default missing')
path.write_text(content)
PY
for component in control loadbalancer k8s-gateway; do
  helm lint "$staging/chart" --set component="$component" --set bootstrap.existingSecret=package-validation
done
helm package "$staging/chart" --version "$chart_version" --app-version "$version" --destination "$output"
