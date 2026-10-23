import { useMutation } from "@tanstack/react-query";
import { deployments } from "./api";
import { Alert, ErrorMessage, Field, Intro, Panel, Submit } from "./components";
import { CommandBlock } from "./deployment";

export function TunnelPage() {
  const instructions = useMutation({
    mutationFn: (form: FormData) => deployments.tunnelInstructions({
      hostname: String(form.get("hostname") || "").trim(),
      port: Number(form.get("port")),
      localAddress: String(form.get("localAddress") || "127.0.0.1"),
      name: String(form.get("name") || "").trim(),
      tailscaleConnection: String(form.get("connection") || "embedded"),
    }),
  });
  return <>
    <Intro title="Expose a local port" subtitle="Expose a local HTTP port from Linux or Apple Silicon macOS. By default, phone home through host Tailscale and run a separate ephemeral identity; no Docker or service installation." />
    <Panel title="Local HTTP service">
      <Alert>Generating a command is read-only. The target host must already be connected to Tailscale and able to reach private control. Embedded mode needs no host tag or Funnel administrator role. Control issues the temporary key when you run the command; hostname policy still applies.</Alert>
      <form className="pf-form-grid" onChange={() => instructions.reset()} onSubmit={event => { event.preventDefault(); instructions.mutate(new FormData(event.currentTarget)); }}>
        <Field label="Tailscale connection"><select className="pf-input" name="connection" defaultValue="embedded"><option value="embedded">Embedded · temporary identity (recommended)</option><option value="host">Host · existing role-tagged device</option></select></Field>
        <Field label="Public hostname" hint="Use a managed domain. Control checks exposure policy when the command runs."><input className="pf-input" name="hostname" placeholder="app.example.com" autoCapitalize="none" autoCorrect="off" required /></Field>
        <Field label="Local HTTP port"><input className="pf-input" name="port" type="number" min="1" max="65535" defaultValue="8080" required /></Field>
        <Field label="Loopback address"><select className="pf-input" name="localAddress" defaultValue="127.0.0.1"><option value="127.0.0.1">127.0.0.1 · IPv4</option><option value="::1">::1 · IPv6</option></select></Field>
        <Field label="Tunnel name (optional)" hint="A local handle. Leave blank for a stable name derived from the hostname and backend."><input className="pf-input" name="name" pattern="[a-z0-9]([a-z0-9-]{0,51}[a-z0-9])?" maxLength={53} /></Field>
        <div className="pf-form-actions"><ErrorMessage error={instructions.error} /><Submit pending={instructions.isPending} label="Generate command" /></div>
      </form>
    </Panel>
    {instructions.data ? <Panel title="Run on the backend host">
      <p className="pf-help">Use a CLI matching control version {instructions.data.version || "your deployment"}. Keep the terminal process running. Command generation has not deployed a tunnel.</p>
      <ul>{instructions.data.prerequisites.map(item => <li key={item}>{item}</li>)}</ul>
      <CommandBlock label="Foreground tunnel" command={instructions.data.commands.run || ""} />
      {instructions.data.steps.map(step => <section key={step.id}><h3>{step.title}</h3><p>{step.description}</p></section>)}
      <Alert>Embedded mode uses a fresh single-use key and an in-memory identity; host mode requires an already tagged device and requests no key. Ctrl+C withdraws only this tunnel. The host remains unchanged. DNS cleanup is asynchronous; after a crash, freshness expiry retires the tunnel.</Alert>
    </Panel> : null}
  </>;
}
