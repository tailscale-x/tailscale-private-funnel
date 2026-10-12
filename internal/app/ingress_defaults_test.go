package app

import (
	"os"
	"strings"
	"testing"
)

func TestIngressHostnameDefault(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	got, err := defaults("ingress", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.SplitN(host, ".", 2)[0] + "-ingress"; got.Hostname != want {
		t.Fatalf("got %q, want %q", got.Hostname, want)
	}
	explicit, err := defaults("ingress", Options{Hostname: "custom-ingress"})
	if err != nil || explicit.Hostname != "custom-ingress" {
		t.Fatal(explicit.Hostname, err)
	}
}
