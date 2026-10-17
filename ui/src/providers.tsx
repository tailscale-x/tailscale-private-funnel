import { Fragment, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import { useAdmin } from "./permissions";
import {
  Alert,
  ErrorMessage,
  Field,
  Intro,
  Modal,
  Panel,
  Submit,
  Table,
  Badge,
  formValues,
  date,
} from "./components";
import type { Provider } from "./gen/funnel/v1/management_pb";
function ProviderForm({
  provider,
  onDone,
}: {
  provider?: Provider;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const vendors = useQuery({
    queryKey: ["vendors"],
    queryFn: () => api.listVendors({}),
  });
  const [vendor, setVendor] = useState(provider?.vendor || "cloudflare");
  const mutation = useMutation({
    mutationFn: (form: FormData) =>
      api.saveProvider({
        id: provider?.id || "",
        name: String(form.get("name") || ""),
        vendor,
        accountId: String(form.get("account") || ""),
        credentials: Object.fromEntries(
          (vendors.data?.items.find((v) => v.id === vendor)?.fields || []).map(
            (field) => [field, String(form.get("credential:" + field) || "")],
          ),
        ),
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["providers"] });
      onDone();
    },
  });
  const fields = vendors.data?.items.find((v) => v.id === vendor)?.fields || [];
  return (
    <form
      className="pf-form-grid"
      onSubmit={(e) => mutation.mutate(formValues(e))}
    >
      <Field label="Name">
        <input
          className="pf-input"
          name="name"
          required
          maxLength={128}
          defaultValue={provider?.name}
        />
      </Field>
      <Field label="Vendor">
        <select
          className="pf-input"
          value={vendor}
          onChange={(e) => setVendor(e.target.value)}
          disabled={!!provider}
        >
          {vendors.data?.items
            .filter((v) => v.available)
            .map((v) => (
              <option key={v.id} value={v.id}>
                {v.id}
              </option>
            ))}
        </select>
      </Field>
      <Field label="Account ID">
        <input
          className="pf-input"
          name="account"
          defaultValue={provider?.accountId}
        />
      </Field>
      {fields
        .filter((f) => f !== "account_id")
        .map((field) => (
          <Field
            key={field}
            label={field.replaceAll("_", " ")}
            hint={
              provider ? "Leave blank to preserve the stored value." : undefined
            }
          >
            <input
              className="pf-input"
              name={"credential:" + field}
              type="password"
              autoComplete="new-password"
            />
          </Field>
        ))}
      <div className="pf-form-actions">
        <Submit
          pending={mutation.isPending}
          label={provider ? "Save changes" : "Add DNS provider"}
        />
        <button className="pf-secondary-button" type="button" onClick={onDone}>
          Cancel
        </button>
      </div>
      <ErrorMessage error={mutation.error} />
    </form>
  );
}
export function ProvidersPage() {
  const admin = useAdmin();
  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.listProviders({}),
  });
  const zones = useQuery({
    queryKey: ["zones"],
    queryFn: () => api.listZones({}),
  });
  const qc = useQueryClient();
  const [editing, setEditing] = useState("");
  const [adding, setAdding] = useState(false);
  const [message, setMessage] = useState("");
  const action = useMutation({
    mutationFn: async ({
      provider,
      action,
    }: {
      provider: Provider;
      action: string;
    }) => {
      if (action === "test") {
        const r = await api.testProvider({ id: provider.id });
        setMessage(r.message);
      } else if (action === "toggle")
        await api.enableProvider({
          id: provider.id,
          enabled: !provider.enabled,
        });
      else await api.deleteProvider({ id: provider.id });
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["providers"] }),
  });
  return (
    <>
      <Intro
        title="DNS providers"
        subtitle="Encrypted credentials for your DNS vendors. One provider can manage several existing domains."
        action={
          admin ? (
            <button className="pf-button" onClick={() => setAdding(true)}>
              Add DNS provider
            </button>
          ) : null
        }
      />
      {message ? <Alert>{message}</Alert> : null}
      <ErrorMessage error={providers.error || action.error} />
      <Panel title="Configured providers">
        <Table
          headers={["Name", "Vendor / account", "Zones", "Status", "Actions"]}
          empty={!providers.isPending && !providers.data?.items.length}
        >
          {providers.data?.items.map((p) => (
            <Fragment key={p.id}>
              <tr>
                <td>
                  <strong>{p.name}</strong>
                  <small className="pf-help">{p.id}</small>
                </td>
                <td>
                  {p.vendor}
                  <small className="pf-help">
                    {p.accountId || "No account alias"}
                  </small>
                </td>
                <td>
                  {zones.data?.items.filter((z) => z.providerId === p.id)
                    .length ?? 0}
                </td>
                <td>
                  <Badge
                    ok={p.enabled}
                    label={p.enabled ? "Enabled" : "Disabled"}
                  />
                  <small className="pf-help">
                    {p.testedAt ? `Tested ${date(p.testedAt)}` : "Not tested"}
                  </small>
                </td>
                <td>
                  <div className="pf-react-actions">
                    <a
                      className="pf-text-link"
                      href={"/zones?provider_id=" + encodeURIComponent(p.id)}
                    >
                      Manage zones
                    </a>
                    {admin ? (
                      <>
                        <button
                          className="pf-secondary-button"
                          onClick={() =>
                            setEditing(editing === p.id ? "" : p.id)
                          }
                        >
                          Edit credentials
                        </button>
                        <button
                          className="pf-secondary-button"
                          disabled={action.isPending}
                          onClick={() =>
                            action.mutate({ provider: p, action: "test" })
                          }
                        >
                          Test
                        </button>
                        <button
                          className="pf-secondary-button"
                          disabled={action.isPending}
                          onClick={() =>
                            action.mutate({ provider: p, action: "toggle" })
                          }
                        >
                          {p.enabled ? "Disable" : "Enable"}
                        </button>
                        <button
                          className="pf-secondary-button"
                          disabled={action.isPending}
                          onClick={() => {
                            if (
                              confirm(
                                `Remove DNS provider ${p.name}? Managed zones must be removed first.`,
                              )
                            )
                              action.mutate({ provider: p, action: "delete" });
                          }}
                        >
                          Remove
                        </button>
                      </>
                    ) : null}
                  </div>
                </td>
              </tr>
              {editing === p.id ? (
                <tr>
                  <td colSpan={5}>
                    <div className="pf-react-inline-editor">
                      <ProviderForm
                        provider={p}
                        onDone={() => setEditing("")}
                      />
                    </div>
                  </td>
                </tr>
              ) : null}
            </Fragment>
          ))}
        </Table>
      </Panel>
      {adding ? (
        <Modal title="Add DNS provider" onClose={() => setAdding(false)}>
          <ProviderForm onDone={() => setAdding(false)} />
        </Modal>
      ) : null}
    </>
  );
}
function AddZone({
  providerId,
  onDone,
}: {
  providerId: string;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.listProviders({}),
  });
  const [id, setId] = useState(providerId);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [errors, setErrors] = useState<string[]>([]);
  const discovered = useQuery({
    queryKey: ["discover", id],
    queryFn: () => api.discoverZones({ id }),
    enabled: !!id,
    retry: false,
  });
  const save = useMutation({
    mutationFn: async () => {
      const failures: string[] = [];
      const pending: string[] = [];
      for (const domain of selected) {
        try {
          await api.addZone({ providerId: id, domain });
        } catch (e) {
          pending.push(domain);
          failures.push(
            `${domain}: ${e instanceof Error ? e.message : String(e)}`,
          );
        }
      }
      setSelected(pending);
      setErrors(failures);
      await qc.invalidateQueries({ queryKey: ["zones"] });
      await qc.invalidateQueries({ queryKey: ["discover", id] });
      if (!failures.length) onDone();
    },
  });
  return (
    <>
      <p className="pf-help">
        Select domains already configured with your DNS vendor. This does not
        register domains or change nameservers. Cloudflare requires Zone Read
        and DNS Edit for each domain.
      </p>
      <Field label="DNS provider">
        <select
          className="pf-input"
          value={id}
          onChange={(e) => {
            setId(e.target.value);
            setSelected([]);
            setErrors([]);
          }}
        >
          <option value="">Select a DNS provider</option>
          {providers.data?.items
            .filter((p) => p.enabled)
            .map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} · {p.vendor} · {p.accountId || p.id.slice(0, 6)}
              </option>
            ))}
        </select>
      </Field>
      {id ? (
        <>
          <Field label="Search domains">
            <input
              className="pf-input"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </Field>
          {discovered.isPending ? (
            <p role="status">Loading accessible domains…</p>
          ) : null}
          <ErrorMessage error={discovered.error} />
          {discovered.error ? (
            <>
              <p className="pf-help">
                Check vendor credentials and zone-listing permissions, then
                retry.
              </p>
              <button
                className="pf-secondary-button"
                onClick={() => discovered.refetch()}
              >
                Retry
              </button>
            </>
          ) : null}
          {discovered.data && !discovered.data.items.length ? (
            <Alert>
              No domains found. Configure the domain with your DNS vendor first.
            </Alert>
          ) : null}
          <div className="pf-react-list">
            {discovered.data?.items
              .filter((z) => z.domain.includes(search.toLowerCase()))
              .map((z) => (
                <label key={z.domain} className="pf-check-field">
                  <input
                    type="checkbox"
                    checked={selected.includes(z.domain)}
                    disabled={z.managed || save.isPending}
                    onChange={(e) =>
                      setSelected((values) =>
                        e.target.checked
                          ? [...values, z.domain]
                          : values.filter((v) => v !== z.domain),
                      )
                    }
                  />
                  <span>
                    {z.domain}
                    {z.managed ? <small>Already managed</small> : null}
                  </span>
                </label>
              ))}
          </div>
        </>
      ) : null}
      {errors.map((e) => (
        <p key={e} role="alert" className="pf-alert pf-alert-error">
          {e}
        </p>
      ))}
      <div className="pf-react-actions">
        <button
          className="pf-button"
          disabled={!selected.length || save.isPending}
          onClick={() => save.mutate()}
        >
          {save.isPending ? "Adding…" : "Add selected zones"}
        </button>
        <button className="pf-secondary-button" onClick={onDone}>
          Cancel
        </button>
      </div>
    </>
  );
}
export function ZonesPage() {
  const admin = useAdmin();
  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.listProviders({}),
  });
  const zones = useQuery({
    queryKey: ["zones"],
    queryFn: () => api.listZones({}),
  });
  const qc = useQueryClient();
  const preselected =
    new URLSearchParams(location.search).get("provider_id") || "";
  const [adding, setAdding] = useState(false);
  const [message, setMessage] = useState("");
  const action = useMutation({
    mutationFn: async ({ id, remove }: { id: string; remove: boolean }) => {
      if (remove) await api.deleteZone({ id });
      else {
        const result = await api.testZone({ id });
        setMessage(result.message);
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["zones"] }),
  });
  return (
    <>
      <Intro
        title="Managed zones"
        subtitle="Assign existing vendor domains. DNS changes remain ownership-scoped and protected from unapproved overwrites."
        action={
          admin ? (
            <button className="pf-button" onClick={() => setAdding(true)}>
              Add zone
            </button>
          ) : null
        }
      />
      {message ? <Alert>{message}</Alert> : null}
      <ErrorMessage error={zones.error || action.error} />
      <Panel title="Domain assignments">
        <Table
          headers={["Domain", "DNS provider", "Actions"]}
          empty={!zones.isPending && !zones.data?.items.length}
        >
          {zones.data?.items
            .filter((z) => !preselected || z.providerId === preselected)
            .map((z) => (
              <tr key={z.id}>
                <td>
                  <strong>{z.domain}</strong>
                </td>
                <td>
                  {providers.data?.items.find((p) => p.id === z.providerId)
                    ?.name || z.providerId}
                </td>
                <td>
                  <div className="pf-react-actions">
                    <a
                      className="pf-text-link"
                      href={"/dns?zone_id=" + encodeURIComponent(z.id)}
                    >
                      DNS details
                    </a>
                    {admin ? (
                      <>
                        <button
                          className="pf-secondary-button"
                          disabled={action.isPending}
                          onClick={() =>
                            action.mutate({ id: z.id, remove: false })
                          }
                        >
                          Test read access
                        </button>
                        <button
                          className="pf-secondary-button"
                          disabled={action.isPending}
                          onClick={() => {
                            if (
                              confirm(
                                `Remove ${z.domain}? Owned DNS records must be cleaned up first.`,
                              )
                            )
                              action.mutate({ id: z.id, remove: true });
                          }}
                        >
                          Remove
                        </button>
                      </>
                    ) : null}
                  </div>
                </td>
              </tr>
            ))}
        </Table>
      </Panel>
      {preselected ? (
        <a className="pf-text-link" href="/zones">
          Show all zones
        </a>
      ) : null}
      {adding ? (
        <Modal title="Add zone" onClose={() => setAdding(false)}>
          <AddZone providerId={preselected} onDone={() => setAdding(false)} />
        </Modal>
      ) : null}
    </>
  );
}
