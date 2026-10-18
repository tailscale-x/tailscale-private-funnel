package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
)

func (m *Manager) ValidateTransfer(r Resource) error {
	if r.Mode != "control" || r.Kind != "controls" {
		return errors.New("only controls can be transferred")
	}
	if r.Phase == "Transfer fenced" || r.Phase == "Transferred" || r.Phase == "Cleanup pending" {
		return errors.New("source is already fenced, transferred, or being deleted")
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			return errors.New("source state/data paths must be real owned directories")
		}
		b, err := os.ReadFile(filepath.Join(p, ".funnel-native-resource"))
		if err != nil || string(b) != r.ID {
			return errors.New("source ownership marker mismatch")
		}
	}
	return nil
}
func (m *Manager) FenceTransfer(ctx context.Context, r *Resource) error {
	if err := m.ValidateTransfer(*r); err != nil {
		return err
	}
	// Record fencing before any action; a partial failure must never permit our
	// lifecycle API to restart this identity.
	r.Phase = "Transfer fenced"
	if err := m.save(r); err != nil {
		return err
	}
	if _, err := m.run(ctx, "systemctl", []string{"disable", "--now", r.Unit}, nil, nil); err != nil {
		return err
	}
	state, _ := m.run(ctx, "systemctl", []string{"is-active", r.Unit}, nil, nil)
	if value := strings.TrimSpace(string(state)); value != "inactive" && value != "failed" {
		return errors.New("source service is not verified stopped")
	}
	pid, err := m.run(ctx, "systemctl", []string{"show", r.Unit, "--property=MainPID", "--value"}, nil, nil)
	if err != nil || strings.TrimSpace(string(pid)) != "0" {
		return errors.New("source process termination is not verified")
	}
	if err := os.WriteFile(filepath.Join(r.StateDir, "transfer-fenced"), []byte(r.ID), 0600); err != nil {
		return err
	}
	unit := filepath.Join(m.UnitDir, r.Unit)
	if err := os.Rename(unit, unit+".transfer-fenced"); err != nil {
		return err
	}
	if _, err := m.run(ctx, "systemctl", []string{"daemon-reload"}, nil, nil); err != nil {
		return err
	}
	_, err = m.run(ctx, "systemctl", []string{"mask", r.Unit}, nil, nil)
	return err
}
func (m *Manager) RemoveTransferred(ctx context.Context, r Resource, transferID string) error {
	if r.Phase != "Transfer fenced" {
		return errors.New("source is not transfer fenced")
	}
	if err := app.MarkControlTransferred(app.Options{StateDir: r.StateDir, DataDir: r.DataDir, Database: r.Database}, transferID); err != nil {
		return err
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		b, err := os.ReadFile(filepath.Join(p, ".funnel-native-resource"))
		if err != nil || string(b) != r.ID {
			return errors.New("source ownership marker mismatch")
		}
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(m.StateRoot, r.Kind, r.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(m.UnitDir, r.Unit) + ".transfer-fenced"); err != nil {
		return err
	}
	if err := os.Remove(m.path(r.Kind, r.Name)); err != nil {
		return err
	}
	// Keep the mask as a non-secret fencing tombstone for the old immutable unit.
	_, err := m.run(ctx, "systemctl", []string{"daemon-reload"}, nil, nil)
	return err
}
