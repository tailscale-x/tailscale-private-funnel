package kubebroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	URL, TokenFile string
	HTTP           *http.Client
}
type Session struct {
	NodeUID, NodeName, NodeID, AuthKey, ManagementURL, ControlKey, ControllerID string
	Identity                                                                    []byte
	State                                                                       map[string][]byte
}

func NewClient(url, tokenFile, caFile string) (*Client, error) {
	b, e := os.ReadFile(caFile)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, fmt.Errorf("invalid broker CA")
	}
	return &Client{URL: strings.TrimRight(url, "/"), TokenFile: tokenFile, HTTP: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}}}, nil
}
func (c *Client) Call(ctx context.Context, path string, in, out any) error {
	b, e := json.Marshal(in)
	if e != nil {
		return e
	}
	token, e := os.ReadFile(c.TokenFile)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.URL+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	res, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("broker HTTP %d: %s", res.StatusCode, body)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
	}
	return nil
}
