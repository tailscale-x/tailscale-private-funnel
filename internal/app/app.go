package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerclient "github.com/docker/docker/client"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	tailscaleclient "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"github.com/tailscale-x/tailscale-private-funnel/internal/web"
	"tailscale.com/tsnet"
)

func defaults(mode string, o Options) (Options, error) {
	if o.Hostname == "" {
		o.Hostname, _ = os.Hostname()
		if store.IsGateway(mode) || mode == "loadbalancer" {
			o.Hostname = strings.SplitN(o.Hostname, ".", 2)[0] + "-" + mode
		}
	}
	if o.StateDir == "" {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return o, err
			}
			base = filepath.Join(home, ".local", "state")
		}
		o.StateDir = filepath.Join(base, "tailscale-private-funnel", mode)
	}
	if o.DataDir == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return o, err
			}
			base = filepath.Join(home, ".local", "share")
		}
		o.DataDir = filepath.Join(base, "tailscale-private-funnel", mode)
	}
	if o.Listen == "" {
		if mode == "control" {
			o.Listen = ":8080"
		} else {
			o.Listen = ":8080"
		}
	}
	if o.LoginServer == "" {
		o.LoginServer = "https://controlplane.tailscale.com"
	}
	requiredTag := map[string]string{
		"control":         "tag:tailscale-private-funnel",
		"loadbalancer":    "tag:tailscale-private-funnel",
		"docker-gateway":  "tag:tailscale-private-funnel",
		"tailnet-gateway": "tag:tailscale-private-funnel",
		"k8s-gateway":     "tag:tailscale-private-funnel",
	}[mode]
	if requiredTag != "" {
		if len(o.Tags) == 0 {
			o.Tags = []string{requiredTag}
		} else {
			for _, tag := range o.Tags {
				if tag != "tag:tailscale-private-funnel" && (!store.IsGateway(mode) || !strings.HasPrefix(tag, "tag:") || strings.ContainsAny(tag, " \t\n")) {
					return o, fmt.Errorf("unsupported Tailscale tag %q; use tag:tailscale-private-funnel", tag)
				}
			}
			if !containsString(o.Tags, requiredTag) {
				return o, fmt.Errorf("%s requires %s", mode, requiredTag)
			}
		}
	}
	if mode != "control" && o.ManagementURL == "" && o.NonInteractive {
		return o, errors.New("--management-url is required for loadbalancer and gateway deployment")
	}
	return o, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == wanted {
			return true
		}
	}
	return false
}

func Setup(ctx context.Context, mode string, o Options) error {
	if mode != "control" {
		return errors.New("only control uses direct OAuth bootstrap")
	}
	if !o.NonInteractive && o.OAuthClientIDFile == "" && o.OAuthClientSecretFile == "" && !o.OAuthClientIDStdin && !o.OAuthClientSecretStdin {
		if err := controlWizard(&o); err != nil {
			return err
		}
	}
	var err error
	o, err = defaults(mode, o)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(o.StateDir, "joined")); err == nil {
		if err := validatePersistentState(mode, o); err != nil {
			return err
		}
	}
	return Serve(ctx, mode, o)
}

func controlWizard(o *Options) error {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("control join requires OAuth credential files; use --non-interactive for scripted setup")
	}
	reader := bufio.NewReader(os.Stdin)
	read := func(label, current string) (string, error) {
		if current != "" {
			fmt.Printf("%s [%s]: ", label, current)
		} else {
			fmt.Printf("%s: ", label)
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return current, nil
		}
		return line, nil
	}
	fmt.Println("tailscale-private-funnel control setup")
	fmt.Println()
	fmt.Println("Tailscale OAuth client requirements:")
	fmt.Println("  scopes: auth_keys, devices:core, services, users:read")
	fmt.Println("  users:read verifies Owner/Admin roles for control UI access; no user write permission is needed")
	fmt.Println("  permitted tag: tag:tailscale-private-funnel")
	fmt.Println("  do not paste credentials; store each value in a protected 0600 file")
	fmt.Println()
	fmt.Println("Add this grants policy once in Tailscale Access controls:")
	fmt.Println(`  {"grants":[{"src":["tag:tailscale-private-funnel"],"dst":["tag:tailscale-private-funnel"],"ip":["*"]}]}`)
	fmt.Println()
	if o.Tailnet, err = read("Tailnet", o.Tailnet); err != nil {
		return err
	}
	if o.OAuthClientIDFile, err = read("OAuth client ID file", o.OAuthClientIDFile); err != nil {
		return err
	}
	if o.OAuthClientSecretFile, err = read("OAuth client secret file", o.OAuthClientSecretFile); err != nil {
		return err
	}
	if o.Hostname, err = read("Control hostname", o.Hostname); err != nil {
		return err
	}
	if o.StateDir, err = read("State directory", o.StateDir); err != nil {
		return err
	}
	if o.DataDir, err = read("Data directory", o.DataDir); err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("Starting control with tailnet %q, OAuth files %q and %q.\n", o.Tailnet, o.OAuthClientIDFile, o.OAuthClientSecretFile)
	answer, err := read("Continue? (yes/no)", "yes")
	if err != nil {
		return err
	}
	if strings.ToLower(answer) != "yes" && strings.ToLower(answer) != "y" {
		return errors.New("control setup cancelled")
	}
	return nil
}

func bindSigningKey(ctx context.Context, mode string, o Options, publicKey string, client *http.Client) error {
	managementURL := o.ManagementURL
	if managementURL == "" {
		managementURL = o.BootstrapURL
	}
	u, err := url.Parse(managementURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/enrollments/" + url.PathEscape(o.EnrollmentID) + "/signing-key"
	q := u.Query()
	q.Set("code", o.EnrollmentCode)
	u.RawQuery = q.Encode()
	body, _ := json.Marshal(map[string]string{"public_key": publicKey})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		message, _ := io.ReadAll(res.Body)
		return fmt.Errorf("signing-key enrollment failed: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func tsnetHTTPClient(s *tsnet.Server) *http.Client {
	client := s.HTTPClient()
	client.Timeout = 30 * time.Second
	return client
}

func authKey(ctx context.Context, mode string, o Options) (string, error) {
	if mode == "control" {
		key, _, err := controlOAuthAuthKey(ctx, o)
		return key, err
	}
	// Container supervisors may receive a freshly minted key through the
	// process environment. It is intentionally read once and never persisted;
	// this also lets a host bootstrap a node before the control bootstrap
	// listener is reachable over its tailnet ACL.
	if key := strings.TrimSpace(os.Getenv("TS_AUTHKEY")); key != "" {
		return key, nil
	}
	return "", errors.New("first join requires the generated temporary TS_AUTHKEY assignment")
}

func controlOAuthAuthKey(ctx context.Context, o Options) (string, bool, error) {
	st, err := openRuntimeStore(o)
	if err != nil {
		return "", true, err
	}
	defer st.Close()
	read := func(inline, file string, stdin bool) (string, error) {
		if strings.TrimSpace(inline) != "" {
			return strings.TrimSpace(inline), nil
		}
		if file != "" {
			b, err := os.ReadFile(file)
			return strings.TrimSpace(string(b)), err
		}
		if stdin {
			b, err := io.ReadAll(os.Stdin)
			return strings.TrimSpace(string(b)), err
		}
		return "", nil
	}
	id, err := read(o.OAuthClientID, o.OAuthClientIDFile, o.OAuthClientIDStdin)
	if err != nil {
		return "", true, err
	}
	secret, err := read(o.OAuthClientSecret, o.OAuthClientSecretFile, o.OAuthClientSecretStdin)
	if err != nil {
		return "", true, err
	}
	if id == "" {
		id, _ = st.GetSecret("tailscale_oauth_client_id")
	}
	if secret == "" {
		secret, _ = st.GetSecret("tailscale_oauth_client_secret")
	}
	tailnet := strings.TrimSpace(o.Tailnet)
	if tailnet == "" {
		var settings struct {
			Name string `json:"name"`
		}
		_ = st.GetJSON("tailnet", &settings)
		tailnet = settings.Name
	}
	if id == "" || secret == "" || tailnet == "" {
		return "", true, errors.New("control requires OAuth client ID, secret, and --tailnet or stored tailnet")
	}
	if err := st.SetSecret("tailscale_oauth_client_id", id); err != nil {
		return "", true, err
	}
	if err := st.SetSecret("tailscale_oauth_client_secret", secret); err != nil {
		return "", true, err
	}
	settings := map[string]any{}
	_ = st.GetJSON("tailnet", &settings)
	settings["name"] = tailnet
	if err := st.SetJSON("tailnet", settings); err != nil {
		return "", true, err
	}
	key, err := (tailscaleclient.Client{ClientID: id, ClientSecret: secret, Tailnet: tailnet}).CreateAuthKey(ctx, "control", o.Hostname, []string{"tag:tailscale-private-funnel"})
	return key, true, err
}

type runtimeStatus struct {
	Mode     string   `json:"mode"`
	Hostname string   `json:"hostname"`
	State    string   `json:"state"`
	IPs      []string `json:"ips"`
	Health   []string `json:"health,omitempty"`
}

func writeRuntimeStatus(dataDir string, status runtimeStatus) error {
	if err := os.WriteFile(filepath.Join(dataDir, "serve.pid"), []byte(fmt.Sprintf("%d", os.Getpid())), 0600); err != nil {
		return err
	}
	b, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "runtime-status.json"), b, 0600)
}

func processAlive(pid string) bool {
	var process *os.Process
	parsed := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(pid), "%d", &parsed); err != nil || parsed <= 0 {
		return false
	}
	process, err := os.FindProcess(parsed)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func readRuntimeStatus(dataDir string) (runtimeStatus, bool) {
	pid, err := os.ReadFile(filepath.Join(dataDir, "serve.pid"))
	if err != nil || !processAlive(string(pid)) {
		return runtimeStatus{}, false
	}
	b, err := os.ReadFile(filepath.Join(dataDir, "runtime-status.json"))
	if err != nil {
		return runtimeStatus{}, false
	}
	var status runtimeStatus
	if json.Unmarshal(b, &status) != nil || status.Mode == "" {
		return runtimeStatus{}, false
	}
	return status, true
}

func serveOptions(mode string, o Options) (Options, error) {
	if o.ConfigFile != "" {
		var err error
		o, err = loadConfig(mode, o)
		if err != nil {
			return o, err
		}
	}
	return defaults(mode, o)
}

func Serve(ctx context.Context, mode string, o Options) error {
	var err error
	o, err = serveOptions(mode, o)
	if err != nil {
		return err
	}
	if o.RequireJoined {
		if err := validatePersistentState(mode, o); err != nil {
			return err
		}
	}
	var runtimeReady atomic.Bool
	if !o.BootstrapOnly && (mode == "control" || mode == "loadbalancer") && o.ProbeListen != "" {
		ln, err := net.Listen("tcp", o.ProbeListen)
		if err != nil {
			return err
		}
		probes := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Path == "/readyz" && !runtimeReady.Load() {
				http.Error(w, "starting", 503)
				return
			}
			w.WriteHeader(204)
		})}
		go probes.Serve(ln)
		defer probes.Close()
	}
	if err := os.MkdirAll(o.DataDir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.DataDir, "hostname"), []byte(o.Hostname), 0600); err != nil {
		return err
	}
	if o.ManagementURL == "" {
		if b, readErr := os.ReadFile(filepath.Join(o.DataDir, "management-url")); readErr == nil {
			o.ManagementURL = strings.TrimSpace(string(b))
		}
	}
	st, err := openRuntimeStore(o)
	if err != nil {
		return err
	}
	defer st.Close()
	if mode != "control" && o.EnrollmentCode == "" {
		if b, readErr := os.ReadFile(filepath.Join(o.DataDir, "enrollment-code")); readErr == nil {
			o.EnrollmentCode = strings.TrimSpace(string(b))
		}
	}
	// A persistent node only needs its enrollment material during its first
	// bind. Subsequent starts reuse the identity already stored in StateDir.
	joinedMarker := filepath.Join(o.StateDir, "joined")
	alreadyJoined := false
	if _, statErr := os.Stat(joinedMarker); statErr == nil {
		alreadyJoined = true
	}
	if mode != "control" && o.EnrollmentID != "" && o.EnrollmentCode == "" && !alreadyJoined {
		return errors.New("enrollment code is required for the first start")
	}
	if err := os.MkdirAll(o.StateDir, 0700); err != nil {
		return err
	}
	key := ""
	if o.TSStore != nil {
		key = ""
	} else if _, statErr := os.Stat(filepath.Join(o.StateDir, "tailscaled.state")); statErr == nil {
		// Do not mint or persist another auth key for an already enrolled
		// persistent device. Tailscale loads the identity from StateDir.
		key = ""
	} else if mode == "control" {
		var ok bool
		key, ok, err = controlOAuthAuthKey(ctx, o)
		if !ok && err == nil {
			err = errors.New("control OAuth credentials are required")
		}
	} else {
		key, err = authKey(ctx, mode, o)
	}
	if err != nil {
		return err
	}
	s := &tsnet.Server{Hostname: o.Hostname, Dir: o.StateDir, Store: o.TSStore, AuthKey: key, Ephemeral: false}
	defer s.Close()
	if _, err := s.Up(ctx); err != nil {
		return fmt.Errorf("start tsnet: %w", err)
	}
	if err := waitTSNetReady(ctx, s, 45*time.Second); err != nil {
		return err
	}
	if mode != "control" && o.EnrollmentID != "" && !alreadyJoined {
		pub, _, identityErr := report.LoadOrCreate(filepath.Join(o.DataDir, "report-identity.json"))
		if identityErr != nil {
			return identityErr
		}
		publicKey := base64.RawStdEncoding.EncodeToString(pub)
		if err := bindSigningKey(ctx, mode, o, publicKey, tsnetHTTPClient(s)); err != nil {
			return err
		}
		_ = os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte(o.EnrollmentID), 0600)
		_ = os.Remove(filepath.Join(o.DataDir, "enrollment-code"))
	}
	if err := os.WriteFile(joinedMarker, []byte("persistent\n"), 0600); err != nil {
		return err
	}
	s.AuthKey = ""
	_ = os.Unsetenv("TS_AUTHKEY")
	if o.BootstrapOnly {
		return nil
	}
	if local, localErr := s.LocalClient(); localErr == nil {
		if current, statusErr := local.StatusWithoutPeers(ctx); statusErr == nil {
			ips := make([]string, 0, len(current.TailscaleIPs))
			for _, ip := range current.TailscaleIPs {
				ips = append(ips, ip.String())
			}
			_ = writeRuntimeStatus(o.DataDir, runtimeStatus{Mode: mode, Hostname: o.Hostname, State: current.BackendState, IPs: ips, Health: current.Health})
		}
	}
	defer os.Remove(filepath.Join(o.DataDir, "serve.pid"))
	if mode == "loadbalancer" {
		_ = os.Setenv("FUNNEL_STATE_DIR", o.StateDir)
		_ = os.Setenv("FUNNEL_HOSTNAME", o.Hostname)
		_ = os.Setenv("FUNNEL_DATA_DIR", o.DataDir)
		setEnvIfPresent("FUNNEL_MANAGEMENT_URL", o.ManagementURL)
		if o.CertEmail != "" {
			_ = os.Setenv("FUNNEL_CERT_EMAIL", o.CertEmail)
		}
		if o.ManagementURL != "" {
			if config, fetchErr := fetchLoadBalancerConfig(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.ControlKey); fetchErr == nil {
				routes := config.Routes
				if encoded, marshalErr := json.Marshal(routes); marshalErr == nil {
					_ = os.Setenv("FUNNEL_ROUTES", string(encoded))
				}
			} else {
				slog.Warn("load balancer route config unavailable; starting with no public routes", "error", fetchErr)
			}
		}
		if err := proxy.Start(mode, o.Listen, s.Dial); err != nil {
			return err
		}
		defer proxy.Stop()
		runtimeReady.Store(true)
		if o.KubernetesService != "" {
			go reportKubernetesEndpoint(ctx, s, o)
		}
		if o.ManagementURL != "" {
			go reportLoadBalancerHealth(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.LoadBalancerHostname)
			go reportLoadBalancerPeers(ctx, s, o)
		}
		if o.ManagementURL != "" {
			go func() {
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						config, fetchErr := fetchLoadBalancerConfig(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.ControlKey)
						if fetchErr != nil {
							slog.Warn("load balancer route refresh failed", "error", fetchErr)
							reportLoadBalancerHealth(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.LoadBalancerHostname)
							continue
						}
						if reloadErr := proxy.ReloadRoutes(config.Routes); reloadErr != nil {
							slog.Error("load balancer route reload failed", "error", reloadErr)
						} else {
							_ = (gateway.HTTPResolver{BaseURL: o.ManagementURL, NodeID: loadBalancerNodeID(o.DataDir), Client: tsnetHTTPClient(s)}).Acknowledge(ctx, config.Revision, nil)
						}
						reportLoadBalancerHealth(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.LoadBalancerHostname)
						go reportLoadBalancerPeers(ctx, s, o)
					}
				}
			}()
		}
		<-ctx.Done()
		return nil
	}
	if mode == "tailnet-gateway" {
		return runTailnetGateway(ctx, o, s)
	}
	if mode == "k8s-gateway" {
		controllerHTTP.Store(tsnetHTTPClient(s))
		return runKubernetesGateway(ctx, o, s)
	}
	if mode == "docker-gateway" {
		_ = os.Setenv("FUNNEL_LABEL_KEYS", strings.Join(o.LabelKeys, ","))
		_ = os.Setenv("FUNNEL_DENY_NETWORKS", strings.Join(o.DenyNetworks, ","))
		if err := runGatewaySupervisor(ctx, o, s); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	ln, err := s.Listen("tcp", o.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	control := &web.Control{BuildVersion: BuildVersion, Store: st}
	h := control.Handler()
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reconcileCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				_ = control.ReconcilePolicy(reconcileCtx)
				_, _ = control.ExpireReports(reconcileCtx, 5*time.Minute)
				_, _ = control.CleanupStaleNodes(reconcileCtx, 5*time.Minute)
				_ = control.ReconcileLoadBalancerDNS(reconcileCtx)
				if err := control.ReconcileServiceApprovals(reconcileCtx); err != nil {
					slog.Warn("service approval reconciliation failed", "error", err)
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
	localClient, err := s.LocalClient()
	if err != nil {
		return err
	}
	identityHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, err := localClient.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil {
			http.Error(w, "tailnet identity required", http.StatusForbidden)
			return
		}
		identity := ""
		if who.Node != nil {
			identity = who.Node.Name
		}
		p := web.Principal{Hostname: identity, Capabilities: map[string][]json.RawMessage{}}
		for capability, values := range who.CapMap {
			for _, value := range values {
				p.Capabilities[string(capability)] = append(p.Capabilities[string(capability)], json.RawMessage(value))
			}
		}
		if who.Node != nil {
			p.NodeID = string(who.Node.StableID)
			p.Tagged = len(who.Node.Tags) > 0
			p.Tags = append(p.Tags, who.Node.Tags...)
			for _, addr := range who.Node.Addresses {
				p.Addresses = append(p.Addresses, addr.String())
			}
		}
		if who.UserProfile != nil {
			p.UserID = fmt.Sprintf("%d", who.UserProfile.ID)
			p.Login = who.UserProfile.LoginName
			p.Groups = append(p.Groups, who.UserProfile.Groups...)
		}
		slog.Info("tailnet request identity", "remote", r.RemoteAddr, "node_id", p.NodeID, "hostname", p.Hostname, "login", p.Login, "groups", p.Groups, "tags", p.Tags)
		h.ServeHTTP(w, web.WithPrincipal(web.WithIdentity(r, identity), p))
	})
	server := &http.Server{Handler: identityHandler}
	go func() { <-ctx.Done(); _ = server.Shutdown(context.Background()) }()
	if o.BootstrapListen != "" {
		bootstrap := &http.Server{Addr: o.BootstrapListen, Handler: control.BootstrapHandler()}
		go func() {
			<-ctx.Done()
			_ = bootstrap.Shutdown(context.Background())
		}()
		go func() { _ = bootstrap.ListenAndServe() }()
		fmt.Printf("enrollment bootstrap listening on %s\n", o.BootstrapListen)
	}
	fmt.Printf("control listening on tailnet %s\n", o.Listen)
	if mode != "control" {
		return fmt.Errorf("unsupported serve mode %q", mode)
	}
	runtimeReady.Store(true)
	return server.Serve(ln)
}

func waitTSNetReady(ctx context.Context, s *tsnet.Server, timeout time.Duration) error {
	local, err := s.LocalClient()
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		status, statusErr := local.StatusWithoutPeers(ctx)
		if statusErr == nil && status.BackendState == "Running" {
			return nil
		}
		lastErr = statusErr
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return fmt.Errorf("tsnet did not become ready: %w", lastErr)
			}
			return fmt.Errorf("tsnet did not become ready")
		case <-ticker.C:
		}
	}
}

func notifyNodeShutdown(ctx context.Context, client *http.Client, managementURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(managementURL, "/")+"/api/node/shutdown", strings.NewReader(`{}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("shutdown cleanup: HTTP %d", res.StatusCode)
	}
	return nil
}

type loadBalancerConfig = nodeconfig.Configuration

func fetchLoadBalancerConfig(ctx context.Context, client *http.Client, managementURL, nodeID, controlKey string) (loadBalancerConfig, error) {
	var payload loadBalancerConfig
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(managementURL, "/")+"/api/node/config", nil)
	if err != nil {
		return payload, err
	}
	res, err := client.Do(req)
	if err != nil {
		return payload, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return payload, fmt.Errorf("management route config: HTTP %d", res.StatusCode)
	}
	var envelope report.Envelope
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return payload, err
	}
	return nodeconfig.Verify(envelope, controlKey, nodeID, time.Now())
}

func reportLoadBalancerPeers(ctx context.Context, s *tsnet.Server, o Options) {
	nodeID := loadBalancerNodeID(o.DataDir)
	if nodeID == "" || o.ManagementURL == "" {
		return
	}
	client := tsnetHTTPClient(s)
	agent := report.Agent{NodeID: nodeID, ManagementURL: o.ManagementURL, DataDir: o.DataDir, HTTPClient: client}
	config, err := fetchLoadBalancerConfig(ctx, client, o.ManagementURL, nodeID, o.ControlKey)
	if err != nil {
		slog.Warn("load balancer signed config failed", "error", err)
		return
	}
	var exposures []report.Exposure
	for _, app := range config.Applications {
		if !app.Enabled {
			continue
		}
		target := net.JoinHostPort(app.ServiceFQDN, strconv.Itoa(int(app.ServicePort)))
		var targetErr error
		reachable := false
		backendIP, backendName := "", app.ServiceName
		if targetErr == nil {
			backendIP, _, _ = net.SplitHostPort(target)
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			conn, dialErr := s.Dial(probeCtx, "tcp", target)
			cancel()
			if conn != nil {
				_ = conn.Close()
			}
			reachable = dialErr == nil
			if len(app.HTTPRules) > 0 && len(app.HTTPBackends) == 0 {
				reachable = true
			}
			if dialErr != nil {
				slog.Warn("application Service unavailable", "application", app.ID, "error", dialErr)
			}
		}
		for _, host := range app.PublicHostnames {
			exposures = append(exposures, loadBalancerExposure(app, host, backendName, backendIP, reachable))
		}
	}
	if err = agent.Send(ctx, exposures); err != nil {
		slog.Warn("load balancer exposure report failed", "error", err)
	}
}

// Every source is reached through an application Service, including tailnet backends.
func loadBalancerExposure(app store.ApplicationRecord, host, backendName, backendIP string, reachable bool) report.Exposure {
	return report.Exposure{ApplicationID: app.ID, ServiceName: app.ServiceName, PublicHostname: host, BackendHostname: backendName, BackendIP: backendIP, Port: app.BackendPort, Protocol: "http", Reachable: reachable}
}

func loadBalancerNodeID(dataDir string) string {
	b, _ := os.ReadFile(filepath.Join(dataDir, "node-id"))
	return strings.TrimSpace(string(b))
}

func reportLoadBalancerHealth(ctx context.Context, client *http.Client, managementURL, nodeID, hostname string) {
	if nodeID == "" || managementURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"mode": "loadbalancer", "ready": true,
		"loadbalancer_hostname": hostname, "reported_at": time.Now().Unix(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(managementURL, "/")+"/api/node/health", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		slog.Warn("load balancer health report failed", "error", err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		slog.Warn("load balancer health report rejected", "status", res.StatusCode)
	}
}

func runGatewaySupervisor(ctx context.Context, o Options, s *tsnet.Server) error {
	var client *dockerclient.Client
	var err error
	client, err = dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	managementClient := tsnetHTTPClient(s)
	nodeIDBytes, _ := os.ReadFile(filepath.Join(o.DataDir, "node-id"))
	nodeID := strings.TrimSpace(string(nodeIDBytes))
	var configRevision string
	resolver := gateway.HTTPResolver{BaseURL: o.ManagementURL, NodeID: nodeID, ControlKey: o.ControlKey, Client: managementClient, Revision: &configRevision}
	listenerManager := gateway.NewListenerManager(s)
	go func() { <-ctx.Done(); listenerManager.Close() }()
	supervisor := gateway.Config{Docker: client, NodeID: nodeID, RelayImage: "ghcr.io/tailscale-x/tailscale-private-funnel:" + BuildVersion, LabelKeys: o.LabelKeys, DenyNetworks: o.DenyNetworks, Resolver: resolver, ListenerManager: listenerManager, DeferListenerCleanup: true}
	var reconcileMu sync.Mutex
	var lastDocker []gateway.Runtime
	var lastDockerAt time.Time
	reconcile := func() {
		reconcileMu.Lock()
		defer reconcileMu.Unlock()
		var runtimes []gateway.Runtime
		if client != nil {
			var err error
			runtimes, err = supervisor.Reconcile(ctx)
			if err != nil {
				slog.Error("Docker discovery failed", "error", err)
				if time.Since(lastDockerAt) < 5*time.Minute {
					runtimes = lastDocker
				} else {
					runtimes = nil
				}
			}
			if err == nil {
				lastDocker = runtimes
				lastDockerAt = time.Now()
			}
		}
		config, err := resolver.Configuration(ctx)
		if err != nil {
			slog.Warn("gateway configuration failed", "error", err)
			return
		}
		configRevision = config.Revision
		session := gateway.Session{Resolver: resolver, Listeners: listenerManager, Agent: report.Agent{NodeID: nodeID, ManagementURL: o.ManagementURL, DataDir: o.DataDir, HTTPClient: managementClient}}
		if err := session.Publish(ctx, config, runtimes, nil); err != nil {
			slog.Warn("gateway publish failed", "error", err)
		}
	}
	// Docker discovery and Service listeners share the process-owned tsnet
	// identity. The supervisor is the only Docker API client.
	reconcile()
	if client != nil {
		go func() {
			messages, errs := client.Events(ctx, events.ListOptions{Filters: filters.NewArgs(filters.Arg("type", "container"))})
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-messages:
					if !ok {
						return
					}
					reconcile()
				case eventErr, ok := <-errs:
					if !ok {
						return
					}
					if eventErr != nil && !errors.Is(eventErr, context.Canceled) {
						slog.Warn("Docker event stream ended; polling remains active", "error", eventErr)
					}
					return
				}
			}
		}()
	}
	go func() {
		if client != nil {
			defer client.Close()
		}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcile()
			}
		}
	}()
	<-ctx.Done()
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	supervisor.Cleanup(cleanup)

	return nil
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func setEnvIfPresent(name, value string) {
	if strings.TrimSpace(value) != "" {
		_ = os.Setenv(name, value)
	}
}
func Status(mode string, o Options) error {
	explicitHostname := strings.TrimSpace(o.Hostname)
	o, err := defaults(mode, o)
	if err != nil {
		return err
	}
	if o.ConfigFile != "" {
		o, err = loadConfig(mode, o)
		if err != nil {
			return err
		}
		o, err = defaults(mode, o)
		if err != nil {
			return err
		}
	}
	if explicitHostname == "" {
		if b, readErr := os.ReadFile(filepath.Join(o.DataDir, "hostname")); readErr == nil && strings.TrimSpace(string(b)) != "" {
			o.Hostname = strings.TrimSpace(string(b))
		}
	}
	if current, ok := readRuntimeStatus(o.DataDir); ok {
		fmt.Printf("mode: %s\nhostname: %s\nstate: %s\nips: %v\nstate_dir: %s\ndata_dir: %s\n", current.Mode, current.Hostname, current.State, current.IPs, o.StateDir, o.DataDir)
		printStoredNode(o.DataDir)
		if store.IsGateway(mode) {
			if b, err := os.ReadFile(filepath.Join(o.DataDir, "gateway-status.json")); err == nil {
				fmt.Printf("gateway: %s\n", b)
			}
		}
		if len(current.Health) > 0 {
			fmt.Printf("health: %s\n", strings.Join(current.Health, "; "))
		} else {
			fmt.Println("health: ok")
		}
		return nil
	}
	fmt.Printf("mode: %s\nhostname: %s\nstate: stopped\nstate_dir: %s\ndata_dir: %s\n", mode, o.Hostname, o.StateDir, o.DataDir)
	if _, stateErr := os.Stat(filepath.Join(o.StateDir, "tailscaled.state")); stateErr == nil {
		fmt.Println("identity: persistent (saved Tailscale state)")
	}
	printStoredNode(o.DataDir)
	return nil
}

func printStoredNode(dataDir string) {
	nodeIDBytes, readErr := os.ReadFile(filepath.Join(dataDir, "node-id"))
	if readErr != nil {
		return
	}
	fmt.Printf("node_id: %s\n", strings.TrimSpace(string(nodeIDBytes)))
	st, storeErr := store.Open(dataDir)
	if storeErr != nil {
		return
	}
	defer st.Close()
	if node, nodeErr := st.Node(strings.TrimSpace(string(nodeIDBytes))); nodeErr == nil {
		fmt.Printf("node_id: %s\nnode_mode: %s\nnode_hostname: %s\nlast_report: %d\nrevoked: %t\n", node.ID, node.Mode, node.Hostname, node.LastReport, node.Revoked)
	}
}
func Reset(mode string, o Options) error {
	o, err := defaults(mode, o)
	if err != nil {
		return err
	}
	if !o.Force {
		return errors.New("reset requires --force")
	}
	if o.ConfigFile != "" || mode != "control" {
		if loaded, loadErr := loadConfig(mode, o); loadErr == nil {
			o = loaded
		}
	}
	if strings.TrimSpace(o.ManagementURL) != "" {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s := &tsnet.Server{Hostname: o.Hostname, Dir: o.StateDir, Ephemeral: false}
		if _, upErr := s.Up(cleanupCtx); upErr == nil {
			if notifyErr := notifyNodeShutdown(cleanupCtx, s.HTTPClient(), o.ManagementURL); notifyErr != nil {
				slog.Warn("control cleanup during reset failed; local state will still be removed", "error", notifyErr)
			}
		}
		_ = s.Close()
	}
	if err := os.RemoveAll(o.StateDir); err != nil {
		return err
	}
	if err := os.RemoveAll(o.DataDir); err != nil {
		return err
	}
	fmt.Printf("removed local %s state and data; revoke the node or enrollment in the control UI if it is still registered\n", mode)
	return nil
}
