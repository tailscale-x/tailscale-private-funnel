package ingress

import (
	"context"
	"net"
	"testing"
)

func TestParseLabel(t *testing.T) {
	got, err := ParseLabel(" 8080:Checkout.dev.example.com:http, 8443:admin.dev.example.com:http ")
	if err != nil || len(got) != 2 || got[0].Hostname != "checkout.dev.example.com" || got[1].Port != 8443 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestParseLabelRejectsGenericProtocolsAndConflicts(t *testing.T) {
	for _, input := range []string{"8080:app.example:tcp", "8080:app.example:http,8080:app.example:http", "8080:app.example"} {
		if _, err := ParseLabel(input); err == nil {
			t.Fatalf("accepted invalid label %q", input)
		}
	}
}

func TestSelectNetworks(t *testing.T) {
	got := SelectNetworks([]Network{{ID: "1", Name: "host", UserDefined: false}, {ID: "2", Name: "app-net", UserDefined: true}, {ID: "3", Name: "secret", UserDefined: true}}, []string{"secret"})
	if len(got) != 1 || got[0].Name != "app-net" {
		t.Fatalf("selected=%#v", got)
	}
}

func TestForward(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		c, e := backend.Accept()
		if e == nil {
			defer c.Close()
			_, _ = c.Write([]byte("ok"))
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Forward(ctx, listener, backend.Addr().String()) }()
	c, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	buf := make([]byte, 2)
	if _, err := c.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("read=%q err=%v", buf, err)
	}
}

func TestStableSidecarIDSurvivesContainerReplacement(t *testing.T) {
	spec := Spec{Port: 8080, Hostname: "checkout.dev.example.com", Protocol: "http"}
	first := StableSidecarID("svc:checkout", spec)
	second := StableSidecarID("svc:checkout", spec)
	if first == "" || first != second {
		t.Fatalf("stable sidecar id changed: %q %q", first, second)
	}
	if first == StableSidecarID("svc:other", spec) {
		t.Fatal("different Services share a sidecar identity")
	}
	if first == StableSidecarID("svc:checkout", Spec{Port: 8443, Hostname: spec.Hostname, Protocol: spec.Protocol}) {
		t.Fatal("different backend ports share a sidecar identity")
	}
	if first != StableSidecarID("svc:checkout", Spec{Port: 8080, Hostname: "checkout-admin.dev.example.com", Protocol: spec.Protocol}) {
		t.Fatal("aliases for one Service and port did not share a sidecar identity")
	}
}

func TestValidateNoOverlap(t *testing.T) {
	if err := ValidateNoOverlap([]Network{{Name: "app", CIDR: "10.0.0.0/24"}, {Name: "shared", CIDR: "10.0.0.0/25"}}); err == nil {
		t.Fatal("overlapping Docker networks were accepted")
	}
	if err := ValidateNoOverlap([]Network{{Name: "app", CIDR: "10.0.0.0/24"}, {Name: "shared", CIDR: "10.0.1.0/24"}}); err != nil {
		t.Fatal(err)
	}
}
