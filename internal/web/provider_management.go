package web

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type providerInput struct {
	Name      string            `json:"name"`
	Provider  string            `json:"provider"`
	Config    map[string]string `json:"config"`
	AccountID string            `json:"account_id"`
	Enabled   *bool             `json:"enabled"`
}

func (c *Control) providerAdapter(id string) (store.ProviderRecord, dns.Provider, error) {
	p, config, err := c.Store.ProviderConfig(id)
	if err != nil {
		return p, nil, fmt.Errorf("DNS provider not found")
	}
	f := dns.Registry[p.Provider]
	if f == nil {
		return p, nil, fmt.Errorf("provider has no linked runtime adapter")
	}
	a, err := f(config)
	return p, a, err
}
func (c *Control) discover(ctx context.Context, id string) ([]libdns.Zone, error) {
	_, a, err := c.providerAdapter(id)
	if err != nil {
		return nil, err
	}
	l, ok := a.(interface {
		ListZones(context.Context) ([]libdns.Zone, error)
	})
	if !ok {
		return nil, fmt.Errorf("zone discovery is unavailable for this vendor")
	}
	return l.ListZones(ctx)
}

func (c *Control) saveProviderOperation(id string, in providerInput, actor string) (store.ProviderRecord, error) {
	creating := id == ""
	enabled := true
	config := map[string]string{}
	if !creating {
		old, existing, err := c.Store.ProviderConfig(id)
		if err != nil {
			return store.ProviderRecord{}, rpcFailure(connect.CodeNotFound, errors.New("DNS provider not found"))
		}
		if in.Provider != "" && in.Provider != old.Provider {
			return store.ProviderRecord{}, rpcFailure(connect.CodeFailedPrecondition, errors.New("Vendor cannot be changed"))
		}
		in.Provider = old.Provider
		config = existing
		enabled = old.Enabled
		if in.Name == "" {
			in.Name = old.Name
		}
	}
	name, err := store.ValidateName(in.Name)
	if err != nil {
		return store.ProviderRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(err.Error()))
	}
	if dns.Registry[in.Provider] == nil {
		return store.ProviderRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New("Choose a linked vendor"))
	}
	if creating {
		id, err = store.NewResourceID()
		if err != nil {
			return store.ProviderRecord{}, rpcFailure(connect.CodeInternal, errors.New(err.Error()))
		}
	}
	credentialsChanged := creating
	for k, v := range in.Config {
		if strings.TrimSpace(v) != "" {
			credentialsChanged = credentialsChanged || config[k] != v
			config[k] = v
		}
	}
	if in.AccountID != "" {
		credentialsChanged = credentialsChanged || config["account_id"] != in.AccountID
		config["account_id"] = in.AccountID
	}
	required := map[string][]string{"cloudflare": {"api_token"}, "godaddy": {"api_token"}, "route53": {"access_key_id", "secret_access_key"}}
	for _, field := range required[in.Provider] {
		if strings.TrimSpace(config[field]) == "" {
			return store.ProviderRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(strings.ReplaceAll(field, "_", " ")+" is required"))
		}
	}
	if len(config) == 0 {
		return store.ProviderRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New("credentials are required for a new DNS provider"))
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := dns.Registry[in.Provider](config); err != nil {
		return store.ProviderRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(err.Error()))
	}
	var saveErr error
	if credentialsChanged {
		saveErr = c.Store.SaveProvider(id, in.Provider, config, enabled, name)
	} else {
		saveErr = c.Store.RenameResource("provider", id, name)
		if saveErr == nil && in.Enabled != nil {
			saveErr = c.Store.SetProviderEnabled(id, enabled)
		}
	}
	if err := saveErr; err != nil {
		return store.ProviderRecord{}, rpcFailure(connect.CodeInternal, errors.New(err.Error()))
	}
	_ = c.Store.Audit("provider.save:"+id, actor)
	p, _, err := c.Store.ProviderConfig(id)
	return p, rpcFailure(connect.CodeInternal, err)
}
