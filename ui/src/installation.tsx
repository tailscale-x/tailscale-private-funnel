import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { useAdmin } from "./permissions";
import { ErrorMessage, Intro, Panel } from "./components";
export function ControlsPage(){
 const health=useQuery({queryKey:['health'],queryFn:()=>api.health({})});const admin=useAdmin();const d=health.data?.deployment;
 return <><Intro title="Controls" subtitle="This installation manages its own control plane. Other controls are independent installations." action={admin?<a className="pf-button" href="/deploy/controls/create">Create control instructions</a>:null}/><ErrorMessage error={health.error}/><Panel title="Current control"><dl><dt>Version</dt><dd>{health.data?.version||'Loading…'}</dd><dt>Database</dt><dd>{health.data?.databaseBackend||"Unknown"} · {health.data?.databaseReady?"Ready":"Unavailable"}</dd><dt>Deployment</dt><dd>{d?.target||'Not reported'}</dd><dt>Resource handle</dt><dd>{d?.resourceName||'Not reported'}</dd><dt>Namespace</dt><dd>{d?.namespace||'Native or not reported'}</dd></dl>{admin&&d?.resourceName ? <a className="pf-secondary-button" href="/deploy/actions?control=true">Generate deployment instructions</a>:null}<p>Use native controls get or k8s controls get with the deployment handle to inspect infrastructure. Creating another control does not copy this installation or migrate its database.</p></Panel></>;
}
