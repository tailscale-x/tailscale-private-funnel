package app

import (
	"os"
	"strings"
	"testing"
)

func TestGatewayHostnameDefault(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaults("docker-gateway", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.SplitN(host, ".", 2)[0] + "-docker-gateway"; got.Hostname != want {
		t.Fatalf("got %q, want %q", got.Hostname, want)
	}
	explicit, err := defaults("docker-gateway", Options{Hostname: "custom-gateway"})
	if err != nil || explicit.Hostname != "custom-gateway" {
		t.Fatal(explicit.Hostname, err)
	}
}
