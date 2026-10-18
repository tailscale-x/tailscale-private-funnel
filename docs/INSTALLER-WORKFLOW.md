# Generate once, execute on the target

Use matching prebuilt artifacts for the generator and target installer.

All public `create` commands **generate installers**. They never install on the
invoking machine, connect over SSH, or contact Kubernetes. Inspect, update,
delete, and native service-operation commands still execute their named action.

## Native gateway or load balancer

Run on an authorized machine that can reach the private control URL:

```sh
tailscale-private-funnel native gateways create site \
  --type docker --control-url http://CONTROL:8080 --version VERSION \
  --output script | ssh ubuntu@TARGET 'sh -s'
```

The generated installer downloads a pinned binary, checks prerequisites, and
runs a hidden installer. The target needs Linux/systemd and appropriate system
permissions, internet access to Tailscale, and Docker for a Docker gateway.
It does **not** need a host Tailscale installation. Its embedded tsnet joins
first, reaches private control, and binds its enrollment and signing key.

## Kubernetes gateway or load balancer

Generate on an authorized machine; the context need not exist there:

```sh
umask 077
tailscale-private-funnel k8s gateways create site \
  --context TARGET_CONTEXT --namespace tailscale-private-funnel-main-system \
  --control-url http://CONTROL:8080 --version VERSION --wait \
  --output script > install-site.sh
```

Use `umask 077` before saving secret output. Transfer the installer securely to a
machine with the selected kubeconfig and execute `sh install-site.sh`. That
machine does not need Tailscale access to control. The operator must already
manage the selected namespace. Workloads bootstrap using a temporary Kubernetes
Secret, join embedded tsnet, and then contact control. Successful initialization
removes the bootstrap Secret. `--wait` and `--timeout` apply during execution.

## Control and operator

Their `create` commands also generate installers, without contacting an existing
control or Kubernetes. OAuth-file paths reference **execution-machine** files;
their contents never enter generated output. Operator execution requires Helm,
kubectl and explicit cluster context. Components never implicitly install an
operator.

## Credentials and lifecycle

`--output instructions` is the default; `script` emits only executable POSIX shell;
`json` returns typed settings, credentials where applicable, and the installer.
Treat all enrolled-component output as secret. `--dry-run` makes no network calls,
creates no credentials, and cannot produce an executable installer.

Enrollment credentials and Tailscale auth keys are one-use and expire after
15 minutes. Regenerate expired or consumed installers. Auth keys are
preauthorized, non-ephemeral and scoped to `tag:tailscale-private-funnel`.
OAuth credentials stay on control. Credentials pass through stdin and temporary
Kubernetes bootstrap Secrets, never command arguments, units, receipts or CR specs.
Persisted component identity survives ordinary service/Pod restarts. Stop preserves
identity/data; delete revokes access and removes owned state.

The web UI generates commands without minting credentials. Run the generated
command from an authorized machine to issue the short-lived installer, then
execute that output on the target. Generating output does not prove readiness.
