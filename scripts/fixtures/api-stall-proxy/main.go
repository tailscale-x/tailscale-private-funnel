// Temporary verification fixture. Logs authorities/counts, never TLS contents.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var state struct {
	sync.Mutex
	gate        chan struct{}
	stalled     map[string]int
	connections map[net.Conn]string
}

func main() {
	state.stalled = map[string]int{}
	state.connections = map[net.Conn]string{}
	log.Fatal(http.ListenAndServe(":8080", http.HandlerFunc(handle)))
}
func handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "CONNECT" {
		switch r.URL.Path {
		case "/fault":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			state.Lock()
			var closeConnections []net.Conn
			if r.URL.Query().Get("enabled") == "true" {
				if state.gate == nil {
					state.gate = make(chan struct{})
				}
				// Retire only API tunnels, so connection reuse cannot bypass the fault.
				for conn, host := range state.connections {
					if target(host) {
						closeConnections = append(closeConnections, conn)
					}
				}
			} else if state.gate != nil {
				close(state.gate)
				state.gate = nil
			}
			state.Unlock()
			for _, conn := range closeConnections {
				conn.Close()
			}
		case "/state":
		default:
			http.NotFound(w, r)
			return
		}
		state.Lock()
		defer state.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"enabled": state.gate != nil, "stalled": state.stalled})
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port != "443" {
		http.Error(w, "HTTPS only", 403)
		return
	}
	state.Lock()
	gate := state.gate
	if gate != nil && target(host) {
		state.stalled[host]++
		log.Printf("stall authority=%s", r.Host)
	} else {
		gate = nil
	}
	state.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	up, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.Host)
	if err != nil {
		http.Error(w, "upstream unavailable", 502)
		return
	}
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		up.Close()
		return
	}
	if _, err = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		up.Close()
		return
	}
	if err = buf.Flush(); err != nil {
		conn.Close()
		up.Close()
		return
	}
	state.Lock()
	if state.gate != nil && target(host) {
		state.Unlock()
		conn.Close()
		up.Close()
		return
	}
	state.connections[conn] = host
	state.Unlock()
	closeTunnel := func() { conn.Close(); up.Close(); state.Lock(); delete(state.connections, conn); state.Unlock() }
	go func() { defer closeTunnel(); io.Copy(up, buf) }()
	go func() { defer closeTunnel(); io.Copy(conn, up) }()
}

func target(host string) bool {
	return strings.EqualFold(host, "api.tailscale.com") || strings.EqualFold(host, "api.cloudflare.com")
}
