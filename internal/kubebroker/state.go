// Package kubebroker persists one identity per installation and Kubernetes node.
package kubebroker

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"

	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"tailscale.com/ipn"
)

type SecretStore struct {
	Client          client.Client
	Namespace, Name string
}

func StateKey(k ipn.StateKey) string {
	return stateKey(k)
}

func stateKey(k ipn.StateKey) string {
	return "tsnet." + base64.RawURLEncoding.EncodeToString([]byte(k))
}
func (s SecretStore) ReadState(k ipn.StateKey) ([]byte, error) {
	var v core.Secret
	if e := s.Client.Get(context.Background(), types.NamespacedName{Namespace: s.Namespace, Name: s.Name}, &v); e != nil {
		return nil, e
	}
	b, ok := v.Data[stateKey(k)]
	if !ok {
		return nil, ipn.ErrStateNotExist
	}
	return append([]byte{}, b...), nil
}
func (s SecretStore) WriteState(k ipn.StateKey, b []byte) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var v core.Secret
		if e := s.Client.Get(context.Background(), types.NamespacedName{Namespace: s.Namespace, Name: s.Name}, &v); e != nil {
			return e
		}
		if v.Data == nil {
			v.Data = map[string][]byte{}
		}
		v.Data[stateKey(k)] = append([]byte{}, b...)
		return s.Client.Update(context.Background(), &v)
	})
}

// RemoteStore never falls back to an empty identity after a broker failure.
type RemoteStore struct {
	Client *Client
	mu     sync.Mutex
	State  map[string][]byte
}

func (s *RemoteStore) ReadState(k ipn.StateKey) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.State[stateKey(k)]
	if !ok {
		return nil, ipn.ErrStateNotExist
	}
	return append([]byte{}, b...), nil
}
func (s *RemoteStore) WriteState(k ipn.StateKey, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.Client.WriteState(context.Background(), stateKey(k), b); e != nil {
		return fmt.Errorf("persist identity: %w", e)
	}
	if s.State == nil {
		s.State = map[string][]byte{}
	}
	s.State[stateKey(k)] = append([]byte{}, b...)
	return nil
}

func ownedRuntimeLabels() map[string]string {
	if uid := os.Getenv("FUNNEL_OWNER_UID"); uid != "" {
		return map[string]string{"tailscale-private-funnel.io/owner-uid": uid}
	}
	return nil
}
