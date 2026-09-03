package supervisor

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeProcess struct {
	mu      sync.Mutex
	events  *[]string
	wait    chan error
	stopped bool
}

func newFakeProcess(events *[]string) *fakeProcess {
	return &fakeProcess{events: events, wait: make(chan error, 1)}
}

func (p *fakeProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	*p.events = append(*p.events, "start")
	return nil
}

func (p *fakeProcess) Wait() error { return <-p.wait }

func (p *fakeProcess) Stop(time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopped {
		p.stopped = true
		*p.events = append(*p.events, "stop")
		p.wait <- nil
	}
	return nil
}

func TestStartConfiguresManagedLoopbackProcess(t *testing.T) {
	var got ProcessConfig
	events := []string{}
	s := New(Config{
		Executable: "bun", Args: []string{"run", "bridge/index.ts"}, Dir: "/collie",
		ConfigDir: "/manager/config", StateDir: "/manager/state", Host: "127.0.0.1", Port: 8787,
		Stdout: io.Discard, Stderr: io.Discard,
	}, func(config ProcessConfig) Process { got = config; return newFakeProcess(&events) }, func(context.Context, string) error { return nil })

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.Name != "bun" || got.Dir != "/collie" || got.Env["HOME"] != "/manager/config" || got.Env["COLLIE_STATE_DIR"] != "/manager/state" || got.Env["COLLIE_HOST"] != "127.0.0.1" || got.Env["COLLIE_PORT"] != "8787" {
		t.Fatalf("process config = %#v", got)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReadyTimesOut(t *testing.T) {
	events := []string{}
	s := New(Config{ReadyTimeout: 20 * time.Millisecond, ProbeInterval: time.Millisecond}, func(ProcessConfig) Process {
		return newFakeProcess(&events)
	}, func(context.Context, string) error { return errors.New("not ready") })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := s.Ready(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Ready error = %v, want deadline exceeded", err)
	}
	_ = s.Stop(context.Background())
}

func TestConcurrentRestartsAreSerializedAndReapOldProcess(t *testing.T) {
	var mu sync.Mutex
	events := []string{}
	s := New(Config{}, func(ProcessConfig) Process {
		mu.Lock()
		defer mu.Unlock()
		return newFakeProcess(&events)
	}, func(context.Context, string) error { return nil })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() { defer wait.Done(); _ = s.Restart(context.Background()) }()
	}
	wait.Wait()
	_ = s.Stop(context.Background())
	mu.Lock()
	defer mu.Unlock()
	want := []string{"start", "stop", "start", "stop", "start", "stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestReadyReportsChildExit(t *testing.T) {
	events := []string{}
	child := newFakeProcess(&events)
	s := New(Config{ReadyTimeout: time.Second}, func(ProcessConfig) Process { return child }, func(context.Context, string) error { return errors.New("not ready") })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	child.wait <- errors.New("exit status 2")
	deadline := time.Now().Add(time.Second)
	for {
		err := s.Ready(context.Background())
		if err != nil && errors.Is(err, ErrChildExited) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Ready error = %v, want child exit", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStopIsGracefulAndIdempotent(t *testing.T) {
	events := []string{}
	s := New(Config{}, func(ProcessConfig) Process { return newFakeProcess(&events) }, func(context.Context, string) error { return nil })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %v", events)
	}
}
