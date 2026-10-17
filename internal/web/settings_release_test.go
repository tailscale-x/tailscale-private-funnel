package web

import (
	"context"
	"testing"
)

func TestReleaseDefaultsAcceptOnlyRunningDevelopmentBuild(t *testing.T) {
	c := &Control{BuildVersion: "dev-operator-test"}
	if err := c.checkRelease(context.Background(), c.BuildVersion); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"dev-other", "latest", "dev-operator-test;echo bad"} {
		if err := c.checkRelease(context.Background(), version); err == nil {
			t.Fatalf("accepted unverified version %q", version)
		}
	}
	checked := false
	c.ReleaseCheck = func(context.Context, string) error { checked = true; return nil }
	if err := c.checkRelease(context.Background(), "v3.0.0"); err != nil || !checked {
		t.Fatalf("stable release verification skipped: %v", err)
	}
}
