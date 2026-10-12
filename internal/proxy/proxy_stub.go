//go:build !caddy

package proxy

import (
	"context"
	"fmt"
	"net"
)

func Start(mode, listen string, _ func(context.Context, string, string) (net.Conn, error)) error {
	return fmt.Errorf("%s runtime requires the caddy build tag", mode)
}

func ValidateConfig(mode string) error {
	return fmt.Errorf("%s config validation requires the caddy build tag", mode)
}

func ReloadRoutes(_ []Route) error                                            { return fmt.Errorf("route reload requires the caddy build tag") }
func SetPeerResolver(_ func(context.Context, string, uint16) (string, error)) {}

func Stop() error { return nil }
