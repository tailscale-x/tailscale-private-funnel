package tsconnection

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmbeddedShutdownPreservesPersistentIdentityAndLogsOutDisposableIdentity(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		t.Run(map[bool]string{false: "persistent", true: "disposable"}[ephemeral], func(t *testing.T) {
			loggedOut, closed := false, false
			err := closeEmbedded(ephemeral, func(ctx context.Context) error {
				if closed {
					t.Fatal("logout ran after transport shutdown")
				}
				loggedOut = true
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("unbounded shutdown")
				}
				return nil
			}, func() error { closed = true; return nil })
			if err != nil || !closed || loggedOut != ephemeral {
				t.Fatal(loggedOut, closed, err)
			}
		})
	}
}
func TestEphemeralLogoutFailureStillClosesTransport(t *testing.T) {
	failed := errors.New("authority unavailable")
	closed := false
	if err := closeEmbedded(true, func(context.Context) error { return failed }, func() error { closed = true; return nil }); !errors.Is(err, failed) || !closed {
		t.Fatal(closed, err)
	}
}
