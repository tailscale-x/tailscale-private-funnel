package web

import "testing"

func TestOAuthClientCacheIsBoundToCredentials(t *testing.T) {
	c := &Control{}
	a := c.tsClient("id", "secret", "tailnet")
	b := c.tsClient("id", "secret", "tailnet")
	rotated := c.tsClient("id", "new-secret", "tailnet")
	if a.HTTP != b.HTTP {
		t.Fatal("reconstructed OAuth client for every API call")
	}
	if a.HTTP == rotated.HTTP {
		t.Fatal("credential rotation reused OAuth client")
	}
}
