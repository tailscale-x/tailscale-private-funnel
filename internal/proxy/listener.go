//go:build caddy

package proxy

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// Keep one kernel TCP accept queue across Caddy reloads. Separate SO_REUSEPORT
// sockets can reset connections queued on the retiring socket during shutdown.
var publicListeners = struct {
	sync.Mutex
	byAddress map[string]*sharedPublicListener
}{byAddress: make(map[string]*sharedPublicListener)}

type sharedPublicListener struct {
	net.Listener
	key     string
	users   []*publicListener
	changed chan struct{}
	failed  chan struct{}
	err     error
}

type publicListener struct {
	shared      *sharedPublicListener
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func init() {
	caddy.RegisterNetwork("funnel", listenPublic)
	caddyhttp.RegisterNetworkHTTP3("funnel", "udp")
}

func listenPublic(ctx context.Context, _, host, port string, offset uint, cfg net.ListenConfig) (any, error) {
	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	address := net.JoinHostPort(host, strconv.Itoa(p+int(offset)))
	publicListeners.Lock()
	defer publicListeners.Unlock()
	shared := publicListeners.byAddress[address]
	if shared == nil {
		ln, err := cfg.Listen(ctx, "tcp", address)
		if err != nil {
			return nil, fmt.Errorf("public listener: %w", err)
		}
		if p == 0 {
			address = ln.Addr().String()
		}
		shared = &sharedPublicListener{Listener: ln, key: address, changed: make(chan struct{}), failed: make(chan struct{})}
		publicListeners.byAddress[address] = shared
		go shared.serve()
	}
	ln := &publicListener{shared: shared, connections: make(chan net.Conn), done: make(chan struct{})}
	shared.users = append(shared.users, ln)
	shared.notify()
	return ln, nil
}

func (s *sharedPublicListener) notify() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *sharedPublicListener) serve() {
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			publicListeners.Lock()
			s.err = err
			close(s.failed)
			publicListeners.Unlock()
			return
		}
		// A pending connection follows the newest accepting configuration, even
		// when the previous virtual listener closes while delivery is blocked.
		for {
			publicListeners.Lock()
			var target *publicListener
			if len(s.users) > 0 {
				target = s.users[len(s.users)-1]
			}
			changed := s.changed
			closed := len(s.users) == 0
			publicListeners.Unlock()
			if closed {
				conn.Close()
				return
			}
			var output chan net.Conn
			if target != nil {
				output = target.connections
			}
			select {
			case output <- conn:
				goto delivered
			case <-changed:
			}
		}
	delivered:
	}
}

func (l *publicListener) Accept() (net.Conn, error) {
	publicListeners.Lock()
	select {
	case <-l.done:
		publicListeners.Unlock()
		return nil, net.ErrClosed
	default:
	}
	publicListeners.Unlock()
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-l.shared.failed:
		return nil, l.shared.err
	}
}

func (l *publicListener) Close() error {
	l.once.Do(func() {
		publicListeners.Lock()
		defer publicListeners.Unlock()
		close(l.done)
		s := l.shared
		for i, user := range s.users {
			if user == l {
				s.users = append(s.users[:i], s.users[i+1:]...)
				break
			}
		}
		s.notify()
		if len(s.users) == 0 {
			delete(publicListeners.byAddress, s.key)
			s.Listener.Close()
		}
	})
	return nil
}

func (l *publicListener) Addr() net.Addr { return l.shared.Addr() }
