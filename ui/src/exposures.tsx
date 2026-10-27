import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { useAdmin } from "./permissions";
import {
  Alert,
  Badge,
  ErrorMessage,
  Field,
  Intro,
  Modal,
  Panel,
  Submit,
  Table,
  date,
  formValues,
} from "./components";
import type { Application, Exposure } from "./gen/funnel/v1/management_pb";
export function ExposuresPage({
  applications = false,
}: {
  applications?: boolean;
}) {
  const admin = useAdmin();
  const exposures = useQuery({
    queryKey: ["exposures"],
    queryFn: () => api.listExposures({}),
    refetchInterval: 15_000,
  });
  const apps = useQuery({
    queryKey: ["applications"],
    queryFn: () => api.listApplications({}),
  });
  const serviceHosts = useQuery({
    queryKey: ["service-hosts"],
    queryFn: () => api.listServiceHosts({}),
    enabled: applications,
    refetchInterval: 15000,
  });
  const [dnsHost, setDNSHost] = useState<Exposure>();
  const [proxied, setProxied] = useState(false);
  const [dnsSaved, setDNSSaved] = useState(false);
  const [alias, setAlias] = useState<Application>();
  const qc = useQueryClient();
  const saveDNS = useMutation({
    mutationFn: () => api.saveHostnameDNS({
      id: dnsHost!.id,
      cloudflareProxied: proxied,
      revision: dnsHost!.dns?.revision ?? 0n,
    }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["exposures"] });
      setDNSHost(undefined);
      setDNSSaved(true);
    },
  });
  const addAlias = useMutation({
    mutationFn: (form: FormData) =>
      api.addAlias({
        applicationId: alias!.id,
        hostname: String(form.get("hostname") || ""),
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["applications"] });
      await qc.invalidateQueries({ queryKey: ["exposures"] });
      setAlias(undefined);
    },
  });
  const appsByHost = new Map<string, Application[]>();
  for (const app of apps.data?.items ?? []) {
    for (const hostname of app.publicHostnames) {
      const members = appsByHost.get(hostname) ?? [];
      members.push(app);
      appsByHost.set(hostname, members);
    }
  }
  return (
    <>
      <Intro
        title={applications ? "Applications" : "Exposures"}
        subtitle="Public routing is published only for authorized, healthy backends. Control assigns eligible load balancers automatically."
        action={
          admin ? (
            <a className="pf-button" href="/tailnet-exposures">
              Expose a tailnet machine
            </a>
          ) : null
        }
      />
      <ErrorMessage
        error={exposures.error || apps.error || serviceHosts.error}
      />
      {dnsSaved ? <Alert>Hostname DNS settings saved. DNS reconciliation will apply the change.</Alert> : null}
      <Panel title={applications ? "Application Services" : "Public hostnames"}>
        <Table
          headers={[
            "Hostname / application",
            "Path / rewrite",
            "Backend",
            "Service",
            "Hostname readiness",
            "DNS mode",
            "Actions",
          ]}
          empty={!exposures.isPending && !exposures.data?.items.length}
        >
          {exposures.data?.items.flatMap((e) => {
            const members: (Application | undefined)[] = appsByHost.get(e.hostname) ?? [undefined];
            return members.map((app) => (
              <tr key={e.id + ":" + (app?.id ?? "pending")}>
                <td>
                  <strong>{e.hostname}</strong>
                  <small className="pf-help">
                    {app?.name || e.applicationId}
                  </small>
                </td>
                <td>
                  <code>{app?.backendSource === "kubernetes" ? "HTTPRoute / Ingress" : app?.pathPrefix || "/"}</code>
                  <small className="pf-help">
                    {app?.backendSource === "kubernetes" ? "Configured in Kubernetes" : app?.rewrite ? <>Replace prefix with <code>{app.rewrite.replacePrefix}</code></> : "Path preserved"}
                  </small>
                  {app && !app.enabled ? <Badge ok={false} label="Disabled" /> : null}
                </td>
                <td>
                  {app?.backendSource || "—"} · {app?.backendPort || "—"}
                </td>
                <td>
                  <code>
                    {app?.serviceFqdn ||
                      app?.serviceName ||
                      "Pending assignment"}
                  </code>
                  {applications ? (
                    <small className="pf-help">
                      {serviceHosts.data?.items.filter(
                        (host) =>
                          host.serviceName === app?.serviceName &&
                          host.approved,
                      ).length || 0}{" "}
                      approved Service hosts
                    </small>
                  ) : null}
                </td>
                <td>
                  {!e.edges.length ? (
                    <Badge ok={false} label="No eligible load balancer" />
                  ) : null}
                  {e.edges.map((edge) => (
                    <div key={edge.id}>
                      <Badge
                        ok={edge.ready}
                        label={edge.ready ? "Ready" : "Pending"}
                      />
                      {!edge.ready && edge.reason ? <small className="pf-help">{edge.reason}</small> : null}
                      <small className="pf-help">
                        {edge.name} · {edge.publicIpv4.join(", ")}
                      </small>
                    </div>
                  ))}
                </td>
                <td>
                  <span>{e.dns?.cloudflareProxied ? "Cloudflare proxy" : "DNS-only"}</span>
                  {admin && (e.cloudflareProxyAvailable || e.dns?.cloudflareProxied) ? (
                    <button className="pf-text-link block" onClick={() => {
                      saveDNS.reset(); setDNSHost(e); setProxied(e.dns?.cloudflareProxied ?? false);
                    }}>Configure DNS</button>
                  ) : null}
                </td>
                <td>
                  {admin && app?.backendSource === "tailnet" ? (
                    <a
                      className="pf-text-link"
                      href={
                        "/tailnet-exposures?application_id=" +
                        encodeURIComponent(app.id)
                      }
                    >
                      Manage exposure
                    </a>
                  ) : admin && app?.backendSource === "docker" ? (
                    <button
                      className="pf-secondary-button whitespace-nowrap"
                      onClick={() => setAlias(app)}
                    >
                      Add alias
                    </button>
                  ) : (
                    <span className="pf-help">
                      Managed by gateway discovery
                    </span>
                  )}
                </td>
              </tr>
            ));
          })}
        </Table>
      </Panel>
      {dnsHost ? (
        <Modal title="Hostname DNS" onClose={() => setDNSHost(undefined)}>
          <form onSubmit={(event) => { event.preventDefault(); saveDNS.mutate(); }}>
            <p className="pf-help break-all">{dnsHost.hostname}</p>
            <Field label="Traffic mode">
              <select className="pf-input" value={proxied ? "cloudflare" : "dns-only"}
                onChange={(event) => setProxied(event.target.value === "cloudflare")}>
                <option value="dns-only">DNS-only</option>
                <option value="cloudflare" disabled={!dnsHost.cloudflareProxyAvailable}>Cloudflare proxy (orange cloud)</option>
              </select>
            </Field>
            <p className="pf-help">This setting applies to every route and gateway using {dnsHost.hostname}.</p>
            {proxied ? <Alert>Use Cloudflare Full (strict) TLS and a valid origin certificate. Cloudflare upload limits, caching and request timeouts apply to this hostname.</Alert> : null}
            <Submit pending={saveDNS.isPending} label="Save DNS settings" />
            <ErrorMessage error={saveDNS.error} />
          </form>
        </Modal>
      ) : null}
      {alias ? (
        <Modal
          title={`Add alias to ${alias.name} (${alias.pathPrefix || "/"})`}
          onClose={() => setAlias(undefined)}
        >
          <form onSubmit={(e) => addAlias.mutate(formValues(e))}>
            <Field label="Public hostname">
              <input className="pf-input" name="hostname" required />
            </Field>
            <p className="pf-help">
              The alias shares this application's Service and backend freshness.
              For a path route, the same prefix and rewrite apply to the alias. Exposure policy and managed-zone validation still apply.
            </p>
            <Submit pending={addAlias.isPending} label="Add alias" />
            <ErrorMessage error={addAlias.error} />
          </form>
        </Modal>
      ) : null}
    </>
  );
}
export function TailnetPage() {
  const admin = useAdmin();
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.listNodes({}),
  });
  const apps = useQuery({
    queryKey: ["applications"],
    queryFn: () => api.listApplications({}),
  });
  const qc = useQueryClient();
  const initialGateway =
    new URLSearchParams(location.search).get("gateway_id") || "";
  const initialApp =
    new URLSearchParams(location.search).get("application_id") || "";
  const [gateway, setGateway] = useState(initialGateway);
  const [device, setDevice] = useState("");
  const [port, setPort] = useState(8080);
  const [hostname, setHostname] = useState("");
  const [name, setName] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [editing, setEditing] = useState<Application>();
  const [probeId, setProbeId] = useState("");
  const [initialized, setInitialized] = useState(false);
  const peers = useQuery({
    queryKey: ["peers", gateway],
    queryFn: () => api.getPeers({ id: gateway }),
    enabled: !!gateway,
    refetchInterval: probeId ? 3000 : 15_000,
    retry: false,
  });
  const probe = useMutation({
    mutationFn: () =>
      api.probeBackend({ nodeId: gateway, deviceId: device, port }),
    onSuccess: (r) => setProbeId(r.id),
  });
  const policy = useMutation({
    mutationFn: () => api.testPolicy({ hostname, gatewayIds: [gateway] }),
  });
  const save = useMutation({
    mutationFn: () =>
      api.saveTailnetExposure({
        id: editing?.id || "",
        name,
        nodeId: gateway,
        backendDeviceId: device,
        backendPort: port,
        publicHostname: hostname,
        enabled,
        revision: editing?.revision || 0n,
        probeId,
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["applications"] });
      await qc.invalidateQueries({ queryKey: ["exposures"] });
    },
  });
  const remove = useMutation({
    mutationFn: (app: Application) =>
      api.deleteTailnetExposure({ id: app.id, revision: app.revision }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["applications"] }),
  });
  const edit = (a: Application) => {
    setEditing(a);
    setGateway(a.gatewayIds[0] || "");
    setDevice(a.backendDeviceId);
    setPort(a.backendPort);
    setHostname(a.publicHostnames[0] || "");
    setName(a.name);
    setEnabled(a.enabled);
    setProbeId("");
  };
  if (!initialized && apps.data) {
    setInitialized(true);
    const a = apps.data.items.find((a) => a.id === initialApp);
    if (a) edit(a);
  }
  const result = peers.data?.results.find((r) => r.id === probeId);
  if (!admin)
    return (
      <Alert>
        Administrator access is required to manage tailnet exposures.
      </Alert>
    );
  return (
    <>
      <Intro
        title="Expose a tailnet machine"
        subtitle="Public HTTPS → Load balancer → Tailscale Service → Selected gateway → Tailnet backend."
      />
      <Alert>
        Additional access tags and grants are managed in Tailscale. Peer
        visibility is not proof of connectivity; a successful backend test is
        required.
      </Alert>
      <Panel title={editing ? "Edit tailnet exposure" : "New tailnet exposure"}>
        <form
          className="pf-form-grid"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="Gateway">
            <select
              className="pf-input"
              value={gateway}
              required
              onChange={(e) => {
                setGateway(e.target.value);
                setDevice("");
                setProbeId("");
              }}
            >
              <option value="">Select tailnet gateway</option>
              {nodes.data?.items
                .filter((n) => n.mode === "tailnet-gateway" && !n.disabled)
                .map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.name} · {n.hostname}
                  </option>
                ))}
            </select>
          </Field>
          <Field label="Backend machine">
            <select
              className="pf-input"
              value={device}
              required
              disabled={!peers.data}
              onChange={(e) => {
                setDevice(e.target.value);
                setProbeId("");
              }}
            >
              <option value="">Select reported peer</option>
              {peers.data?.peers.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.hostname || p.dnsName} · {p.addresses.join(", ")}
                </option>
              ))}
            </select>
          </Field>
          {peers.data ? (
            <p className="pf-help pf-field-wide">
              Effective tags: {peers.data.tags.join(", ")} · Peer report:{" "}
              {date(peers.data.receivedAt)}. Visibility does not prove access.
            </p>
          ) : null}
          <Field label="HTTP port">
            <input
              className="pf-input"
              type="number"
              min={1}
              max={65535}
              value={port}
              required
              onChange={(e) => {
                setPort(Number(e.target.value));
                setProbeId("");
              }}
            />
          </Field>
          <Field label="Public hostname">
            <input
              className="pf-input"
              value={hostname}
              required
              onChange={(e) => setHostname(e.target.value)}
            />
          </Field>
          <Field label="Name">
            <input
              className="pf-input"
              value={name}
              maxLength={128}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <label className="pf-check-field">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            <span>Enable exposure</span>
          </label>
          <div className="pf-form-actions">
            <button
              type="button"
              className="pf-secondary-button"
              disabled={!hostname || policy.isPending}
              onClick={() => policy.mutate()}
            >
              Check policy
            </button>
            <button
              type="button"
              className="pf-secondary-button"
              disabled={!gateway || !device || probe.isPending}
              onClick={() => probe.mutate()}
            >
              Test backend
            </button>
            <Submit pending={save.isPending} />
            {editing ? (
              <button
                type="button"
                className="pf-secondary-button"
                onClick={() => {
                  setEditing(undefined);
                  setHostname("");
                  setName("");
                  setProbeId("");
                }}
              >
                Cancel edit
              </button>
            ) : null}
          </div>
          {policy.data ? (
            <Alert>
              {policy.data.allowed
                ? `Allowed by ${policy.data.matchedRule}`
                : "Policy denied"}
            </Alert>
          ) : null}
          {probeId ? (
            <Alert>
              {result
                ? result.reachable
                  ? "Backend reachable"
                  : `Backend unreachable: ${result.error}`
                : "Waiting for the gateway to execute the connection test…"}
            </Alert>
          ) : null}
          {save.data ? (
            <Alert>
              Exposure saved. Routing and DNS readiness are available in
              Exposures.
            </Alert>
          ) : null}
          <ErrorMessage
            error={peers.error || probe.error || save.error || policy.error}
          />
        </form>
      </Panel>
      <Panel title="Tailnet exposures">
        <Table
          headers={["Name / hostname", "Port", "State", "Actions"]}
          empty={
            !apps.isPending &&
            !apps.data?.items.some((a) => a.backendSource === "tailnet")
          }
        >
          {apps.data?.items
            .filter((a) => a.backendSource === "tailnet")
            .map((a) => (
              <tr key={a.id}>
                <td>
                  <strong>{a.name}</strong>
                  <small>{a.publicHostnames.join(", ")}</small>
                </td>
                <td>{a.backendPort}</td>
                <td>
                  <Badge
                    ok={a.enabled}
                    label={a.enabled ? "Enabled" : "Disabled"}
                  />
                </td>
                <td>
                  <button
                    className="pf-secondary-button"
                    onClick={() => edit(a)}
                  >
                    Edit
                  </button>
                  <button
                    className="pf-secondary-button"
                    disabled={remove.isPending}
                    onClick={() => {
                      if (
                        confirm(
                          `Delete ${a.name}? Its owned routing and DNS will be withdrawn.`,
                        )
                      )
                        remove.mutate(a);
                    }}
                  >
                    Delete
                  </button>
                </td>
              </tr>
            ))}
        </Table>
        <ErrorMessage error={remove.error} />
        {remove.data ? <Alert>{remove.data.message}</Alert> : null}
      </Panel>
    </>
  );
}
