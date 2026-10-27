package app

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestControlHTTPServerClosesIdleConnectionsWithoutCuttingActiveResponse(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := controlHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
		fmt.Fprint(w, "ok")
	}))
	if server.IdleTimeout != 90*time.Second || server.ReadHeaderTimeout != 5*time.Second {
		t.Fatal("control connection budgets missing")
	}
	server.IdleTimeout = 30 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve(listener)
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /slow HTTP/1.1\r\nHost: control\r\n\r\n")
	<-started
	time.Sleep(3 * server.IdleTimeout)
	close(release)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal("idle timeout cut active response", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatal(string(body), err)
	}
	var b [1]byte
	_, err = conn.Read(b[:])
	if err != io.EOF {
		t.Fatal("idle connection was not closed", err)
	}
}
