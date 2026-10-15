package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	cloudflare "github.com/libdns/cloudflare"
	godaddy "github.com/libdns/godaddy"
	"github.com/libdns/libdns"
	route53 "github.com/libdns/route53"
)

type Record struct {
	Name, Type, Value      string
	TTL                    time.Duration
	Priority, Weight, Port uint16
	Target                 string
}
type Provider interface {
	List(context.Context, string) ([]libdns.Record, error)
	Apply(context.Context, string, []libdns.Record, []libdns.Record) error
	Capabilities() []string
}
type Factory func(map[string]string) (Provider, error)

type ProviderSpec struct {
	ID, Name string
	Fields   []string
	Supports []string
}

type cloudflareProvider struct {
	p          cloudflare.Provider
	apiBase    string
	httpClient *http.Client
}
type godaddyProvider struct {
	p          godaddy.Provider
	apiBase    string
	httpClient *http.Client
}
type route53Provider struct {
	p          route53.Provider
	zoneClient route53ZoneAPI
}

type recordProvider interface {
	libdns.RecordGetter
	libdns.RecordSetter
	libdns.RecordDeleter
}

type zoneLister interface{ libdns.ZoneLister }

func NormalizeRecords(records []libdns.Record, zone string) []libdns.Record {
	zone = strings.TrimSuffix(strings.TrimSpace(zone), ".")
	out := make([]libdns.Record, 0, len(records))
	for _, record := range records {
		rr := record.RR()
		name := strings.TrimSuffix(strings.TrimSpace(rr.Name), ".")
		if name == "" || name == "@" {
			name = zone
		} else if zone != "" && name != zone && !strings.HasSuffix(name, "."+zone) {
			if !strings.Contains(name, ".") {
				name += "." + zone
			}
		}
		out = append(out, libdns.RR{Name: name, Type: rr.Type, Data: rr.Data, TTL: rr.TTL})
	}
	return out
}

func applyRecords(ctx context.Context, p recordProvider, zone string, add, remove []libdns.Record) error {
	if len(remove) > 0 {
		if _, err := p.DeleteRecords(ctx, zone, remove); err != nil {
			return err
		}
	}
	if len(add) > 0 {
		_, err := p.SetRecords(ctx, zone, add)
		return err
	}
	return nil
}

func (p *cloudflareProvider) List(ctx context.Context, zone string) ([]libdns.Record, error) {
	records, err := p.p.GetRecords(ctx, zone)
	if err != nil {
		return nil, err
	}
	return NormalizeRecords(records, zone), nil
}
func (p *cloudflareProvider) Capabilities() []string { return []string{"A", "CNAME", "SRV", "TXT"} }
func relativeRecords(records []libdns.Record, zone string) ([]libdns.Record, error) {
	out := make([]libdns.Record, 0, len(records))
	for _, record := range records {
		rr := record.RR()
		rr.Name = libdns.RelativeName(rr.Name, zone)
		parsed, err := rr.Parse()
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

func (p *cloudflareProvider) Apply(ctx context.Context, zone string, add, remove []libdns.Record) error {
	// One-record operations make completed additions recoverable if a later
	// append fails. The control ledger advances only after the full apply.
	appendOne := func(ctx context.Context, record libdns.Record) error {
		relative, err := relativeRecords([]libdns.Record{record}, zone)
		if err != nil {
			return err
		}
		_, err = p.p.AppendRecords(ctx, zone, relative)
		return err
	}
	return applyExact(ctx, add, remove, appendOne, func(ctx context.Context, record libdns.Record) error { return p.deleteExactRecord(ctx, zone, record) })
}

func applyExact(ctx context.Context, add, remove []libdns.Record, appendOne, deleteOne func(context.Context, libdns.Record) error) error {
	added, removed := []libdns.Record{}, []libdns.Record{}
	recover := func(cause error) error {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		failures := []string{}
		for _, record := range added {
			if err := deleteOne(recoveryCtx, record); err != nil {
				failures = append(failures, err.Error())
			}
		}
		for _, record := range removed {
			if err := appendOne(recoveryCtx, record); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("%w; DNS recovery pending: %s", cause, strings.Join(failures, "; "))
		}
		return cause
	}
	for _, record := range remove {
		if err := deleteOne(ctx, record); err != nil {
			return recover(err)
		}
		removed = append(removed, record)
	}
	for _, record := range add {
		if err := appendOne(ctx, record); err != nil {
			return recover(err)
		}
		added = append(added, record)
	}
	return nil
}

func (p *cloudflareProvider) deleteExactRecord(ctx context.Context, zone string, record libdns.Record) error {
	zone = strings.TrimSuffix(strings.TrimSpace(zone), ".")
	base := p.apiBase
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	request := func(method, target string, out any) error {
		req, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return err
		}
		token := p.p.APIToken
		if strings.Contains(target, "/dns_records") && p.p.ZoneToken != "" {
			token = p.p.ZoneToken
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		// Never surface provider bodies: they may contain credential diagnostics.
		if res.StatusCode/100 != 2 {
			return fmt.Errorf("cloudflare %s: HTTP %d", method, res.StatusCode)
		}
		var envelope struct {
			Success    bool            `json:"success"`
			Result     json.RawMessage `json:"result"`
			ResultInfo struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&envelope); err != nil {
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
	var zones []struct {
		ID string `json:"id"`
	}
	if err := request(http.MethodGet, base+"/zones?name="+url.QueryEscape(zone)+"&per_page=2", &zones); err != nil {
		return err
	}
	if len(zones) != 1 {
		return fmt.Errorf("cloudflare zone %q is missing or ambiguous", zone)
	}
	rr := record.RR()
	name := strings.TrimSuffix(rr.Name, ".")
	if name == "" || name == "@" {
		name = zone
	}
	var ids []string
	for page := 1; ; page++ {
		var records []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Content string `json:"content"`
			Data    struct {
				Priority int    `json:"priority"`
				Weight   int    `json:"weight"`
				Port     int    `json:"port"`
				Target   string `json:"target"`
			} `json:"data"`
		}
		query := base + "/zones/" + zones[0].ID + "/dns_records?type=" + url.QueryEscape(strings.ToUpper(rr.Type)) + "&name=" + url.QueryEscape(name) + "&per_page=100&page=" + fmt.Sprint(page)
		if err := request(http.MethodGet, query, &records); err != nil {
			return err
		}
		// Collect exact IDs before any deletes so pagination cannot skip entries.
		for _, item := range records {
			data := item.Content
			if item.Type == "SRV" {
				data = fmt.Sprintf("%d %d %d %s", item.Data.Priority, item.Data.Weight, item.Data.Port, item.Data.Target)
			}
			candidate := libdns.RR{Name: item.Name, Type: item.Type, Data: data}
			if RecordKey(candidate) != RecordKey(record) {
				continue
			}
			ids = append(ids, item.ID)
		}
		if len(records) < 100 {
			break
		}
	}
	for _, id := range ids {
		if err := request(http.MethodDelete, base+"/zones/"+zones[0].ID+"/dns_records/"+id, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *godaddyProvider) List(ctx context.Context, zone string) ([]libdns.Record, error) {
	records, err := p.p.GetRecords(ctx, zone)
	if err != nil {
		return nil, err
	}
	return NormalizeRecords(records, zone), nil
}
func (p *godaddyProvider) Capabilities() []string { return []string{"A", "CNAME", "SRV", "TXT"} }
func (p *godaddyProvider) Apply(ctx context.Context, zone string, add, remove []libdns.Record) error {
	return applyRecords(ctx, &p.p, zone, add, remove)
}
func (p *route53Provider) List(ctx context.Context, zone string) ([]libdns.Record, error) {
	provider, err := p.recordsProvider(ctx, zone)
	if err != nil {
		return nil, err
	}
	records, err := provider.GetRecords(ctx, zone)
	if err != nil {
		return nil, err
	}
	return NormalizeRecords(records, zone), nil
}
func (p *route53Provider) Capabilities() []string { return []string{"A", "CNAME", "SRV", "TXT"} }
func (p *route53Provider) Apply(ctx context.Context, zone string, add, remove []libdns.Record) error {
	provider, err := p.recordsProvider(ctx, zone)
	if err != nil {
		return err
	}
	return applyRecords(ctx, provider, zone, add, remove)
}

var Registry = map[string]Factory{
	"cloudflare": func(c map[string]string) (Provider, error) {
		return &cloudflareProvider{p: cloudflare.Provider{APIToken: c["api_token"], ZoneToken: c["zone_token"]}}, nil
	},
	"godaddy": func(c map[string]string) (Provider, error) {
		return &godaddyProvider{p: godaddy.Provider{APIToken: c["api_token"]}}, nil
	},
	"route53": func(c map[string]string) (Provider, error) {
		return &route53Provider{p: route53.Provider{AccessKeyId: c["access_key_id"], SecretAccessKey: c["secret_access_key"], Region: c["region"], HostedZoneID: c["hosted_zone_id"]}}, nil
	},
}

// Catalog is generated from the pinned libdns provider repository list. Factories
// are linked by the release generator; providers without a usable factory remain
// visible in the UI with their capability status.
var Catalog = []ProviderSpec{
	{ID: "cloudflare", Name: "Cloudflare", Fields: []string{"api_token", "zone_token", "account_id"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "route53", Name: "Amazon Route 53", Fields: []string{"access_key_id", "secret_access_key", "region"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "googleclouddns", Name: "Google Cloud DNS", Fields: []string{"credentials_json", "project"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "azure", Name: "Azure DNS", Fields: []string{"client_id", "client_secret", "tenant_id", "subscription_id"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "digitalocean", Name: "DigitalOcean", Fields: []string{"token"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "hetzner", Name: "Hetzner", Fields: []string{"token"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "gandi", Name: "Gandi", Fields: []string{"token"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "godaddy", Name: "GoDaddy", Fields: []string{"api_token"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "porkbun", Name: "Porkbun", Fields: []string{"api_key", "secret_api_key"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "namecheap", Name: "Namecheap", Fields: []string{"api_user", "api_key", "client_ip"}, Supports: []string{"A", "CNAME", "TXT"}},
	{ID: "powerdns", Name: "PowerDNS", Fields: []string{"server", "api_key"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "rfc2136", Name: "RFC 2136", Fields: []string{"server", "key_name", "key_secret", "key_algorithm"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "vultr", Name: "Vultr", Fields: []string{"api_key"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "linode", Name: "Linode", Fields: []string{"token"}, Supports: []string{"A", "CNAME", "SRV", "TXT"}},
	{ID: "netlify", Name: "Netlify DNS", Fields: []string{"token"}, Supports: []string{"A", "CNAME", "TXT"}},
}

func init() {
	known := map[string]bool{}
	for _, p := range Catalog {
		known[p.ID] = true
	}
	for _, id := range []string{"acmedns", "alidns", "all-inkl", "arvancloud", "autodns", "bluecat", "bunny", "cloudns", "conoha", "ddnss", "desec", "dinahosting", "directadmin", "dnsexit", "dnsimple", "dnsmadeeasy", "dnspod", "dnsupdate", "dode", "domainnameshop", "dreamhost", "duckdns", "dynu", "dynv6", "easydns", "edgeone", "ednsde", "exoscale", "gcore", "glesys", "googleclouddns", "he", "hexonet", "hosttech", "huaweicloud", "infomaniak", "inwx", "ionos", "katapult", "leaseweb", "liara", "linode", "loopia", "luadns", "mailinabox", "metaname", "mijnhost", "mythicbeasts", "namecheap", "namedotcom", "namesilo", "nanelo", "neoserv", "netcup", "netnod", "nfsn", "nicrudns", "njalla", "openstack-designate", "oraclecloud", "ovh", "parspack", "pph", "regery", "regfish", "route53", "scaleway", "selectel", "servercow", "simplydotcom", "spaceship", "tecnocratica", "tencentcloud", "thelittlehost", "timeweb", "totaluptime", "transip", "unifi", "vercel", "volcengine", "vultr", "websupport", "wedos", "westcn"} {
		if !known[id] {
			Catalog = append(Catalog, ProviderSpec{ID: id, Name: id, Supports: []string{"A", "CNAME", "TXT"}})
		}
	}
}

func ProviderIDs() []string {
	ids := make([]string, 0, len(Catalog))
	for _, spec := range Catalog {
		ids = append(ids, spec.ID)
	}
	return ids
}
