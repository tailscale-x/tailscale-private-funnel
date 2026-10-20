package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
)

func serveServiceDiagnostics(ctx context.Context, data string, c tsconnection.Connection) (func(), error) {
	dir := filepath.Join(data, "diagnostics")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("diagnostic directory must be a real directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "service.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("diagnostic path is not a socket")
		}
		conn, e := net.DialTimeout("unix", path, time.Second)
		if e == nil {
			conn.Close()
			return nil, fmt.Errorf("diagnostic socket already active")
		}
		if !errors.Is(e, syscall.ECONNREFUSED) {
			return nil, e
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		ln.Close()
		return nil, err
	}
	busy := make(chan struct{}, 1)
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/service" {
			http.Error(w, "GET /service required", 405)
			return
		}
		q, err := diagnosticQuery(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ip, err := netip.ParseAddr(q[0])
		port, e := strconv.ParseUint(q[1], 10, 16)
		if err != nil || e != nil || port == 0 {
			http.Error(w, "literal Tailscale IP and TCP port required", 400)
			return
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			http.Error(w, "diagnostic already running", 429)
			return
		}
		probe, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		result, err := tsconnection.InspectService(probe, c, ip, uint16(port))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(result)
	})}
	go server.Serve(ln)
	stop := func() { server.Close() }
	go func() { <-ctx.Done(); stop() }()
	return stop, nil
}

func diagnosticQuery(r *http.Request) ([2]string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return [2]string{}, err
	}
	for k, v := range q {
		if (k != "ip" && k != "port") || len(v) != 1 {
			return [2]string{}, fmt.Errorf("only single ip and port parameters accepted")
		}
	}
	if len(q) != 2 {
		err = fmt.Errorf("ip and port required")
	}
	return [2]string{q.Get("ip"), q.Get("port")}, err
}
