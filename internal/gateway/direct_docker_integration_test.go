package gateway

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	docker "github.com/docker/docker/client"
)

// This opt-in fixture owns only its uniquely named container and network. The
// production supervisor receives a real Docker client and must never mutate it.
func TestDirectDockerStreamingAndReplacement(t *testing.T) {
	if os.Getenv("FUNNEL_DOCKER_INTEGRATION") != "1" {
		t.Skip("set FUNNEL_DOCKER_INTEGRATION=1 for a disposable real-Docker fixture")
	}
	run := func(args ...string) string {
		t.Helper()
		b, e := exec.CommandContext(t.Context(), "docker", args...).CombinedOutput()
		if e != nil {
			t.Fatalf("docker %s: %s: %v", args[0], b, e)
		}
		return strings.TrimSpace(string(b))
	}
	name := fmt.Sprintf("funnel-role-direct-%d", time.Now().UnixNano())
	run("network", "create", name)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", name).Run() })
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	code := `require('http').createServer((req,res)=>{res.writeHead(200,{'content-type':'text/event-stream'});let n=0;const id=setInterval(()=>{if(n++===128){clearInterval(id);res.end('data: [DONE]\n\n')}else res.write('data: '+n+' '+('x'.repeat(1024))+'\n\n')},1)}).listen(8080,'0.0.0.0')`
	start := func() {
		run("run", "-d", "--name", name, "--network", name, "--label", ExposureLabel+"=8080:direct.example.com:http", "node:24.18.0-alpine", "node", "-e", code)
	}
	start()
	client, e := docker.NewClientWithOpts(docker.FromEnv, docker.WithAPIVersionNegotiation())
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	conn := &directConnection{}
	listeners := NewListenerManager(conn)
	defer listeners.Close()
	config := Config{Docker: client, ListenerManager: listeners, Resolver: StaticResolver{"direct.example.com": {ApplicationID: "direct", ServiceName: "svc:direct", ServicePort: 8080, Enabled: true}}}
	var previous string
	for generation := 0; generation < 2; generation++ {
		var current Runtime
		deadline := time.Now().Add(15 * time.Second)
		for {
			states, e := config.Reconcile(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			for _, s := range states {
				if s.Spec.Hostname == "direct.example.com" {
					current = s
				}
			}
			if current.Reachable {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("backend never reachable", current)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if previous == current.ContainerID {
			t.Fatal("replacement was not inspected")
		}
		previous = current.ContainerID
		if current.BackendIP == "" || current.BackendIP == "127.0.0.1" {
			t.Fatal("not direct Docker IP", current)
		}
		resp, e := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + conn.listener.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(resp.Body)
		resp.Body.Close()
		if e != nil || strings.Count(string(b), "data:") != 129 || !strings.HasSuffix(string(b), "data: [DONE]\n\n") {
			t.Fatalf("truncated generation %d: %d bytes: %v", generation, len(b), e)
		}
		if ports := run("inspect", "--format", "{{json .HostConfig.PortBindings}}", name); ports != "{}" && ports != "null" {
			t.Fatal("published backend port", ports)
		}
		if generation == 0 {
			run("rm", "-f", name)
			start()
		}
	}
}
