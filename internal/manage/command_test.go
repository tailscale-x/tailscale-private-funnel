package manage

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
)

type fixtureRPC struct {
	funnelv1connect.UnimplementedManagementServiceHandler
}

func (fixtureRPC) ListProviders(context.Context, *connect.Request[v1.Empty]) (*connect.Response[v1.Providers], error) {
	return connect.NewResponse(&v1.Providers{Items: []*v1.Provider{{Id: "opaque-id", Name: "Demo", Vendor: "cloudflare", Enabled: true}}}), nil
}
func TestDescriptorCLIInvokesGeneratedRPCContract(t *testing.T) {
	path, handler := funnelv1connect.NewManagementServiceHandler(fixtureRPC{})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	cmd := &cobra.Command{Use: "control"}
	var output bytes.Buffer
	cmd.SetOut(&output)
	Attach(cmd, &Options{Client: server.Client()})
	cmd.SetArgs([]string{"providers", "list", "--control-url", server.URL, "--output", "json"})
	if e := cmd.Execute(); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(output.String(), `"opaque-id"`) || !strings.Contains(output.String(), `"cloudflare"`) {
		t.Fatal("missing typed response")
	}
}
func TestSecretFieldsAreNotFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "control"}
	Attach(cmd, nil)
	sub, _, e := cmd.Find([]string{"settings", "oauth", "save"})
	if e != nil {
		t.Fatal(e)
	}
	if sub.Flags().Lookup("client-secret") != nil || sub.Flags().Lookup("client-id") != nil {
		t.Fatal("secret-valued flag exposed")
	}
	if sub.Flags().Lookup("file") == nil {
		t.Fatal("no protected input path")
	}
}
func TestDestructiveCommandNeedsExplicitConfirmation(t *testing.T) {
	cmd := &cobra.Command{Use: "control"}
	Attach(cmd, nil)
	cmd.SetArgs([]string{"providers", "delete", "--id", "opaque"})
	if e := cmd.Execute(); e == nil || !strings.Contains(e.Error(), "--yes") {
		t.Fatalf("confirmation=%v", e)
	}
}
func TestURLRejectsCredentialsAndImplicitTargets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, raw := range []string{"", "https://user:secret@control", "file:///tmp/control", "http://control/a", "http://control?token=secret"} {
		if _, e := ResolveURL(raw); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
func TestStableExitCodes(t *testing.T) {
	for code, want := range map[connect.Code]int{connect.CodePermissionDenied: 3, connect.CodeUnavailable: 4, connect.CodeAborted: 5, connect.CodeDeadlineExceeded: 6, connect.CodeInvalidArgument: 2, connect.CodeInternal: 1} {
		if got := ExitCode(connect.NewError(code, context.Canceled)); got != want {
			t.Fatalf("%v: %d", code, got)
		}
	}
}
