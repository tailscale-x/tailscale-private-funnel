package web

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func (c *Control) addZoneOperation(ctx context.Context, providerID, domain, actor string) (store.ManagedZone, error) {
	fail := func(code connect.Code, message string) (store.ManagedZone, error) {
		return store.ManagedZone{}, connect.NewError(code, errors.New(message))
	}
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if policy.ValidateHostname(domain) != nil || providerID == "" {
		return fail(connect.CodeInvalidArgument, "DNS provider and valid domain are required")
	}
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	zones, e := c.Store.Zones()
	if e != nil {
		return fail(connect.CodeUnavailable, "Unable to read managed zones")
	}
	for _, z := range zones {
		if z.Zone == domain {
			return fail(connect.CodeAlreadyExists, "Already managed")
		}
	}
	discovered, e := c.discover(ctx, providerID)
	if e != nil {
		return fail(connect.CodeUnavailable, "Unable to verify domain with DNS provider: "+e.Error())
	}
	count := 0
	for _, z := range discovered {
		if strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z.Name), ".")) == domain {
			count++
		}
	}
	if count == 0 {
		return fail(connect.CodeInvalidArgument, "Domain was not returned by this DNS provider; configure it with the vendor and check listing permissions")
	}
	if count > 1 {
		return fail(connect.CodeFailedPrecondition, "Multiple provider zones match this domain; resolve vendor ambiguity")
	}
	_, adapter, e := c.providerAdapter(providerID)
	if e == nil {
		_, e = adapter.List(ctx, domain)
	}
	if e != nil {
		return fail(connect.CodeUnavailable, "Domain DNS read access could not be verified")
	}
	if e = c.Store.AddZone(providerID, domain); e != nil {
		return store.ManagedZone{}, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	_ = c.Store.Audit("zone.add:"+providerID+":"+domain, actor)
	zones, e = c.Store.Zones()
	if e != nil {
		return store.ManagedZone{}, rpcFailure(connect.CodeInternal, e)
	}
	for _, z := range zones {
		if z.ProviderID == providerID && z.Zone == domain {
			return z, nil
		}
	}
	return fail(connect.CodeInternal, "Added zone could not be read")
}
