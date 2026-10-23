package tsconnection

import (
	"context"
	"net"

	"tailscale.com/tsnet"
)

// DrainableListener quiesces Accept separately from final Serve/advertisement
// removal. Existing streams keep their Service route until final Close. The
// caller must bound draining and must never reuse the port before final cleanup.
// Early advertisement withdrawal is deliberately avoided: on the pinned SDK it
// can remove the client's only route while an established stream is still open.
type DrainableListener interface {
	net.Listener
	Drain(context.Context) error
}

type serviceListener struct{ *tsnet.ServiceListener }

func (e *Embedded) listenService(name string, mode tsnet.ServiceMode) (net.Listener, error) {
	ln, err := e.Server.ListenService(name, mode)
	if err != nil {
		return nil, err
	}
	return &serviceListener{ServiceListener: ln}, nil
}
func (l *serviceListener) Drain(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Closing just the underlying acceptor leaves SDK advertisement references
	// and Serve entries intact. ServiceListener.Close performs final cleanup once
	// the application has finished existing streams (or reached its deadline).
	return l.ServiceListener.Listener.Close()
}
