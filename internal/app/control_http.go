package app

import (
	"net/http"
	"time"
)

// Bound idle control RPC connections, including clients using older releases.
// IdleTimeout runs between requests; it does not limit active uploads/streams.
func controlHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
}
