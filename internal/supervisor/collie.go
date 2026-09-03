package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var ErrChildExited = errors.New("collie child exited")

type Collie interface {
	Start(context.Context) error
	Restart(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}

type Config struct {
	Executable    string
	Args          []string
	Dir           string
	ConfigDir     string
	StateDir      string
	Host          string
	Port          int
	Stdout        io.Writer
	Stderr        io.Writer
	ReadyTimeout  time.Duration
	ProbeInterval time.Duration
	StopTimeout   time.Duration
}

type ProcessConfig struct {
	Name   string
	Args   []string
	Dir    string
	Env    map[string]string
	Stdout io.Writer
	Stderr io.Writer
}

type Process interface {
	Start() error
	Wait() error
	Stop(time.Duration) error
}

type ProcessFactory func(ProcessConfig) Process
type Probe func(context.Context, string) error

type Supervisor struct {
	config  Config
	factory ProcessFactory
	probe   Probe
	mu      sync.Mutex
	process Process
	exited  chan error
}

func New(config Config, factory ProcessFactory, probe Probe) *Supervisor {
	if config.Executable == "" {
		config.Executable = "bun"
	}
	if len(config.Args) == 0 {
		config.Args = []string{"run", "bridge/index.ts"}
	}
	if config.Host == "" {
		config.Host = "127.0.0.1"
	}
	if config.Port == 0 {
		config.Port = 8787
	}
	if config.Stdout == nil {
		config.Stdout = io.Discard
	}
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = 10 * time.Second
	}
	if config.ProbeInterval <= 0 {
		config.ProbeInterval = 50 * time.Millisecond
	}
	if config.StopTimeout <= 0 {
		config.StopTimeout = 5 * time.Second
	}
	if factory == nil {
		factory = func(c ProcessConfig) Process { return newExecProcess(c) }
	}
	if probe == nil {
		probe = httpProbe
	}
	return &Supervisor{config: config, factory: factory, probe: probe}
}

func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked(ctx)
}

func (s *Supervisor) startLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.process != nil {
		return errors.New("collie is already running")
	}
	env := map[string]string{
		"HOME": s.config.ConfigDir, "XDG_CONFIG_HOME": s.config.ConfigDir,
		"COLLIE_STATE_DIR": s.config.StateDir, "COLLIE_HOST": s.config.Host,
		"COLLIE_PORT": strconv.Itoa(s.config.Port),
	}
	process := s.factory(ProcessConfig{Name: s.config.Executable, Args: append([]string(nil), s.config.Args...), Dir: s.config.Dir, Env: env, Stdout: s.config.Stdout, Stderr: s.config.Stderr})
	if err := process.Start(); err != nil {
		return fmt.Errorf("start collie: %w", err)
	}
	exited := make(chan error, 1)
	s.process, s.exited = process, exited
	go func() { exited <- process.Wait(); close(exited) }()
	return nil
}

func (s *Supervisor) Restart(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.stopLocked(ctx); err != nil {
		return err
	}
	return s.startLocked(ctx)
}

func (s *Supervisor) Ready(ctx context.Context) error {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited == nil {
		return errors.New("collie is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.ReadyTimeout)
	defer cancel()
	url := fmt.Sprintf("http://%s:%d/api/snapshot", s.config.Host, s.config.Port)
	ticker := time.NewTicker(s.config.ProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			if err == nil {
				err = errors.New("unexpected clean exit")
			}
			return fmt.Errorf("%w: %v", ErrChildExited, err)
		default:
		}
		if err := s.probe(ctx, url); err == nil {
			return nil
		}
		select {
		case err := <-exited:
			if err == nil {
				err = errors.New("unexpected clean exit")
			}
			return fmt.Errorf("%w: %v", ErrChildExited, err)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked(ctx)
}

func (s *Supervisor) stopLocked(ctx context.Context) error {
	if s.process == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	process, exited := s.process, s.exited
	if err := process.Stop(s.config.StopTimeout); err != nil {
		return fmt.Errorf("stop collie: %w", err)
	}
	select {
	case <-exited:
		s.process, s.exited = nil, nil
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.config.StopTimeout):
		return errors.New("timed out reaping collie")
	}
}

func httpProbe(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("snapshot returned %s", response.Status)
	}
	return nil
}

type execProcess struct {
	command *exec.Cmd
	wait    chan error
}

func newExecProcess(config ProcessConfig) *execProcess {
	command := exec.Command(config.Name, config.Args...)
	command.Dir, command.Stdout, command.Stderr = config.Dir, config.Stdout, config.Stderr
	command.Env = os.Environ()
	for key, value := range config.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &execProcess{command: command, wait: make(chan error, 1)}
}

func (p *execProcess) Start() error { return p.command.Start() }
func (p *execProcess) Wait() error  { return p.command.Wait() }
func (p *execProcess) Stop(timeout time.Duration) error {
	if p.command.Process == nil {
		return nil
	}
	pgid := p.command.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
