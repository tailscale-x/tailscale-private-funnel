import { StrictMode, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query";
import { api } from "./api";
import {
  Icon,
  ErrorMessage,
  Intro,
  Panel,
  Badge,
  date,
  auditLabel,
} from "./components";
import { ProvidersPage, ZonesPage } from "./providers";
import { NodesPage, EnrollmentPage } from "./nodes";
import { PolicyPage, SettingsPage, AccessPage } from "./settings";
import { ExposuresPage, TailnetPage } from "./exposures";
import { ControlsPage } from "./installation";
import { DeploymentWizard, DeploymentActions } from "./deployment";
import { OperationsPage, DNSPage } from "./operations";
import "./responsive.css";
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 10_000, refetchOnWindowFocus: true },
    mutations: { retry: false },
  },
});
import { Permissions, useAdmin } from "./permissions";
const groups = [
  {
    name: "Overview",
    items: [
      ["/", "Dashboard", "layout-dashboard"],
      ["/exposures", "Exposures", "world"],
    ],
  },
  {
    name: "Infrastructure",
    items: [
      ["/controls", "Controls", "server-2"],
      ["/loadbalancers", "Load balancers", "server-2"],
      ["/gateways", "Gateways", "route"],
      ["/k8s/operator", "Kubernetes operator", "settings"],
      ["/applications", "Applications", "server-2"],
      ["/providers", "DNS providers", "database"],
      ["/zones", "Zones", "link"],
    ],
  },
  {
    name: "Access",
    items: [
      ["/settings", "Tailscale", "settings"],
      ["/access", "Access roles", "users"],
      ["/policies", "Exposure policy", "shield-check"],
    ],
  },
  { name: "Operations", items: [["/operations", "Operations", "list-check"]] },
];
function Dashboard() {
  const admin = useAdmin();
  const health = useQuery({
    queryKey: ["health"],
    queryFn: () => api.health({}),
  });
  const nodes = useQuery({
    queryKey: ["nodes"],
    queryFn: () => api.listNodes({}),
  });
  const audit = useQuery({
    queryKey: ["audit", "recent"],
    queryFn: () => api.listAudit({ limit: 6 }),
  });
  return (
    <>
      <Intro
        title="Your private network. Public possibilities."
        subtitle="Expose approved services through isolated gateways while control manages identity, policy and owned DNS."
      />
      <ErrorMessage error={health.error || nodes.error} />
      <div className="pf-react-stats">
        {[
          [
            "Gateways",
            nodes.data?.items.filter(
              (n) =>
                n.mode !== "loadbalancer" &&
                n.mode !== "k8s-forwarder" &&
                !n.revoked,
            ).length,
          ],
          [
            "Load balancers",
            nodes.data?.items.filter(
              (n) => n.mode === "loadbalancer" && !n.revoked,
            ).length,
          ],
          ["Applications", health.data?.applications],
          ["DNS providers", health.data?.providers],
        ].map(([label, count]) => (
          <section className="pf-surface" key={String(label)}>
            <p className="pf-kicker">{label}</p>
            <strong className="pf-react-stat">{count ?? "—"}</strong>
          </section>
        ))}
      </div>
      <Panel title="Network topology">
        <div className="pf-react-topology">
          <div>
            <Icon name="shield-check" />
            <strong>Control plane</strong>
            <small>Identity · policy · DNS ownership</small>
          </div>
          <span aria-hidden="true">→</span>
          <div>
            <Icon name="server-2" />
            <strong>Load balancers</strong>
            <small>Public HTTPS · trusted forwarding headers</small>
          </div>
          <span aria-hidden="true">→</span>
          <div>
            <Icon name="route" />
            <strong>Specialized gateways</strong>
            <small>Docker · Tailnet · Kubernetes</small>
          </div>
        </div>
      </Panel>
      <div className="pf-react-two">
        <Panel title="Next step">
          <p>
            Authorize a hostname rule, enroll a gateway, then verify its backend
            and public readiness.
          </p>
          {admin ? (
            <a className="pf-button" href="/deploy/gateways/create">
              Add gateway
            </a>
          ) : null}
          <a className="pf-secondary-button" href="/policies">
            Review policy
          </a>
        </Panel>
        <Panel title="Recent activity">
          {audit.isPending ? (
            <p role="status">Loading activity…</p>
          ) : !audit.data?.items.length && !audit.error ? (
            <p className="pf-help">
              Audited changes will appear here after your first operation.
            </p>
          ) : null}
          {audit.data?.items.map((a, i) => (
            <div className="pf-react-event" key={i}>
              <strong title={a.action}>{auditLabel(a.action)}</strong>
              <small>
                {a.actor} · {date(a.createdAt)}
              </small>
            </div>
          ))}
          <ErrorMessage error={audit.error} />
        </Panel>
      </div>
    </>
  );
}
function Route() {
  const path = window.location.pathname;
  if (path === "/providers") return <ProvidersPage />;
  if (path === "/zones") return <ZonesPage />;
  if (path === "/gateways" || path === "/loadbalancers")
    return <NodesPage loadbalancers={path === "/loadbalancers"} />;
  if (path.startsWith("/deploy/") && path.endsWith("/create") || path === "/k8s/operator" || path === "/deploy/loadbalancers/create")
    return <EnrollmentPage />;
  if (path === "/policies") return <PolicyPage />;
  if (path === "/deploy/actions") return <DeploymentActions />;
  if (path === "/controls") return <ControlsPage />;
  if (path === "/settings") return <SettingsPage />;
  if (path === "/access") return <AccessPage />;
  if (path === "/tailnet-exposures") return <TailnetPage />;
  if (path === "/exposures" || path === "/applications")
    return <ExposuresPage applications={path === "/applications"} />;
  if (path === "/operations") return <OperationsPage />;
  if (path === "/dns") return <DNSPage />;
  if (path === "/") return <Dashboard />;
  return (
    <Intro
      title="Page not found"
      subtitle="Choose a page from the navigation to continue."
    />
  );
}
function App() {
  const [open, setOpen] = useState(false);
  const [mobile, setMobile] = useState(
    () => window.matchMedia("(max-width: 1023px)").matches,
  );
  const menu = useRef<HTMLButtonElement>(null);
  const sidebar = useRef<HTMLElement>(null);
  useEffect(() => {
    const media = window.matchMedia("(max-width: 1023px)");
    const change = () => {
      setMobile(media.matches);
      if (!media.matches) setOpen(false);
    };
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    if (!open) return;
    sidebar.current?.querySelector<HTMLAnchorElement>("a")?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        menu.current?.focus();
      }
      if (event.key === "Tab" && mobile) {
        const links = sidebar.current?.querySelectorAll<HTMLAnchorElement>("a");
        if (!links?.length) return;
        const first = links[0],
          last = links[links.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [open, mobile]);
  const identity = useQuery({
    queryKey: ["identity"],
    queryFn: () => api.whoAmI({}),
    retry: false,
  });
  const path = window.location.pathname;
  return (
    <Permissions.Provider
      value={identity.data?.roles.includes("admin") ?? false}
    >
      <a className="pf-skip-link" href="#page-content">
        Skip to content
      </a>
      <div className="pf-shell pf-app">
        <aside
          className={`pf-sidebar ${open ? "pf-sidebar-open" : ""}`}
          ref={sidebar}
          id="main-sidebar"
          inert={mobile && !open}
        >
          <a className="pf-brand" href="/">
            <img
              className="pf-brand-logo"
              src="/assets/brand/local-funnel-logo.png"
              alt="Local Funnel"
            />
            <span>
              <strong>Local Funnel</strong>
              <small>Tailscale Private Funnel</small>
            </span>
          </a>
          <nav className="pf-nav" aria-label="Main navigation">
            {groups.map((g) => (
              <div key={g.name}>
                <p className="pf-nav-label">{g.name}</p>
                {g.items.map(([url, label, icon]) => (
                  <a
                    key={url}
                    href={url}
                    className={path === url ? "is-active" : ""}
                    aria-current={path === url ? "page" : undefined}
                  >
                    <Icon name={icon} />
                    {label}
                  </a>
                ))}
              </div>
            ))}
          </nav>
          <div className="pf-sidebar-footer">
            <a href="/docs">
              <Icon name="book" />
              Documentation
            </a>
            <a href="https://github.com/tailscale-x/tailscale-private-funnel/issues">
              <Icon name="help-circle" />
              Support
            </a>
          </div>
        </aside>
        {open ? (
          <button
            className="pf-react-backdrop"
            aria-label="Close navigation"
            onClick={() => {
              setOpen(false);
              menu.current?.focus();
            }}
          />
        ) : null}
        <div className="pf-main">
          <header className="pf-topbar">
            <button
              ref={menu}
              aria-controls="main-sidebar"
              className="pf-menu"
              onClick={() => setOpen((v) => !v)}
              aria-expanded={open}
              aria-label={open ? "Close navigation" : "Open navigation"}
            >
              <Icon name="menu-2" />
            </button>
            <div className="pf-topbar-context">
              <span className="pf-kicker">Control plane</span>
              <strong>Local Funnel</strong>
            </div>
            <div className="pf-topbar-right">
              <Badge
                ok={!!identity.data}
                label={
                  identity.isPending
                    ? "Connecting…"
                    : identity.data
                      ? "Tailnet connected"
                      : "Access unavailable"
                }
              />
              <span className="pf-identity">
                {identity.data?.login ||
                  identity.data?.hostname ||
                  "Verified Tailscale identity"}
              </span>
            </div>
          </header>
          <main className="pf-content" id="page-content" tabIndex={-1}>
            {identity.isPending ? (
              <p role="status">Connecting to control…</p>
            ) : identity.error ? (
              <>
                <Intro
                  title="Control access unavailable"
                  subtitle="Connect to the tailnet and verify your assigned control role."
                />
                <ErrorMessage error={identity.error} />
              </>
            ) : (
              <Route />
            )}
          </main>
          <footer className="pf-footer">
            <span>Local Funnel · tailnet-only management</span>
            <a href="/llms.txt">Agent documentation</a>
          </footer>
        </div>
      </div>
    </Permissions.Provider>
  );
}
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
