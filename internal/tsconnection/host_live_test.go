package tsconnection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	tsapi "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// This opt-in test borrows an isolated, already-connected fixture daemon. It
// never changes the machine's real Tailscale daemon or any public DNS records.
func TestLiveHostLoopbackStreamingAndCleanup(t *testing.T) {
	if os.Getenv("FUNNEL_LIVE_HOST_TEST") != "1" {
		t.Skip("requires protected issuer/services files and outbound Tailscale access")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	home, _ := os.UserHomeDir()
	credentials := func(purpose string) tsapi.Client {
		read := func(name string) string {
			p := filepath.Join(home, ".config/tailscale-private-funnel/credentials/main-"+purpose, name)
			info, err := os.Stat(p)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("protected credential file required", p)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			return strings.TrimSpace(string(b))
		}
		return tsapi.Client{ClientID: read("client-id"), ClientSecret: read("client-secret"), Tailnet: "-"}
	}
	issuer, services := credentials("issuer"), credentials("services")
	unique := fmt.Sprintf("tunnel-qa-%d", time.Now().Unix())
	svc := "svc:tailscale-private-funnel-" + unique
	_, err := services.CreateService(ctx, tsapi.Service{Name: svc, Ports: []string{"tcp:8080"}, Tags: []string{identity.ServiceTag("main", false)}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if err := services.DeleteService(c, svc); err != nil {
			t.Error("fixture Service cleanup", err)
		}
	}()
	key := func(role identity.Role) string {
		k, e := issuer.CreateWorkloadAuthKey(ctx, string(role), unique, []string{identity.Tag("main", role)}, true)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "tailscaled.sock")
	daemon := exec.CommandContext(ctx, "tailscaled", "--tun=userspace-networking", "--state=mem:", "--socket="+socket, "--statedir="+dir, "--port=0")
	daemon.Stdout = io.Discard
	daemon.Stderr = io.Discard
	if err = daemon.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = daemon.Process.Kill(); _ = daemon.Wait() }()
	for i := 0; i < 100; i++ {
		if _, e := os.Stat(socket); e == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	join := exec.CommandContext(ctx, "tailscale", "--socket="+socket, "up", "--auth-key=file:/dev/stdin", "--hostname="+unique, "--accept-dns=false")
	join.Stdin = strings.NewReader(key(identity.TailnetGateway))
	join.Stdout = io.Discard
	join.Stderr = io.Discard
	if err = join.Run(); err != nil {
		t.Fatal("isolated fixture join", err)
	}
	host := NewHost(socket, filepath.Join(dir, "component"))
	status, err := host.Up(ctx)
	if err != nil {
		t.Fatal(err)
	}
	device := status.Self.ID
	lockDir := filepath.Join("/var/tmp", fmt.Sprintf("tailscale-private-funnel-host-%x", sha256.Sum256([]byte(string(device)))))
	t.Cleanup(func() { _ = os.RemoveAll(lockDir) })
	defer func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = host.Client.Logout(c)
	}()
	// This pre-existing entry is unrelated to the component's application Service.
	original := &ipn.ServeConfig{TCP: map[uint16]*ipn.TCPPortHandler{19001: {TCPForward: "127.0.0.1:19002"}}}
	if err = host.Client.SetServeConfig(ctx, original); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("stream-data\n"), 100000)
	payload = append(payload, []byte("[DONE]\n")...)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if e != nil {
				return
			}
			defer c.CloseNow()
			typ, data, e := c.Read(r.Context())
			if e == nil {
				_ = c.Write(r.Context(), typ, data)
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(payload)
	}))
	defer backend.Close()
	if err = host.ForwardService(ctx, svc, 8080, strings.TrimPrefix(backend.URL, "http://")); err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	lb := &tsnet.Server{Dir: filepath.Join(dir, "lb"), Hostname: unique + "-lb", AuthKey: key(identity.LoadBalancer), Ephemeral: true, Logf: func(string, ...any) {}}
	defer lb.Close()
	if _, err = lb.Up(ctx); err != nil {
		t.Fatal(err)
	}
	lb.AuthKey = ""
	defer func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if lc, e := lb.LocalClient(); e == nil {
			_ = lc.Logout(c)
		}
	}()
	fqdn := strings.TrimPrefix(svc, "svc:") + "." + status.CurrentTailnet.MagicDNSSuffix
	httpClient := lb.HTTPClient()
	httpClient.Timeout = 15 * time.Second
	var received []byte
	for ctx.Err() == nil {
		response, e := httpClient.Get("http://" + fqdn + ":8080/stream")
		if e == nil {
			received, e = io.ReadAll(response.Body)
			response.Body.Close()
			if e == nil && bytes.Equal(received, payload) {
				break
			}
		}
		time.Sleep(time.Second)
	}
	if !bytes.Equal(received, payload) {
		t.Fatal("incomplete loopback Service stream", len(received), ctx.Err())
	}
	ws, _, err := websocket.Dial(ctx, "ws://"+fqdn+":8080/ws", &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if err = ws.Write(ctx, websocket.MessageText, []byte("loopback-websocket")); err != nil {
		t.Fatal(err)
	}
	_, message, err := ws.Read(ctx)
	ws.CloseNow()
	if err != nil || string(message) != "loopback-websocket" {
		t.Fatal("WebSocket", err)
	}
	if err = host.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := host.Client.GetServeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := host.Up(ctx)
	if err != nil || after.Self.ID != device || !reflect.DeepEqual(current.TCP, original.TCP) || len(current.Services) != 0 {
		t.Fatal("borrowed identity or unrelated Serve entry changed", err)
	}
	t.Log("Full 1.2 MB SSE + DONE, WebSocket, same device and unrelated Serve entry preserved; owned Service removed")
}
