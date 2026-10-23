//go:build caddy

package proxy

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestPublicListenerPreservesQueuedConnectionsAcrossReload(t *testing.T) {
	port := "0"
	open := func() *publicListener {
		t.Helper()
		value, err := listenPublic(t.Context(), "funnel", "127.0.0.1", port, 0, net.ListenConfig{})
		if err != nil {
			t.Fatal(err)
		}
		ln := value.(*publicListener)
		t.Cleanup(func() { ln.Close() })
		return ln
	}
	old := open()
	_, port, _ = net.SplitHostPort(old.Addr().String())
	client, err := net.DialTimeout("tcp", old.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("queued")); err != nil {
		t.Fatal(err)
	}
	current := open()
	if old.shared != current.shared {
		t.Fatal("reload replaced the kernel listener")
	}
	// Provisioning a replacement that later fails must leave the last working
	// configuration able to accept the connection already in the queue.
	failed := open()
	failed.Close()
	old.Close()
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := current.Accept(); accepted <- conn }()
	select {
	case conn := <-accepted:
		if conn == nil {
			t.Fatal("queued connection lost")
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		body := make([]byte, 6)
		if _, err := io.ReadFull(conn, body); err != nil || string(body) != "queued" {
			t.Fatalf("queued payload %q: %v", body, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued connection not delivered")
	}
	closed := make(chan error, 1)
	go func() { _, err := current.Accept(); closed <- err }()
	current.Close()
	select {
	case err := <-closed:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("closed Accept: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing listener did not unblock Accept")
	}
	publicListeners.Lock()
	_, remains := publicListeners.byAddress[current.shared.key]
	publicListeners.Unlock()
	if remains {
		t.Fatal("listener retained after final close")
	}
}
