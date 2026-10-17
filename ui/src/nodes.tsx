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
  const enrollments = useQuery({
    queryKey: ["enrollments"],
    queryFn: () => api.listEnrollments({}),
    refetchInterval: 15000,
  });
  const revokeEnrollment = useMutation({
    mutationFn: (id: string) => api.revokeEnrollment({ id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["enrollments"] }),
  });
  const [type, setType] = useState("");
  const [rename, setRename] = useState<Node>();
  const action = useMutation({
    mutationFn: async ({ node, cleanup }: { node: Node; cleanup: boolean }) =>
      cleanup
        ? api.cleanupNode({ id: node.id })
        : api.revokeNode({ id: node.id }),
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
            : "One persistent identity per gateway. Each type discovers and forwards only its approved backend sources."
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
                <code>{n.tailnetId || "Enrollment pending"}</code>
              </td>
              <td>{n.mode}</td>
              <td>
                <Badge
                  ok={!n.revoked && fresh(n.lastReport)}
                  label={
                    n.revoked
                      ? "Revoked"
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
                  {n.mode === "tailnet-gateway" && !n.revoked ? (
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
                              n.revoked
                                ? `Retry cleanup for ${n.name}?`
                                : `Revoke node access for ${n.name}? Owned exposures and Service hosts will be withdrawn. Host or cluster deployment resources are not deleted.`,
                            )
                          )
                            action.mutate({ node: n, cleanup: n.revoked });
                        }}
                      >
                        {n.revoked ? "Retry cleanup" : "Revoke node access"}
                      </button>
                    </>
                  ) : null}
                </div>
              </td>
            </tr>
          ))}
        </Table>
      </Panel>
      <Panel title="Unused enrollments">
        <Table
          headers={["Name", "Type", "Created", "State", "Actions"]}
          empty={
            !enrollments.isPending &&
            !enrollments.data?.items.some(
              (enrollment) =>
                (loadbalancers
                  ? enrollment.mode === "loadbalancer"
                  : enrollment.mode !== "loadbalancer") &&
                !nodes.data?.items.some((node) => node.id === enrollment.id),
            )
          }
        >
          {enrollments.data?.items
            .filter(
              (enrollment) =>
                (loadbalancers
                  ? enrollment.mode === "loadbalancer"
                  : enrollment.mode !== "loadbalancer") &&
                !nodes.data?.items.some((node) => node.id === enrollment.id),
            )
            .map((enrollment) => (
              <tr key={enrollment.id}>
                <td>
                  <strong>{enrollment.name}</strong>
                  <small className="pf-help">{enrollment.hostname}</small>
                </td>
                <td>{enrollment.mode}</td>
                <td>{date(enrollment.createdAt)}</td>
                <td>{enrollment.revoked ? "Revoked" : "Not joined"}</td>
                <td>
                  {admin && !enrollment.revoked ? (
                    <button
                      className="pf-secondary-button"
                      disabled={revokeEnrollment.isPending}
                      onClick={() => {
                        if (
                          confirm(
                            `Revoke unused enrollment for ${enrollment.name}?`,
                          )
                        )
                          revokeEnrollment.mutate(enrollment.id);
                      }}
                    >
                      Revoke enrollment
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
        </Table>
        <ErrorMessage error={enrollments.error || revokeEnrollment.error} />
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
              enrollment remain unchanged.
            </p>
            <Submit pending={save.isPending} />
            <ErrorMessage error={save.error} />
          </form>
        </Modal>
      ) : null}
    </>
  );
}
export { DeploymentWizard as EnrollmentPage } from "./deployment";
