package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestRegistrationRetryTransientThenSuccess(t *testing.T) {
	codes := []connect.Code{connect.CodeAborted, connect.CodeUnavailable, connect.CodeDeadlineExceeded}
	attempts, notices := 0, 0
	err := retryRegistration(t.Context(), func(context.Context) error {
		attempts++
		if attempts <= len(codes) {
			return connect.NewError(codes[attempts-1], errors.New("transient"))
		}
		return nil
	}, time.Millisecond, func(error) { notices++ })
	if err != nil || attempts != 4 || notices != 3 {
		t.Fatalf("err=%v attempts=%d notices=%d", err, attempts, notices)
	}
}

func TestRegistrationRetryPermanentDenial(t *testing.T) {
	for _, code := range []connect.Code{connect.CodePermissionDenied, connect.CodeUnauthenticated, connect.CodeInvalidArgument, connect.CodeNotFound} {
		t.Run(code.String(), func(t *testing.T) {
			attempts := 0
			denial := connect.NewError(code, errors.New("permanent"))
			err := retryRegistration(t.Context(), func(context.Context) error { attempts++; return denial }, time.Millisecond, nil)
			if err != denial || attempts != 1 {
				t.Fatalf("err=%v attempts=%d", err, attempts)
			}
		})
	}
}

func TestRegistrationRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	err := retryRegistration(ctx, func(context.Context) error {
		attempts++
		cancel()
		return connect.NewError(connect.CodeUnavailable, errors.New("offline"))
	}, time.Hour, nil)
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
	err = retryRegistration(ctx, func(context.Context) error { t.Fatal("canceled caller performed another RPC"); return nil }, time.Hour, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRegistrationRetryHonorsCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	attempts := 0
	err := retryRegistration(ctx, func(context.Context) error {
		attempts++
		return connect.NewError(connect.CodeUnavailable, errors.New("offline"))
	}, time.Hour, nil)
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestRegistrationRetryTransportDeadline(t *testing.T) {
	attempts := 0
	err := retryRegistration(t.Context(), func(context.Context) error {
		attempts++
		if attempts == 1 {
			return context.DeadlineExceeded
		}
		return nil
	}, time.Millisecond, nil)
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}
