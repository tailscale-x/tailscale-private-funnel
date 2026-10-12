# Install the native executable from a public OCI image without Docker.
import hashlib, io, json, os, platform, re, sys, tarfile, tempfile, urllib.request
image = sys.argv[1]
match = re.fullmatch(r'ghcr.io/(tailscale-x/tailscale-private-funnel):(dev-[A-Za-z0-9_.-]+)', image)
if not match:
    raise SystemExit('Invalid development artifact reference')
repository, reference = match.groups()
arch = {'x86_64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine())
if platform.system() != 'Linux' or not arch:
    raise SystemExit('Generated service deployment requires Linux amd64 or arm64')
base = 'https://ghcr.io/v2/' + repository
with urllib.request.urlopen('https://ghcr.io/token?service=ghcr.io&scope=repository:' + repository + ':pull', timeout=30) as response:
    token = json.load(response)['token']
def fetch(url, accept='application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json,application/vnd.docker.distribution.manifest.list.v2+json'):
    request = urllib.request.Request(url, headers={'Authorization': 'Bearer ' + token, 'Accept': accept})
    with urllib.request.urlopen(request, timeout=120) as response:
        return response.read()
def verified(data, digest):
    if digest != 'sha256:' + hashlib.sha256(data).hexdigest():
        raise SystemExit('Artifact digest verification failed')
    return data
manifest_raw = fetch(base + '/manifests/' + reference)
manifest = json.loads(manifest_raw)
if 'manifests' in manifest:
    candidates = [m for m in manifest['manifests'] if m.get('platform', {}).get('os') == 'linux' and m.get('platform', {}).get('architecture') == arch]
    if len(candidates) != 1:
        raise SystemExit('Development artifact is unavailable for this architecture')
    digest = candidates[0]['digest']
    manifest = json.loads(verified(fetch(base + '/manifests/' + digest), digest))
config = json.loads(verified(fetch(base + '/blobs/' + manifest['config']['digest']), manifest['config']['digest']))
if config.get('os') != 'linux' or config.get('architecture') != arch:
    raise SystemExit('Artifact architecture mismatch')
program = None
for layer in reversed(manifest['layers']):
    data = verified(fetch(base + '/blobs/' + layer['digest']), layer['digest'])
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:*') as archive:
        for member in archive:
            if member.name.removeprefix('./').lstrip('/') == 'usr/local/bin/tailscale-private-funnel' and member.isfile():
                program = archive.extractfile(member).read()
                break
    if program is not None:
        break
if not program or not program.startswith(b'\x7fELF'):
    raise SystemExit('Verified image has no native Linux executable')
directory = os.environ.get('XDG_BIN_HOME', os.path.expanduser('~/.local/bin'))
os.makedirs(directory, exist_ok=True)
fd, temporary = tempfile.mkstemp(prefix='.private-funnel-', dir=directory)
try:
    with os.fdopen(fd, 'wb') as output:
        output.write(program)
    os.chmod(temporary, 0o755)
    target = os.path.join(directory, 'tailscale-private-funnel')
    os.replace(temporary, target)
    print('Installed verified development binary:', target)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
