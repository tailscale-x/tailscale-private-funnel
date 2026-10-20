// This disposable integration fixture is not a shipped runtime command.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"tailscale.com/tsnet"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	name := os.Getenv("POD_NAME")
	if os.Getenv("QA_BACKEND") == "1" {
		s := &http.Server{Addr: ":8080", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				for i := 0; i < 20; i++ {
					fmt.Fprintf(w, "%s:%d\n", name, i)
					w.(http.Flusher).Flush()
					time.Sleep(500 * time.Millisecond)
				}
				fmt.Fprintln(w, "DONE")
				return
			}
			fmt.Fprintln(w, name)
		})}
		drained := make(chan struct{})
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = s.Shutdown(shutdown)
			close(drained)
		}()
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
		<-drained
		return
	}
	key, e := os.ReadFile("/bootstrap/key")
	if e != nil {
		log.Fatal("bootstrap unavailable")
	}
	raw := &tsnet.Server{Dir: "/state", Hostname: name, AuthKey: strings.TrimSpace(string(key)), Ephemeral: true, Logf: func(format string, args ...any) {
		if os.Getenv("QA_MAP_TRACE") == "1" && (strings.Contains(format, "PollNetMap:") || strings.Contains(format, "netmap received:") || strings.Contains(format, "mapRoutine: context done")) {
			log.Printf(format, args...)
		}
	}, UserLogf: func(string, ...any) {}}
	c := tsconnection.Wrap(raw)
	up, cancel := context.WithTimeout(ctx, 60*time.Second)
	_, e = c.Up(up)
	cancel()
	if e != nil {
		log.Fatal(e)
	}
	raw.AuthKey = ""
	key = nil
	defer c.Close()
	api, _ := c.LocalClient()
	manager := gateway.NewListenerManager(c)
	defer manager.Close()
	pool := &gateway.LocalPool{AllowRemote: true}
	continuity := &gateway.BackendContinuity{}
	var mu sync.Mutex
	service := ""
	port := uint16(8080)
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		st, e := api.Status(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"device_id": st.Self.ID, "ips": st.TailscaleIPs})
	})
	mux.HandleFunc("/configure", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Service   string
			Port      uint16
			NodeUID   string
			NodeName  string
			Endpoints []proxy.PodEndpoint
		}
		if e := json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&q); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		service = q.Service
		port = q.Port
		pool.Update(q.Endpoints, q.NodeUID, q.NodeName)
		probe, done := context.WithTimeout(r.Context(), 3*time.Second)
		reachable := false
		if manager.Has(service, port) {
			reachable = pool.Ready(probe)
		} else {
			reachable = pool.ReadyLocal(probe)
		}
		done()
		if !reachable {
			if !continuity.Keep(manager.Has(service, port), false, time.Now()) {
				manager.Remove(service, port)
			}
			json.NewEncoder(w).Encode(map[string]any{"reachable": false, "local": pool.Count(), "remote": pool.RemoteCount(), "active": manager.Has(service, port)})
			return
		}
		if pool.Count() == 0 && !manager.Has(service, port) {
			json.NewEncoder(w).Encode(map[string]any{"reachable": true, "local": 0, "remote": pool.RemoteCount(), "active": false})
			return
		}
		continuity.Keep(manager.Has(service, port), true, time.Now())
		if e := manager.ListenDial(ctx, service, port, "fixture-backend", pool.Dial); e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"reachable": true, "local": pool.Count(), "remote": pool.RemoteCount(), "active": manager.Has(service, port)})
	})
	mux.HandleFunc("/drain", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		manager.Remove(service, port)
		fmt.Fprintln(w, "manager draining")
	})
	mux.HandleFunc("/close", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		manager.Remove(service, port)
		fmt.Fprintln(w, "closed")
	})
	mux.HandleFunc("/map", func(w http.ResponseWriter, r *http.Request) {
		ip, e := netip.ParseAddr(r.URL.Query().Get("ip"))
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		probe, done := context.WithTimeout(r.Context(), 7*time.Second)
		defer done()
		x, e := tsconnection.InspectService(probe, c, ip, 8080)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(x)
	})
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		host, _, e := net.SplitHostPort(target)
		ip, x := netip.ParseAddr(host)
		if e != nil || x != nil || !ip.IsValid() {
			http.Error(w, "literal target required", 400)
			return
		}
		client := c.HTTPClient()
		client.Timeout = 45 * time.Second
		req, _ := http.NewRequestWithContext(r.Context(), "GET", "http://"+target+r.URL.Query().Get("path"), nil)
		res, e := client.Do(req)
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		buf := make([]byte, 1024)
		for {
			n, e := res.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				w.(http.Flusher).Flush()
			}
			if e != nil {
				break
			}
		}
	})
	server := &http.Server{Addr: "127.0.0.1:9092", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); server.Close() }()
	log.Print("fixture ready")
	_ = server.ListenAndServe()
}
