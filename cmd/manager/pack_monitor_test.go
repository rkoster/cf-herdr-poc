package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cf-herdr-poc/internal/supervisor"
)

type monitorCollie struct {
	mu       sync.Mutex
	restarts int
	readies  int
}

func (m *monitorCollie) Start(context.Context) error { return nil }
func (m *monitorCollie) Restart(context.Context) error { m.mu.Lock(); m.restarts++; m.mu.Unlock(); return nil }
func (m *monitorCollie) Ready(context.Context) error { m.mu.Lock(); m.readies++; m.mu.Unlock(); return nil }
func (m *monitorCollie) Stop(context.Context) error { return nil }
func (m *monitorCollie) Errors() <-chan error { return make(chan error) }
func (m *monitorCollie) Healthy() bool { return true }

var _ supervisor.Collie = (*monitorCollie)(nil)

func TestPackStateMonitorRestartsCollieOnceAfterDebouncedChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pack-trust.json")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil { t.Fatal(err) }
	collie := &monitorCollie{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitorPackState(ctx, path, collie, 10*time.Millisecond, 30*time.Millisecond, func(error) {})
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("three"), 0o600); err != nil { t.Fatal(err) }
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		collie.mu.Lock(); restarts, readies := collie.restarts, collie.readies; collie.mu.Unlock()
		if restarts == 1 && readies == 1 { return }
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Collie was not restarted once after the debounced trust-store change")
}
