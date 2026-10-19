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
  Panel,
  Table,
  date,
  auditLabel,
} from "./components";
import type { DNSResult } from "./gen/funnel/v1/management_pb";
export function OperationsPage() {
  const admin = useAdmin();
  const reports = useQuery({
    queryKey: ["reports"],
    queryFn: () => api.listReports({}),
    refetchInterval: 15000,
  });
  const audit = useQuery({
    queryKey: ["audit"],
    queryFn: () => api.listAudit({ limit: 100 }),
  });
  const health = useQuery({
    queryKey: ["health"],
    queryFn: () => api.health({}),
  });
  return (
    <>
      <Intro
        title="Operations"
        subtitle="Audited control activity and deployment information. DNS reconciliation is available from each managed zone."
      />
      <Panel title="Deployment">
        <p>
          Version: <code>{health.data?.version || "Unknown"}</code>
        </p>
        <p>
          Update and rollback use pinned binaries, images and OCI charts. Stop
          the old process before transferring an identity; never share it
          between concurrent processes.
        </p>
        <p>
          Back up control data and encryption keys before schema changes. Retain
          certificates and Kubernetes identity Secrets during ordinary updates.
        </p>
        {admin ? (
          <p>
            <a className="pf-text-link" href="/controls">
              Install an independent control on Kubernetes
            </a>
          </p>
        ) : null}
        <a className="pf-text-link" href="/zones">
          Open managed zones
        </a>{" "}
        ·{" "}
        <a className="pf-text-link" href="/docs/cli.md">
          CLI reference
        </a>
      </Panel>
      <Panel title="Gateway reports">
        <p className="pf-help">
          Only fresh reports authenticated through verified Tailscale roles can authorize
          routing. A visible peer or Service is not proof of backend
          reachability.
        </p>
        <Table
          headers={["Node", "Freshness", "Exposures", "Last report"]}
          empty={!reports.isPending && !reports.data?.items.length}
        >
          {reports.data?.items.map((report) => (
            <tr key={report.nodeId}>
              <td>
                <strong>{report.name}</strong>
                <small className="pf-help">{report.mode}</small>
              </td>
              <td>
                <Badge
                  ok={report.fresh}
                  label={report.fresh ? "Fresh" : "Expired / revoked"}
                />
              </td>
              <td>
                <details>
                  <summary>
                    {report.exposures.length} reported exposures
                  </summary>
                  {report.exposures.map((exposure, index) => (
                    <div className="pf-react-event" key={index}>
                      <code>{exposure.publicHostname}</code>
                      <small>
                        {exposure.serviceName || exposure.backendHostname}:
                        {exposure.port} ·{" "}
                        {exposure.reachable ? "Reachable" : "Unavailable"}
                      </small>
                      {exposure.networks.length ? (
                        <small>Networks: {exposure.networks.join(", ")}</small>
                      ) : null}
                    </div>
                  ))}
                </details>
              </td>
              <td>{date(report.receivedAt)}</td>
            </tr>
          ))}
        </Table>
        <ErrorMessage error={reports.error} />
      </Panel>
      <Panel title="Audit events">
        <Table
          headers={["Event", "Actor", "Time"]}
          empty={!audit.isPending && !audit.data?.items.length}
        >
          {audit.data?.items.map((e, i) => (
            <tr key={i}>
              <td>{auditLabel(e.action)}</td>
              <td>{e.actor}</td>
              <td>{date(e.createdAt)}</td>
            </tr>
          ))}
        </Table>
        <ErrorMessage error={audit.error} />
      </Panel>
    </>
  );
}
export function DNSPage() {
  const admin = useAdmin();
  const qc = useQueryClient();
  const zones = useQuery({
    queryKey: ["zones"],
    queryFn: () => api.listZones({}),
  });
  const identity = useQuery({
    queryKey: ["identity"],
    queryFn: () => api.whoAmI({}),
  });
  const [selected, setSelected] = useState(
    new URLSearchParams(location.search).get("zone_id") || "",
  );
  const zoneId = selected || zones.data?.items[0]?.id || "";
  const state = useQuery({
    queryKey: ["dns", zoneId],
    queryFn: () => api.zoneDNS({ id: zoneId }),
    enabled: !!zoneId,
  });
  const [preview, setPreview] = useState<DNSResult>();
  const [approved, setApproved] = useState<string[]>([]);
  const review = useMutation({
    mutationFn: () => api.previewDNS({ zoneId }),
    onSuccess: (r) => {
      setPreview(r);
      setApproved([]);
    },
  });
  const apply = useMutation({
    mutationFn: () =>
      api.applyDNS({
        zoneId,
        approvals: Object.fromEntries(
          (preview?.results || [])
            .filter((r) => approved.includes(r.resourceId))
            .map((r) => [r.resourceId, r.approvalToken]),
        ),
      }),
    onSuccess: async (r) => {
      setPreview(r);
      await qc.invalidateQueries({ queryKey: ["dns", zoneId] });
    },
  });
  return (
    <>
      <Intro
        title="Zone DNS"
        subtitle="Review owned records and reconcile approved desired state. Unowned overwrites require approval for the exact preview."
      />
      <Field label="Managed zone">
        <select
          className="pf-input"
          value={zoneId}
          onChange={(e) => {
            setSelected(e.target.value);
            setPreview(undefined);
            setApproved([]);
          }}
        >
          {zones.data?.items.map((z) => (
            <option key={z.id} value={z.id}>
              {z.domain}
            </option>
          ))}
        </select>
      </Field>
      <ErrorMessage error={state.error || review.error || apply.error} />
      {state.data ? (
        <Panel title="Managed record ledger">
          <p className="pf-help">
            This is the ownership ledger, not a claim that live vendor records
            match it.
          </p>
          <p>
            {state.data.status || "Not reconciled yet"} · Last success:{" "}
            {date(state.data.lastSuccess)}
          </p>
          <Table
            headers={["Name", "Type", "Value", "Management"]}
            empty={!state.data.records.length}
          >
            {state.data.records.map((r, i) => (
              <tr key={i}>
                <td>
                  <code>{r.name}</code>
                </td>
                <td>{r.type}</td>
                <td>
                  <code>{r.data}</code>
                </td>
                <td>
                  {r.management}
                  <small className="pf-help">{r.node}</small>
                </td>
              </tr>
            ))}
          </Table>
        </Panel>
      ) : null}
      {identity.data?.roles.includes("operator") ? (
        <Panel title="Reconciliation">
          <button
            className="pf-secondary-button"
            disabled={!zoneId || review.isPending}
            onClick={() => review.mutate()}
          >
            {review.isPending ? "Reading live DNS…" : "Preview changes"}
          </button>
          {preview ? (
            <>
              <Alert>
                {preview.pending
                  ? "Some partitions require approval or have pending errors. Independent valid partitions can reconcile."
                  : "Preview ready."}
              </Alert>
              {preview.results.map((r, i) => (
                <div className="pf-react-event" key={r.resourceId || i}>
                  <strong>{r.name}</strong>
                  <p>
                    {r.addCount} additions · {r.removeCount} removals
                  </p>
                  {r.error ? <p role="alert">{r.error}</p> : null}
                  {r.requiresApproval ? (
                    <>
                      <p>
                        Existing records:{" "}
                        {r.existingRecords
                          .map(
                            (v) =>
                              `${v.name} ${v.type} ${v.data} (${v.management || "Unmanaged"})`,
                          )
                          .join("; ")}
                      </p>
                      <p>
                        Desired records:{" "}
                        {r.desiredRecords
                          .map((v) => `${v.name} ${v.type} ${v.data}`)
                          .join("; ")}
                      </p>
                      {admin ? (
                        <label className="pf-check-field">
                          <input
                            type="checkbox"
                            checked={approved.includes(r.resourceId)}
                            onChange={(e) =>
                              setApproved((values) =>
                                e.target.checked
                                  ? [...values, r.resourceId]
                                  : values.filter((v) => v !== r.resourceId),
                              )
                            }
                          />
                          <span>Approve this specific overwrite</span>
                        </label>
                      ) : null}
                    </>
                  ) : null}
                </div>
              ))}
              {admin ? (
                <button
                  className="pf-button"
                  disabled={apply.isPending}
                  onClick={() => {
                    if (
                      confirm(
                        "Apply these DNS changes? Only explicitly selected overwrites are approved.",
                      )
                    )
                      apply.mutate();
                  }}
                >
                  {apply.isPending ? "Applying…" : "Apply changes"}
                </button>
              ) : null}
            </>
          ) : null}
          {apply.data ? (
            <Alert>
              {apply.data.pending
                ? "Reconciliation is pending for some records. Review errors and retry."
                : "Reconciliation completed."}
            </Alert>
          ) : null}
        </Panel>
      ) : null}
    </>
  );
}
