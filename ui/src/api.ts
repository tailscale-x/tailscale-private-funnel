import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { ManagementService } from "./gen/funnel/v1/management_pb";
import { DeploymentService } from "./gen/funnel/v1/provisioning_pb";
const transport = createConnectTransport({
  baseUrl: window.location.origin,
  defaultTimeoutMs: 30_000,
});
export const api = createClient(ManagementService, transport);
export const deployments = createClient(DeploymentService, transport);
