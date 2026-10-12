package dns

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/libdns/cloudflare"
	"github.com/libdns/godaddy"
	"github.com/libdns/route53"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoDaddyDiscoveryPaginationAndPermission(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "sso-key secret" {
			t.Error("missing authentication")
		}
		switch r.URL.Query().Get("marker") {
		case "":
			w.Write([]byte(`[{"domain":"example.com"}]`))
		case "example.com":
			w.Write([]byte(`[{"domain":"example.net"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	p := &godaddyProvider{p: godaddy.Provider{APIToken: "secret"}, apiBase: server.URL}
	zones, err := p.ListZones(context.Background())
	if err != nil || len(zones) != 2 || calls != 3 {
		t.Fatal(zones, err, calls)
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer denied.Close()
	p.apiBase = denied.URL
	if _, err = p.ListZones(context.Background()); err == nil {
		t.Fatal("permission failure accepted")
	}
	cycle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[{"domain":"example.com"}]`)) }))
	defer cycle.Close()
	p.apiBase = cycle.URL
	if _, err = p.ListZones(context.Background()); err == nil {
		t.Fatal("pagination loop accepted")
	}
}

type zonePages struct {
	pages []*r53.ListHostedZonesOutput
	calls int
	fail  bool
}

func (p *zonePages) ListHostedZones(_ context.Context, in *r53.ListHostedZonesInput, _ ...func(*r53.Options)) (*r53.ListHostedZonesOutput, error) {
	if p.fail {
		return nil, errors.New("permission denied")
	}
	if p.calls > 0 && aws.ToString(in.Marker) != "next" {
		return nil, errors.New("missing pagination marker")
	}
	page := p.pages[p.calls]
	p.calls++
	return page, nil
}
func hosted(name, id string, private bool) types.HostedZone {
	return types.HostedZone{Name: aws.String(name), Id: aws.String(id), Config: &types.HostedZoneConfig{PrivateZone: private}}
}
func TestRoute53DiscoveryPaginationRestrictionAmbiguity(t *testing.T) {
	pages := func() *zonePages {
		return &zonePages{pages: []*r53.ListHostedZonesOutput{
			{HostedZones: []types.HostedZone{hosted("example.com.", "/hostedzone/A", false), hosted("private.example.", "P", true)}, IsTruncated: true, NextMarker: aws.String("next")},
			{HostedZones: []types.HostedZone{hosted("example.net.", "B", false)}},
		}}
	}
	p := &route53Provider{zoneClient: pages()}
	zones, err := p.ListZones(context.Background())
	if err != nil || len(zones) != 2 {
		t.Fatal(zones, err)
	}
	p.p = route53.Provider{HostedZoneID: "/hostedzone/B"}
	p.zoneClient = pages()
	zones, err = p.ListZones(context.Background())
	if err != nil || len(zones) != 1 || zones[0].Name != "example.net." {
		t.Fatal(zones, err)
	}
	p.p.HostedZoneID = ""
	p.zoneClient = &zonePages{pages: []*r53.ListHostedZonesOutput{{HostedZones: []types.HostedZone{hosted("example.com.", "A", false), hosted("example.com.", "B", false)}}}}
	if _, err = p.ListZones(context.Background()); err == nil {
		t.Fatal("ambiguous public zones accepted")
	}
	p.zoneClient = &zonePages{fail: true}
	if _, err = p.ListZones(context.Background()); err == nil {
		t.Fatal("permission failure accepted")
	}
}

func TestCloudflareDiscoveryPaginationAndPermission(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		page := r.URL.Query().Get("page")
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authentication")
		}
		fmt.Fprintf(w, `{"success":true,"result":[{"id":"%s","name":"example%s.com"}],"result_info":{"page":%s,"total_pages":2}}`, page, page, page)
	}))
	defer server.Close()
	p := &cloudflareProvider{p: cloudflare.Provider{APIToken: "secret"}, apiBase: server.URL}
	zones, err := p.ListZones(context.Background())
	if err != nil || len(zones) != 2 || calls != 2 {
		t.Fatal(zones, err, calls)
	}
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer denied.Close()
	p.apiBase = denied.URL
	if _, err = p.ListZones(context.Background()); err == nil {
		t.Fatal("permission failure accepted")
	}
}

func TestRoute53RecordsResolveExactPublicZone(t *testing.T) {
	p := &route53Provider{zoneClient: &zonePages{pages: []*r53.ListHostedZonesOutput{{HostedZones: []types.HostedZone{hosted("example.com.", "private", true), hosted("example.com.", "public", false)}}}}}
	provider, err := p.recordsProvider(context.Background(), "example.com")
	if err != nil || provider.HostedZoneID != "public" {
		t.Fatal(provider, err)
	}
	p.zoneClient = &zonePages{pages: []*r53.ListHostedZonesOutput{{HostedZones: []types.HostedZone{hosted("example.com.", "public", false)}}}}
	if _, err = p.recordsProvider(context.Background(), "other.example"); err == nil {
		t.Fatal("undiscovered record target accepted")
	}
	p.zoneClient = &zonePages{pages: []*r53.ListHostedZonesOutput{{IsTruncated: true}}}
	if _, err = p.ListZones(context.Background()); err == nil {
		t.Fatal("missing pagination marker accepted")
	}
}
