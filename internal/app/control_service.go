package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	ts "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func readProtected(path string, projected bool) (string, error) {
	info, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	allowed := os.FileMode(0600)
	if projected {
		allowed = 0640
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() & ^allowed != 0 {
		return "", errors.New("OAuth credential input must be a protected regular file")
	}
	b, e := os.ReadFile(path)
	return strings.TrimSpace(string(b)), e
}
func ensureControlService(ctx context.Context, o Options, s *store.Store, port uint16) error {
	var existing map[string]any
	if e := s.GetJSON("tailnet", &existing); e == nil {
		if slug, ok := existing["installation"].(string); ok && slug != "" && slug != o.Installation {
			return errors.New("database belongs to a different installation")
		}
	}
	idFile, secretFile := o.ServicesClientIDFile, o.ServicesClientSecretFile
	if o.KubeNamespace != "" {
		idFile = "/etc/funnel-services/client_id"
		secretFile = "/etc/funnel-services/client_secret"
	}
	id, secret := "", ""
	var e error
	if idFile != "" {
		id, e = readProtected(idFile, o.KubeNamespace != "")
		if e != nil {
			return e
		}
	}
	if secretFile != "" {
		secret, e = readProtected(secretFile, o.KubeNamespace != "")
		if e != nil {
			return e
		}
	}
	if id == "" {
		id, _ = s.GetSecret("tailscale_services_client_id")
	}
	if secret == "" {
		secret, _ = s.GetSecret("tailscale_services_client_secret")
	}
	if id == "" || secret == "" {
		return errors.New("separate Service-management OAuth credentials required for the stable control Service")
	}
	if o.Tailnet == "" {
		return errors.New("tailnet required for control Service creation")
	}
	client := ts.Client{ClientID: id, ClientSecret: secret, Tailnet: o.Tailnet}
	service, e := client.CreateService(ctx, ts.Service{Name: identity.ControlService(o.Installation), DisplayName: "Funnel " + o.Installation + " control", Ports: []string{"tcp:" + strconv.Itoa(int(port))}, Tags: []string{identity.ServiceTag(o.Installation, true)}})
	if e != nil {
		return fmt.Errorf("control Service creation: %w", e)
	}
	if e = s.SetSecrets(map[string]string{"tailscale_services_client_id": id, "tailscale_services_client_secret": secret}); e != nil {
		return e
	}
	var settings map[string]any
	_ = s.GetJSON("tailnet", &settings)
	if settings == nil {
		settings = map[string]any{}
	}
	settings["name"] = o.Tailnet
	settings["installation"] = o.Installation
	if e = s.SetJSON("tailnet", settings); e != nil {
		return e
	}
	if e = s.SetJSON("control_service", service); e != nil {
		return e
	}
	usersIDFile, usersSecretFile := o.UsersClientIDFile, o.UsersClientSecretFile
	if o.KubeNamespace != "" {
		usersIDFile = "/etc/funnel-users/client_id"
		usersSecretFile = "/etc/funnel-users/client_secret"
	}
	uid, usecret := "", ""
	if usersIDFile != "" {
		uid, e = readProtected(usersIDFile, o.KubeNamespace != "")
		if e != nil {
			return e
		}
	} else {
		uid, _ = s.GetSecret("tailscale_users_client_id")
	}
	if usersSecretFile != "" {
		usecret, e = readProtected(usersSecretFile, o.KubeNamespace != "")
		if e != nil {
			return e
		}
	} else {
		usecret, _ = s.GetSecret("tailscale_users_client_secret")
	}
	if uid == "" || usecret == "" {
		return errors.New("separate users:read credentials required for Owner/Admin authorization")
	}
	if _, e = (ts.Client{ClientID: uid, ClientSecret: usecret, Tailnet: o.Tailnet}).ListUsers(ctx); e != nil {
		return fmt.Errorf("human-role lookup credentials: %w", e)
	}
	return s.SetSecrets(map[string]string{"tailscale_users_client_id": uid, "tailscale_users_client_secret": usecret})
}
