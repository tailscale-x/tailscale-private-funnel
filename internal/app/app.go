package app

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"

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
		return errors.New("control creation requires protected OAuth credential files")
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
	if client == nil {
		return errors.New("process-owned Tailscale HTTP client required")
	}
	base := o.ManagementURL
	key, e := base64.RawStdEncoding.DecodeString(publicKey)
	if e != nil {
		return e
	}
	_, e = funnelv1connect.NewEnrollmentServiceClient(client, base).BindSigningKey(ctx, connect.NewRequest(&v1.BindSigningKeyRequest{EnrollmentId: o.EnrollmentID, Code: o.EnrollmentCode, PublicKey: key}))
	return e
}

func tsnetHTTPClient(s *tsnet.Server) *http.Client {
	client := s.HTTPClient()
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
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
	if len(o.Administrators) > 0 {
		var existing map[string]any
		if e := st.GetJSON("access_roles", &existing); errors.Is(e, sql.ErrNoRows) {
			selectors := append([]string{"autogroup:owner"}, o.Administrators...)
			if e = st.SetJSON("access_roles", map[string]any{"roles": map[string][]string{"admin": selectors}}); e != nil {
				return "", true, e
			}
		} else if e != nil {
			return "", true, e
		}
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
	Ready    bool     `json:"ready"`
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
	if mode == "control" {
		ip4, ip6 := s.TailscaleIPs()
		address := ip4.String()
		if !ip4.IsValid() {
			address = ip6.String()
		}
		if err := initializeControlManagement(st, address, o.Listen, BuildVersion); err != nil {
			return err
		}
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
		markRuntimeReady(o.DataDir)
		runtimeReady.Store(true)
		if o.KubernetesService != "" {
			go reportKubernetesEndpoint(ctx, s, o)
		}
		if o.ManagementURL != "" {
			go reportLoadBalancerHealth(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.LoadBalancerHostname, o.DataDir)
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
							reportLoadBalancerHealth(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.LoadBalancerHostname, o.DataDir)
							continue
						}
						if reloadErr := proxy.ReloadRoutes(config.Routes); reloadErr != nil {
							slog.Error("load balancer route reload failed", "error", reloadErr)
						} else {
							_ = (gateway.RPCResolver{BaseURL: o.ManagementURL, NodeID: loadBalancerNodeID(o.DataDir), Client: tsnetHTTPClient(s)}).Acknowledge(ctx, config.Revision, nil)
						}
						reportLoadBalancerHealth(ctx, tsnetHTTPClient(s), o.ManagementURL, loadBalancerNodeID(o.DataDir), o.LoadBalancerHostname, o.DataDir)
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
	fmt.Printf("control listening on tailnet %s\n", o.Listen)
	if mode != "control" {
		return fmt.Errorf("unsupported serve mode %q", mode)
	}
	markRuntimeReady(o.DataDir)
	runtimeReady.Store(true)
	err = server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
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

type loadBalancerConfig = nodeconfig.Configuration

func fetchLoadBalancerConfig(ctx context.Context, client *http.Client, managementURL, nodeID, controlKey string) (loadBalancerConfig, error) {
	return (gateway.RPCResolver{Client: client, BaseURL: managementURL, NodeID: nodeID, ControlKey: controlKey}).Configuration(ctx)
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

func reportLoadBalancerHealth(ctx context.Context, client *http.Client, managementURL, nodeID, hostname, dataDir string) {
	if nodeID == "" || managementURL == "" {
		return
	}
	_, e := funnelv1connect.NewNodeServiceClient(client, managementURL).ReportHealth(ctx, connect.NewRequest(&v1.NodeHealth{LoadbalancerHostname: hostname, Online: true, LastReport: time.Now().Unix()}))
	if e != nil {
		slog.Warn("load balancer health report failed", "error", e)
	} else {
		(report.Agent{DataDir: dataDir}).RecordContact()
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
	resolver := gateway.RPCResolver{BaseURL: o.ManagementURL, NodeID: nodeID, ControlKey: o.ControlKey, Client: managementClient, Revision: &configRevision}
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
func markRuntimeReady(dir string) {
	b, e := os.ReadFile(filepath.Join(dir, "runtime-status.json"))
	if e != nil {
		return
	}
	var status runtimeStatus
	if json.Unmarshal(b, &status) == nil {
		status.Ready = true
		_ = writeRuntimeStatus(dir, status)
	}
}

// Bootstrap leaves a usable canonical URL without requiring a manual settings
// mutation before the first gateway can enroll. Explicit administrator values
// survive restarts and version changes.
func initializeControlManagement(st *store.Store, address, listen, version string) error {
	settings := map[string]any{}
	if err := st.GetJSON("tailnet", &settings); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current, _ := settings["management_url"].(string); strings.TrimSpace(current) == "" {
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			return fmt.Errorf("control listener: %w", err)
		}
		settings["management_url"] = "http://" + net.JoinHostPort(address, port)
	}
	if current, _ := settings["release_version"].(string); current == "" && version != "" && version != "dev" {
		settings["release_version"] = version
	}
	return st.SetJSON("tailnet", settings)
}
