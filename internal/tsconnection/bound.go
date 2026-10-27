package tsconnection

import (
	"net/http"
)

type Bound struct {
	Connection
	ComponentID, InstanceID string
}

func (b *Bound) HTTPClient() *http.Client {
	c := *b.Connection.HTTPClient()
	c.Transport = &boundTransport{base: c.Transport, id: b.ComponentID, instance: b.InstanceID}
	return &c
}

type boundTransport struct {
	base         http.RoundTripper
	id, instance string
}

func (t *boundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.Header.Set("Funnel-Component-ID", t.id)
	c.Header.Set("Funnel-Instance-ID", t.instance)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(c)
}

// Preserve client lifecycle operations through the identity-header wrapper.
func (t *boundTransport) CloseIdleConnections() {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if closer, ok := base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
