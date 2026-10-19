import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, deployments } from "./api";
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
  fresh,
  formValues,
} from "./components";
import type { Node } from "./gen/funnel/v1/management_pb";
import type {
  DeploymentInstructions,
  DeploymentRequest,
} from "./gen/funnel/v1/provisioning_pb";
import { create } from "@bufbuild/protobuf";
import { DeploymentRequestSchema } from "./gen/funnel/v1/provisioning_pb";
export function NodesPage({
  loadbalancers = false,
}: {
  loadbalancers?: boolean;
}) {
  const admin = useAdmin();
  const qc = useQueryClient();
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.listNodes({}),
    refetchInterval: 15_000,
  });

  const [type, setType] = useState("");
  const [rename, setRename] = useState<Node>();
  const action = useMutation({
    mutationFn: async ({ node, cleanup }: { node: Node; cleanup: boolean }) =>
      cleanup
        ? api.cleanupNode({ id: node.id })
        : api.disableNode({ id: node.id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["nodes"] }),
  });
  const save = useMutation({
    mutationFn: (form: FormData) =>
      api.renameNode({ id: rename!.id, name: String(form.get("name")) }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["nodes"] });
      setRename(undefined);
    },
  });
  const items =
    nodes.data?.items.filter(
      (n) =>
        (loadbalancers
          ? n.mode === "loadbalancer"
          : n.mode !== "loadbalancer") &&
        (!type || n.mode === type),
    ) || [];
  return (
    <>
      <Intro
        title={loadbalancers ? "Load balancers" : "Gateways"}
        subtitle={
          loadbalancers
            ? "Public HTTPS edges. Control automatically distributes eligible exposures and reconciles their public addresses."
            : "Each gateway keeps a logical identity. Native network identities persist; Kubernetes devices are disposable. Each type forwards only its approved backend sources."
        }
        action={
          admin ? (
            <a
              className="pf-button"
              href={loadbalancers ? "/deploy/loadbalancers/create" : "/deploy/gateways/create"}
            >
              Add {loadbalancers ? "load balancer" : "gateway"}
            </a>
          ) : null
        }
      />
      {!loadbalancers ? (
        <Field label="Gateway type">
          <select
            className="pf-input"
            value={type}
            onChange={(e) => setType(e.target.value)}
          >
            <option value="">All types</option>
            {["docker-gateway", "tailnet-gateway", "k8s-gateway"].map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
        </Field>
      ) : null}
      <ErrorMessage error={nodes.error || action.error} />
      {action.data ? <Alert>{action.data.message}</Alert> : null}
      <Panel title="Node inventory">
        <Table
          headers={[
            "Name / hostname",
            "Type",
            "Health",
            "Last report",
            "Actions",
          ]}
          empty={!nodes.isPending && !items.length}
        >
          {items.map((n) => (
            <tr key={n.id}>
              <td>
                <strong>{n.name}</strong>
                <small className="pf-help">{n.hostname}</small>
                <code>{n.tailnetId || "Awaiting registration"}</code><small className="pf-help">{n.tailscaleConnection || "Embedded"}{n.instanceId ? ` · instance ${n.instanceId.slice(0,8)} · generation ${n.generation}` : ""}</small>
              </td>
              <td>{n.mode}</td>
              <td>
                <Badge
                  ok={!n.disabled && fresh(n.lastReport)}
                  label={
                    n.disabled
                      ? "Disabled"
                      : fresh(n.lastReport)
                        ? "Reporting"
                        : "Offline / pending"
                  }
                />
              </td>
              <td>{date(n.lastReport)}{n.diagnostics.map((message,index)=><small className="pf-help" key={index}>{message}</small>)}</td>
              <td>
                <div className="pf-react-actions">
                  {n.deployment?.resourceName && admin ? <a className="pf-text-link" href={"/deploy/actions?node_id="+encodeURIComponent(n.id)}>Generate deployment instructions</a> : null}
                  {n.mode === "tailnet-gateway" && !n.disabled ? (
                    <a
                      href={
                        "/tailnet-exposures?gateway_id=" +
                        encodeURIComponent(n.id)
                      }
                      className="pf-text-link"
                    >
                      Expose a machine
                    </a>
                  ) : null}
                  {admin ? (
                    <>
                      <button
                        className="pf-secondary-button"
                        onClick={() => setRename(n)}
                      >
                        Rename
                      </button>
                      <button
                        disabled={action.isPending}
                        className="pf-secondary-button"
                        onClick={() => {
                          if (
                            confirm(
                              n.disabled
                                ? `Retry cleanup for ${n.name}?`
                                : `Disable component for ${n.name}? Owned exposures will be withdrawn. This does not remove host/cluster deployments or revoke the device role tag in Tailscale.`,
                            )
                          )
                            action.mutate({ node: n, cleanup: n.disabled });
                        }}
                      >
                        {n.disabled ? "Retry cleanup" : "Disable component"}
                      </button>
                    </>
                  ) : null}
                </div>
              </td>
            </tr>
          ))}
        </Table>
      </Panel>

      {rename ? (
        <Modal
          title={`Rename ${rename.name}`}
          onClose={() => setRename(undefined)}
        >
          <form onSubmit={(e) => save.mutate(formValues(e))}>
            <Field label="Name">
              <input
                className="pf-input"
                name="name"
                required
                maxLength={128}
                defaultValue={rename.name}
              />
            </Field>
            <p className="pf-help">
              Renaming changes only the display name. Identity, DNS and
              role authority remain unchanged.
            </p>
            <Submit pending={save.isPending} />
            <ErrorMessage error={save.error} />
          </form>
        </Modal>
      ) : null}
    </>
  );
}
export { DeploymentWizard as RegistrationPage } from "./deployment";
