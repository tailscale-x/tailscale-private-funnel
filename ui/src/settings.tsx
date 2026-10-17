import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { create, fromJsonString, toJson } from "@bufbuild/protobuf";
import { api } from "./api";
import { useAdmin } from "./permissions";
import {
  Alert,
  Badge,
  ErrorMessage,
  Field,
  Intro,
  JsonEditor,
  Modal,
  Panel,
  Submit,
  Table,
  formValues,
} from "./components";
import {
  AccessSettingsSchema,
  PolicySchema,
  RuleSchema,
  type Policy,
  type Rule,
} from "./gen/funnel/v1/management_pb";
function RuleForm({
  rule,
  onSave,
  onCancel,
}: {
  rule?: Rule;
  onSave: (rule: Rule) => void;
  onCancel: () => void;
}) {
  const [patterns, setPatterns] = useState(
    rule?.hostnameRegexes.length ? [...rule.hostnameRegexes] : [""],
  );
  const [selectedGateways, setSelectedGateways] = useState(
    rule?.gatewayIds || [],
  );
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.listNodes({}),
  });
  return (
    <form
      className="pf-form-grid"
      onSubmit={(e) => {
        const form = formValues(e);
        onSave(
          create(RuleSchema, {
            id: rule?.id || "",
            name: String(form.get("name") || ""),
            hostnameRegexes: patterns,
            roles: String(form.get("roles") || "")
              .split("\n")
              .map((v) => v.trim())
              .filter(Boolean),
            gatewayIds: form.getAll("gateways").map(String),
            enabled: form.get("enabled") === "on",
          }),
        );
      }}
    >
      <Field label="Name">
        <input
          className="pf-input"
          name="name"
          required
          maxLength={128}
          defaultValue={rule?.name}
        />
      </Field>
      <div className="pf-field pf-field-wide">
        <span className="pf-label">
          Hostname patterns · any pattern may match
        </span>
        {patterns.map((pattern, i) => (
          <div className="pf-react-pattern" key={i}>
            <input
              className="pf-input font-mono"
              aria-label={`Hostname regex ${i + 1}`}
              value={pattern}
              required
              onChange={(e) =>
                setPatterns((values) =>
                  values.map((v, j) => (i === j ? e.target.value : v)),
                )
              }
            />
            <button
              type="button"
              className="pf-secondary-button"
              disabled={patterns.length === 1}
              onClick={() =>
                setPatterns((values) => values.filter((_, j) => j !== i))
              }
            >
              Remove
            </button>
          </div>
        ))}
        <button
          type="button"
          className="pf-secondary-button"
          onClick={() => setPatterns((values) => [...values, ""])}
        >
          Add pattern
        </button>
        <p className="pf-help">
          Patterns match the complete hostname. Start with a narrow domain
          boundary, such as [^.]+[.]example[.]com.
        </p>
      </div>
      <Field
        label="Gateway identities (optional)"
        hint="Empty means no gateway identity restriction."
      >
        <select
          className="pf-input"
          name="gateways"
          multiple
          value={selectedGateways}
          onChange={(event) =>
            setSelectedGateways(
              [...event.target.selectedOptions].map((option) => option.value),
            )
          }
        >
          {nodes.data?.items
            .filter((n) => n.mode !== "loadbalancer" && !n.revoked)
            .map((n) => (
              <option key={n.id} value={n.id}>
                {n.name} · {n.hostname}
              </option>
            ))}
        </select>
      </Field>
      <Field
        label="Gateway roles (optional)"
        hint="One verified role/tag per line; empty means no role restriction."
      >
        <textarea
          className="pf-input"
          name="roles"
          defaultValue={rule?.roles.join("\n") || ""}
          rows={3}
        />
      </Field>
      <label className="pf-check-field">
        <input
          type="checkbox"
          name="enabled"
          defaultChecked={rule?.enabled ?? true}
        />
        <span>Enable rule</span>
      </label>
      <div className="pf-form-actions">
        <button className="pf-button" type="submit">
          Review changes
        </button>
        <button
          className="pf-secondary-button"
          type="button"
          onClick={onCancel}
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
export function PolicyPage() {
  const admin = useAdmin();
  const qc = useQueryClient();
  const policy = useQuery({
    queryKey: ["policy"],
    queryFn: () => api.getPolicy({}),
  });
  const [editor, setEditor] = useState<Rule | null | undefined>();
  const [pending, setPending] = useState<Policy>();
  const [affected, setAffected] = useState<string[]>([]);
  const [test, setTest] = useState("");
  const [testGateway, setTestGateway] = useState("");
  const testNodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.listNodes({}),
  });
  const save = useMutation({
    mutationFn: (p: Policy) => api.savePolicy(p),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["policy"] });
      setPending(undefined);
      setEditor(undefined);
    },
  });
  const preview = useMutation({
    mutationFn: async (p: Policy) => {
      const result = await api.previewPolicy(p);
      setAffected(result.affectedHostnames);
      setPending(p);
    },
  });
  const probe = useMutation({
    mutationFn: () =>
      api.testPolicy({
        hostname: test,
        gatewayIds: testGateway ? [testGateway] : [],
        roles:
          testNodes.data?.items.find((node) => node.id === testGateway)
            ?.effectiveTags || [],
      }),
  });
  const change = (rule: Rule) => {
    const p = create(PolicySchema, policy.data!);
    if (rule.id) {
      p.rules = p.rules.map((r) => (r.id === rule.id ? rule : r));
    } else p.rules = [...p.rules, rule];
    preview.mutate(p);
  };
  return (
    <>
      <Intro
        title="Exposure policy"
        subtitle="Default deny. An enabled rule must authorize each public hostname before a gateway can expose it."
        action={
          admin ? (
            <button className="pf-button" onClick={() => setEditor(null)}>
              Add rule
            </button>
          ) : null
        }
      />
      <ErrorMessage error={policy.error || preview.error || save.error} />
      <Panel title="Rules">
        <Table
          headers={[
            "Name",
            "Hostname patterns",
            "Gateway scope",
            "State",
            "Actions",
          ]}
          empty={!policy.isPending && !policy.data?.rules.length}
        >
          {policy.data?.rules.map((r) => (
            <tr key={r.id}>
              <td>
                <strong>{r.name}</strong>
              </td>
              <td>
                {r.hostnameRegexes.map((p) => (
                  <code key={p} className="pf-react-event">
                    {p}
                  </code>
                ))}
              </td>
              <td>
                {r.gatewayIds.length
                  ? `${r.gatewayIds.length} identities`
                  : "Any enrolled gateway"}
                <small className="pf-help">
                  {r.roles.join(", ") || "No role restriction"}
                </small>
              </td>
              <td>
                <Badge
                  ok={r.enabled}
                  label={r.enabled ? "Enabled" : "Disabled"}
                />
              </td>
              <td>
                {admin ? (
                  <div className="pf-react-actions">
                    <button
                      className="pf-secondary-button"
                      onClick={() => setEditor(r)}
                    >
                      Edit
                    </button>
                    <button
                      className="pf-secondary-button"
                      onClick={() =>
                        change(
                          create(RuleSchema, { ...r, enabled: !r.enabled }),
                        )
                      }
                    >
                      {r.enabled ? "Disable" : "Enable"}
                    </button>
                    <button
                      className="pf-secondary-button"
                      onClick={() => {
                        const p = create(PolicySchema, policy.data!);
                        p.rules = p.rules.filter((v) => v.id !== r.id);
                        preview.mutate(p);
                      }}
                    >
                      Delete
                    </button>
                  </div>
                ) : null}
              </td>
            </tr>
          ))}
        </Table>
      </Panel>
      <Panel title="Test a hostname">
        <form
          className="pf-react-actions"
          onSubmit={(e) => {
            e.preventDefault();
            probe.mutate();
          }}
        >
          <Field label="Public hostname">
            <input
              className="pf-input"
              value={test}
              onChange={(e) => setTest(e.target.value)}
              required
            />
          </Field>
          <Field
            label="Gateway context (optional)"
            hint="Select a gateway to include its identity and verified effective tags."
          >
            <select
              className="pf-input"
              value={testGateway}
              onChange={(event) => setTestGateway(event.target.value)}
            >
              <option value="">Hostname-only simulation</option>
              {testNodes.data?.items
                .filter(
                  (node) =>
                    node.mode !== "loadbalancer" &&
                    node.mode !== "k8s-forwarder" &&
                    !node.revoked,
                )
                .map((node) => (
                  <option key={node.id} value={node.id}>
                    {node.name} · {node.hostname}
                  </option>
                ))}
            </select>
          </Field>
          <Submit pending={probe.isPending} label="Test policy" />
        </form>
        {probe.data ? (
          <Alert>
            {probe.data.allowed
              ? `Allowed by ${probe.data.matchedRule}`
              : "Denied: no enabled rule matched."}
          </Alert>
        ) : null}
        <ErrorMessage error={probe.error} />
      </Panel>
      {policy.data ? (
        <Panel title="Policy document">
          <JsonEditor
            value={toJson(PolicySchema, policy.data)}
            onValidate={
              admin
                ? (value) =>
                    api.validatePolicy(
                      fromJsonString(PolicySchema, JSON.stringify(value)),
                    )
                : undefined
            }
            readOnly={!admin}
            onSave={
              admin
                ? async (value) => {
                    preview.mutate(
                      fromJsonString(PolicySchema, JSON.stringify(value)),
                    );
                  }
                : undefined
            }
          />
        </Panel>
      ) : null}
      {editor !== undefined ? (
        <Modal
          title={editor ? "Edit rule" : "Add rule"}
          onClose={() => setEditor(undefined)}
        >
          <RuleForm
            rule={editor ?? undefined}
            onCancel={() => setEditor(undefined)}
            onSave={change}
          />
        </Modal>
      ) : null}
      {pending ? (
        <Modal
          title="Confirm policy changes"
          onClose={() => setPending(undefined)}
        >
          <p>
            Saving updates authorization immediately. Gateway polling and DNS
            caching can delay visible withdrawal.
          </p>
          {affected.length ? (
            <>
              <p>These exposures will lose authorization:</p>
              <ul>
                {affected.map((host) => (
                  <li key={host}>
                    <code>{host}</code>
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p>No currently authorized exposures will be withdrawn.</p>
          )}
          <button
            className="pf-button"
            disabled={save.isPending}
            onClick={() => save.mutate(pending)}
          >
            {save.isPending ? "Saving…" : "Save policy"}
          </button>
          <ErrorMessage error={save.error} />
        </Modal>
      ) : null}
    </>
  );
}
export function SettingsPage() {
  const admin = useAdmin();
  const qc = useQueryClient();
  const settings = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.getSettings({}),
  });
  const oauth = useMutation({
    mutationFn: async ({ form, test }: { form: FormData; test: boolean }) =>
      (test ? api.testOAuth : api.saveOAuth)({
        clientId: String(form.get("clientId") || ""),
        clientSecret: String(form.get("clientSecret") || ""),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["settings"] }),
  });
  const defaults = useMutation({
    mutationFn: (form: FormData) =>
      api.saveDefaults({
        managementUrl: String(form.get("url") || ""),
        releaseVersion: String(form.get("version") || ""),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["settings"] }),
  });
  return (
    <>
      <Intro
        title="Tailscale settings"
        subtitle="Persistent control identity, encrypted OAuth credentials, and defaults for new deployments."
      />
      <ErrorMessage error={settings.error} />
      <Panel title="Control identity">
        <p>
          Tailnet: <code>{settings.data?.tailnet || "Not configured"}</code>
        </p>
        <p>
          Base tag: <code>tag:tailscale-private-funnel</code>
        </p>
        <p className="pf-help">
          Identity is established during control bootstrap. Saving these
          settings does not reconnect a running node or modify ACL policy.
        </p>
      </Panel>
      {admin ? (
        <>
          <Panel title="OAuth credentials">
            <p className="pf-help">
              Blank fields preserve stored credentials. Required scopes include
              auth_keys, devices:core, services, and users:read; resource scopes
              remain restricted to the shared tag where supported.
            </p>
            <form
              className="pf-form-grid"
              onSubmit={(e) => {
                const form = formValues(e);
                const submitter = (e.nativeEvent as SubmitEvent)
                  .submitter as HTMLButtonElement;
                oauth.mutate({ form, test: submitter?.value === "test" });
              }}
            >
              <Field label="Client ID">
                <input
                  className="pf-input"
                  name="clientId"
                  type="password"
                  autoComplete="new-password"
                  placeholder="Leave blank to preserve"
                />
              </Field>
              <Field label="Client secret">
                <input
                  className="pf-input"
                  name="clientSecret"
                  type="password"
                  autoComplete="new-password"
                  placeholder="Leave blank to preserve"
                />
              </Field>
              <div className="pf-form-actions">
                <button
                  className="pf-button"
                  type="submit"
                  disabled={oauth.isPending}
                  value="save"
                >
                  Test and save credentials
                </button>
                <button
                  className="pf-secondary-button"
                  type="submit"
                  disabled={oauth.isPending}
                  value="test"
                >
                  Test only
                </button>
              </div>
              {oauth.data ? <Alert>{oauth.data.message}</Alert> : null}
              <ErrorMessage error={oauth.error} />
            </form>
          </Panel>
          <Panel title="New enrollment defaults">
            {settings.data ? (
              <form
                className="pf-form-grid"
                onSubmit={(e) => defaults.mutate(formValues(e))}
              >
                <Field label="Management URL">
                  <input
                    className="pf-input"
                    name="url"
                    type="url"
                    defaultValue={settings.data.managementUrl}
                  />
                </Field>
                <Field label="Published binary release">
                  <input
                    className="pf-input"
                    name="version"
                    required
                    defaultValue={settings.data.releaseVersion}
                  />
                </Field>
                <p className="pf-help">
                  Applies to new enrollments only. Existing binaries, services
                  and management URLs are not changed.
                </p>
                <Submit
                  pending={defaults.isPending}
                  label="Save enrollment defaults"
                />
                {defaults.data ? (
                  <Alert>Defaults saved for new enrollments.</Alert>
                ) : null}
                <ErrorMessage error={defaults.error} />
              </form>
            ) : null}
          </Panel>
        </>
      ) : (
        <Alert>
          Read only. An administrator can rotate credentials and change
          enrollment defaults.
        </Alert>
      )}
    </>
  );
}
export function AccessPage() {
  const admin = useAdmin();
  const [confirmSelfRemoval, setConfirmSelfRemoval] = useState(false);
  const qc = useQueryClient();
  const access = useQuery({
    queryKey: ["access"],
    queryFn: () => api.getAccess({}),
  });
  const identity = useQuery({
    queryKey: ["identity"],
    queryFn: () => api.whoAmI({}),
  });
  const save = useMutation({
    mutationFn: (form: FormData) =>
      api.saveAccess({
        capability: String(form.get("capability") || ""),
        confirmSelfRemoval: form.get("confirm_self_removal") === "on",
        roles: Object.fromEntries(
          ["admin", "operator", "viewer"].map((role) => [
            role,
            {
              selectors: String(form.get(role) || "")
                .split("\n")
                .map((v) => v.trim())
                .filter(Boolean),
            },
          ]),
        ),
      }),
    onSuccess: async () => {
      setConfirmSelfRemoval(false);
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["access"] }),
        qc.invalidateQueries({ queryKey: ["identity"] }),
      ]);
    },
  });
  return (
    <>
      <Intro
        title="Access roles"
        subtitle="Authorization comes from verified Tailscale identity, account roles, tags and capabilities."
      />
      <Panel title="Current identity">
        <p>{identity.data?.login || identity.data?.hostname}</p>
        <p>
          <code>{identity.data?.nodeId}</code>
        </p>
        <p>Roles: {identity.data?.roles.join(", ")}</p>
        <p className="pf-help">
          Tagged nodes never inherit human Owner/Admin roles. Account roles
          require users:read.
        </p>
      </Panel>
      <ErrorMessage error={access.error} />
      {access.data ? (
        <Panel title="Role mappings">
          <form
            className="pf-form-grid"
            onSubmit={(e) => save.mutate(formValues(e))}
          >
            {["admin", "operator", "viewer"].map((role) => (
              <Field
                key={role}
                label={role}
                hint="One selector per line, for example autogroup:owner or tag:automation."
              >
                <textarea
                  className="pf-input"
                  name={role}
                  disabled={!admin}
                  defaultValue={
                    access.data?.roles[role]?.selectors.join("\n") || ""
                  }
                />
              </Field>
            ))}
            <Field label="Application capability">
              <input
                className="pf-input"
                name="capability"
                defaultValue={access.data.capability}
                disabled={!admin}
              />
            </Field>
            {admin ? <label className="pf-help"><input type="checkbox" name="confirm_self_removal" checked={confirmSelfRemoval} onChange={e => setConfirmSelfRemoval(e.target.checked)} /> I confirm removing my own administrator access if these mappings exclude me. An active tailnet Owner must remain an administrator. This may sign me out.</label> : null}
            {admin ? <Submit pending={save.isPending} /> : null}
            <ErrorMessage error={save.error} />
            {save.data ? <Alert>Role mappings saved.</Alert> : null}
          </form>
          <JsonEditor
            value={toJson(AccessSettingsSchema, access.data)}
            readOnly={!admin}
            onSave={
              admin
                ? async (value) => {
                    await api.saveAccess(
                      fromJsonString(
                        AccessSettingsSchema,
                        JSON.stringify(value),
                      ),
                    );
                    await qc.invalidateQueries({ queryKey: ["access"] });
                  }
                : undefined
            }
          />
        </Panel>
      ) : null}
    </>
  );
}
