package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsroute53 "github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/libdns/libdns"
	"github.com/libdns/route53"
)

func (p *godaddyProvider) ListZones(ctx context.Context) ([]libdns.Zone, error) {
	if p.p.APIToken == "" {
		return nil, fmt.Errorf("GoDaddy credentials are required")
	}
	base := p.apiBase
	if base == "" {
		base = "https://api.godaddy.com"
	}
	client := p.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	out := []libdns.Zone{}
	marker := ""
	seen := map[string]bool{}
	seenMarkers := map[string]bool{}
	for {
		query := url.Values{"limit": {"1000"}}
		if marker != "" {
			query.Set("marker", marker)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/domains?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "sso-key "+p.p.APIToken)
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GoDaddy domain discovery failed: %w", err)
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("GoDaddy domain listing: HTTP %d; check API access and credentials", response.StatusCode)
		}
		var domains []struct {
			Domain string `json:"domain"`
		}
		err = json.NewDecoder(response.Body).Decode(&domains)
		response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("GoDaddy domain listing: invalid response")
		}
		if len(domains) == 0 {
			break
		}
		for _, domain := range domains {
			name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain.Domain), "."))
			if name == "" {
				return nil, fmt.Errorf("GoDaddy returned an empty domain")
			}
			if !seen[name] {
				out = append(out, libdns.Zone{Name: name + "."})
				seen[name] = true
			}
		}
		next := domains[len(domains)-1].Domain
		if next == marker || seenMarkers[next] {
			return nil, fmt.Errorf("GoDaddy domain pagination did not advance")
		}
		seenMarkers[next] = true
		marker = next
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type route53ZoneAPI interface {
	ListHostedZones(context.Context, *awsroute53.ListHostedZonesInput, ...func(*awsroute53.Options)) (*awsroute53.ListHostedZonesOutput, error)
}

func (p *route53Provider) publicZones(ctx context.Context) (map[string]string, error) {
	client := p.zoneClient
	if client == nil {
		if p.p.AccessKeyId == "" || p.p.SecretAccessKey == "" {
			return nil, fmt.Errorf("Route 53 credentials are required")
		}
		region := p.p.Region
		if region == "" {
			region = "us-east-1"
		}
		client = awsroute53.NewFromConfig(aws.Config{Region: region, Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(p.p.AccessKeyId, p.p.SecretAccessKey, p.p.SessionToken)), HTTPClient: &http.Client{Timeout: 30 * time.Second}})
	}
	names := map[string]string{}
	var marker *string
	seenMarkers := map[string]bool{}
	restriction := strings.TrimPrefix(p.p.HostedZoneID, "/hostedzone/")
	for {
		page, err := client.ListHostedZones(ctx, &awsroute53.ListHostedZonesInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("Route 53 zone discovery: check ListHostedZones permission: %w", err)
		}
		for _, zone := range page.HostedZones {
			if zone.Config == nil || zone.Config.PrivateZone {
				continue
			}
			id := strings.TrimPrefix(aws.ToString(zone.Id), "/hostedzone/")
			if restriction != "" && id != restriction {
				continue
			}
			name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(aws.ToString(zone.Name)), "."))
			if name == "" || id == "" {
				return nil, fmt.Errorf("Route 53 returned an incomplete hosted zone")
			}
			if previous, ok := names[name]; ok {
				if previous != id {
					return nil, fmt.Errorf("Route 53 has multiple public hosted zones for %s; resolve the ambiguity or restrict this DNS provider to one hosted zone", name)
				}
				continue
			}
			names[name] = id
		}
		if !page.IsTruncated {
			break
		}
		next := aws.ToString(page.NextMarker)
		if next == "" || seenMarkers[next] {
			return nil, fmt.Errorf("Route 53 zone pagination did not advance")
		}
		seenMarkers[next] = true
		marker = page.NextMarker
	}
	return names, nil
}

func (p *route53Provider) ListZones(ctx context.Context) ([]libdns.Zone, error) {
	names, err := p.publicZones(ctx)
	if err != nil {
		return nil, err
	}
	out := []libdns.Zone{}
	for name := range names {
		out = append(out, libdns.Zone{Name: name + "."})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve the public zone explicitly rather than using libdns's first-name match.
func (p *route53Provider) recordsProvider(ctx context.Context, zone string) (*route53.Provider, error) {
	zones, err := p.publicZones(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := zones[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))]
	if !ok {
		return nil, fmt.Errorf("domain is not an accessible public Route 53 hosted zone")
	}
	return &route53.Provider{AccessKeyId: p.p.AccessKeyId, SecretAccessKey: p.p.SecretAccessKey, SessionToken: p.p.SessionToken, Region: p.p.Region, HostedZoneID: id}, nil
}

// The pinned libdns Cloudflare implementation does not paginate ListZones.
func (p *cloudflareProvider) ListZones(ctx context.Context) ([]libdns.Zone, error) {
	base := p.apiBase
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	client := p.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	zones := []libdns.Zone{}
	names := map[string]string{}
	for page := 1; ; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/zones?per_page=50&page=%d", base, page), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+p.p.APIToken)
		response, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Cloudflare zone discovery failed")
		}
		var data struct {
			Success bool `json:"success"`
			Result  []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"result"`
			Info struct {
				Page       int `json:"page"`
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		err = json.NewDecoder(response.Body).Decode(&data)
		response.Body.Close()
		if response.StatusCode != 200 || err != nil || !data.Success {
			return nil, fmt.Errorf("Cloudflare zone listing failed (HTTP %d); check Zone Read permission and credentials", response.StatusCode)
		}
		for _, zone := range data.Result {
			name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone.Name), "."))
			if name == "" || zone.ID == "" {
				return nil, fmt.Errorf("Cloudflare returned an incomplete zone")
			}
			if previous, exists := names[name]; exists {
				if previous != zone.ID {
					return nil, fmt.Errorf("Cloudflare returned ambiguous zones for %s", name)
				}
				continue
			}
			names[name] = zone.ID
			zones = append(zones, libdns.Zone{Name: name + "."})
		}
		if page >= data.Info.TotalPages {
			break
		}
		if len(data.Result) == 0 || data.Info.Page != page {
			return nil, fmt.Errorf("Cloudflare pagination did not advance")
		}
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	return zones, nil
}
