package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/libdns/libdns"
)

// ProxyChange is separate from RR identity: changing proxy mode must never
// delete/recreate a DNS record or transfer its ownership.
type ProxyChange struct {
	ZoneID, ID string
	Record     libdns.Record
	Proxied    bool
}
type ProxyProvider interface {
	ProxyChanges(context.Context, string, []libdns.Record, bool) ([]ProxyChange, error)
	ApplyProxyChanges(context.Context, []ProxyChange) error
}
type cloudflareProxyRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (r cloudflareProxyRecord) record() libdns.Record {
	return libdns.RR{Name: r.Name, Type: r.Type, Data: r.Content}
}

func (p *cloudflareProvider) proxyRequest(ctx context.Context, method, path string, body any, out any) error {
	base := p.apiBase
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	token := p.p.APIToken
	if strings.Contains(path, "/dns_records") && p.p.ZoneToken != "" {
		token = p.p.ZoneToken
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("cloudflare %s: HTTP %d", method, res.StatusCode)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.Success {
		return fmt.Errorf("cloudflare %s request failed", method)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

// Caller must validate the exact RR ownership before planning proxy changes.
func (p *cloudflareProvider) ProxyChanges(ctx context.Context, zone string, records []libdns.Record, proxied bool) ([]ProxyChange, error) {
	if len(records) == 0 {
		return nil, nil
	}
	var zones []struct {
		ID string `json:"id"`
	}
	if err := p.proxyRequest(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(strings.TrimSuffix(zone, "."))+"&per_page=2", nil, &zones); err != nil {
		return nil, err
	}
	if len(zones) != 1 || zones[0].ID == "" {
		return nil, fmt.Errorf("cloudflare zone missing or ambiguous")
	}
	var changes []ProxyChange
	for _, record := range records {
		rr := record.RR()
		if rr.Type != "A" && rr.Type != "AAAA" && rr.Type != "CNAME" {
			continue
		}
		// Collect all pages before PATCH; exact data matching excludes siblings.
		for page := 1; ; page++ {
			var current []cloudflareProxyRecord
			path := "/zones/" + url.PathEscape(zones[0].ID) + "/dns_records?name=" + url.QueryEscape(rr.Name) + "&type=" + url.QueryEscape(rr.Type) + "&per_page=100&page=" + fmt.Sprint(page)
			if err := p.proxyRequest(ctx, http.MethodGet, path, nil, &current); err != nil {
				return nil, err
			}
			for _, r := range current {
				if RecordKey(r.record()) == RecordKey(record) && r.Proxied != proxied {
					if r.ID == "" {
						return nil, fmt.Errorf("cloudflare returned missing record ID")
					}
					changes = append(changes, ProxyChange{ZoneID: zones[0].ID, ID: r.ID, Record: record, Proxied: proxied})
				}
			}
			if len(current) < 100 {
				break
			}
		}
	}
	return changes, nil
}
func (p *cloudflareProvider) ApplyProxyChanges(ctx context.Context, changes []ProxyChange) error {
	for _, change := range changes {
		path := "/zones/" + url.PathEscape(change.ZoneID) + "/dns_records/" + url.PathEscape(change.ID)
		var current cloudflareProxyRecord
		if err := p.proxyRequest(ctx, http.MethodGet, path, nil, &current); err != nil {
			return err
		}
		if current.ID != change.ID || RecordKey(current.record()) != RecordKey(change.Record) {
			return fmt.Errorf("cloudflare record changed; retry DNS reconciliation")
		}
		if current.Proxied == change.Proxied {
			continue
		}
		ttl := int(change.Record.RR().TTL.Seconds())
		if ttl < 60 {
			ttl = 60
		}
		if change.Proxied {
			ttl = 1
		}
		var updated cloudflareProxyRecord
		if err := p.proxyRequest(ctx, http.MethodPatch, path, map[string]any{"proxied": change.Proxied, "ttl": ttl}, &updated); err != nil {
			return err
		}
		if updated.Proxied != change.Proxied || RecordKey(updated.record()) != RecordKey(change.Record) {
			return fmt.Errorf("cloudflare proxy update not confirmed")
		}
	}
	return nil
}
