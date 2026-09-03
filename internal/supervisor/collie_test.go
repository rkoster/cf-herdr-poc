package supervisor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
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
	values := environmentMap(got.Env)
	if got.Name != "bun" || got.Dir != "/collie" || values["HERDR_PLUGIN_CONFIG_DIR"] != "/manager/config" || values["HERDR_PLUGIN_STATE_DIR"] != "/manager/state" || values["COLLIE_STATE_DIR"] != "/manager/state" || values["COLLIE_HOST"] != "127.0.0.1" || values["COLLIE_PORT"] != "8787" {
		t.Fatalf("process config = %#v", got)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultWritersStreamChildLogs(t *testing.T) {
	events := []string{}
	var got ProcessConfig
	s := New(Config{}, func(config ProcessConfig) Process { got = config; return newFakeProcess(&events) }, func(context.Context, string) error { return nil })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	if got.Stdout != os.Stdout || got.Stderr != os.Stderr {
		t.Fatalf("default writers = %v, %v", got.Stdout, got.Stderr)
	}
}

func TestExecProcessStreamsLogsAndKillsStuckProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process groups")
	}
	temp := t.TempDir()
	ready := temp + "/ready"
	term := temp + "/term"
	var stdout, stderr bytes.Buffer
	s := New(Config{
		Executable: "sh", Args: []string{"-c", `trap 'touch "$TERM_FILE"' TERM; echo child-out; echo child-err >&2; touch "$READY_FILE"; while :; do :; done`},
		Stdout: &stdout, Stderr: &stderr, StopTimeout: 30 * time.Millisecond,
	}, nil, func(context.Context, string) error { return errors.New("not ready") })
	t.Setenv("READY_FILE", ready)
	t.Setenv("TERM_FILE", term)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, ready)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, term)
	if !strings.Contains(stdout.String(), "child-out") || !strings.Contains(stderr.String(), "child-err") {
		t.Fatalf("logs = %q / %q", stdout.String(), stderr.String())
	}
}

func TestConcurrentReadyAndRestartUsesProcessGeneration(t *testing.T) {
	var mu sync.Mutex
	events := []string{}
	probeEntered := make(chan struct{}, 1)
	s := New(Config{ReadyTimeout: time.Second, ProbeInterval: time.Millisecond}, func(ProcessConfig) Process {
		mu.Lock()
		defer mu.Unlock()
		return newFakeProcess(&events)
	}, func(context.Context, string) error {
		select {
		case probeEntered <- struct{}{}:
		default:
		}
		return errors.New("not ready")
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	readyDone := make(chan error, 1)
	go func() { readyDone <- s.Ready(context.Background()) }()
	<-probeEntered
	if err := s.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readyDone:
		if err == nil {
			t.Fatal("Ready unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent Ready leaked after restart")
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRealProcessConcurrentReadyAndRestartReapsOldGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process groups")
	}
	temp := t.TempDir()
	starts := temp + "/starts"
	script := `echo "$$" >> "$STARTS_FILE"; trap 'exit 0' TERM; while :; do sleep 1; done`
	probeEntered := make(chan struct{}, 1)
	s := New(Config{Executable: "sh", Args: []string{"-c", script}, StopTimeout: 100 * time.Millisecond, ReadyTimeout: 2 * time.Second, ProbeInterval: time.Millisecond}, nil, func(context.Context, string) error {
		select {
		case probeEntered <- struct{}{}:
		default:
		}
		return errors.New("not ready")
	})
	t.Setenv("STARTS_FILE", starts)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForLines(t, starts, 1)
	readyDone := make(chan error, 1)
	go func() { readyDone <- s.Ready(context.Background()) }()
	<-probeEntered
	if err := s.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForLines(t, starts, 2)
	select {
	case err := <-readyDone:
		if !errors.Is(err, ErrChildExited) {
			t.Fatalf("Ready error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Ready did not observe old child exit")
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func environmentMap(env []string) map[string]string {
	result := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		result[key] = value
	}
	return result
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		runtime.Gosched()
	}
}

func waitForLines(t *testing.T, path string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		if strings.Count(string(data), "\n") >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d lines in %s", count, path)
		}
		runtime.Gosched()
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
