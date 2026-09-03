package pack

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	collieruntime "cf-herdr-poc/internal/collie"
)

const defaultTokenLifetime = 10 * time.Minute

var (
	memberIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	invitePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[0-9a-fA-F]{64}$`)
	expiryPattern   = regexp.MustCompile(`expires ([0-9]{4}-[0-9]{2}-[0-9]{2}T[^ ]+)`)
	rosterPattern   = regexp.MustCompile(`^  ([a-z0-9][a-z0-9-]{0,62})  \((?:lead|peer)\)  `)
)

type Supervisor interface {
	Start(context.Context) error
	Restart(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}

type Config struct {
	Executable    string
	TempDir       string
	ConfigDir     string
	StateDir      string
	SocketPath    string
	Port          int
	TokenLifetime time.Duration
}

type Runner interface {
	RunEnv(context.Context, []string, string, ...string) ([]byte, error)
}

type Enrollment struct {
	Path      string
	ExpiresAt time.Time
	once      sync.Once
	err       error
}

func (e *Enrollment) Cleanup() error {
	e.once.Do(func() {
		e.err = os.Remove(e.Path)
		if errors.Is(e.err, os.ErrNotExist) {
			e.err = nil
		}
	})
	return e.err
}

type Manager struct {
	runner     Runner
	supervisor Supervisor
	config     Config
}

func New(commandRunner Runner, supervisor Supervisor, config Config) *Manager {
	if config.Executable == "" {
		config.Executable = "collie"
	}
	if config.TokenLifetime <= 0 {
		config.TokenLifetime = defaultTokenLifetime
	}
	if config.Port == 0 {
		config.Port = 8787
	}
	return &Manager{runner: commandRunner, supervisor: supervisor, config: config}
}

func (m *Manager) PrepareEnrollment(ctx context.Context, managerPackHost, sandboxName string) (*Enrollment, error) {
	if err := validateMemberID(sandboxName); err != nil {
		return nil, fmt.Errorf("sandbox name: %w", err)
	}
	address, err := packAddress(managerPackHost)
	if err != nil {
		return nil, err
	}
	output, runErr := m.run(ctx, "pack", "invite", "--address", address)
	invite, expiresAt := parseInvite(output)
	if runErr != nil {
		return nil, errors.New("create Collie Pack invite: command failed")
	}
	if invite == "" {
		return nil, errors.New("create Collie Pack invite: output did not contain an invite")
	}
	file, err := os.CreateTemp(m.config.TempDir, "collie-invite-"+sandboxName+"-*")
	if err != nil {
		return nil, fmt.Errorf("create enrollment file: %w", err)
	}
	path := file.Name()
	cleanup := func() { file.Close(); os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("secure enrollment file: %w", err)
	}
	if _, err := file.WriteString(invite + "\n"); err != nil {
		cleanup()
		return nil, fmt.Errorf("write enrollment file: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("close enrollment file: %w", err)
	}
	if err := m.supervisor.Restart(ctx); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("restart Collie after invite: %w", err)
	}
	handle := &Enrollment{Path: path, ExpiresAt: expiresAt}
	time.AfterFunc(m.config.TokenLifetime, func() { _ = handle.Cleanup() })
	return handle, nil
}

func (m *Manager) MemberPresent(ctx context.Context, id string) (bool, error) {
	if err := validateMemberID(id); err != nil {
		return false, err
	}
	output, err := m.run(ctx, "pack", "status", "--no-probe")
	if err != nil {
		return false, errors.New("read Collie Pack status: command failed")
	}
	for _, line := range strings.Split(string(output), "\n") {
		match := rosterPattern.FindStringSubmatch(line)
		if len(match) == 2 && match[1] == id {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) RemoveMember(ctx context.Context, id string) error {
	if err := validateMemberID(id); err != nil {
		return err
	}
	if _, err := m.run(ctx, "pack", "remove", id); err != nil {
		return errors.New("remove Collie Pack member: command failed")
	}
	if err := m.supervisor.Restart(ctx); err != nil {
		return fmt.Errorf("restart Collie after removal: %w", err)
	}
	return nil
}

func (m *Manager) run(ctx context.Context, args ...string) ([]byte, error) {
	return m.runner.RunEnv(ctx, m.environment(), m.config.Executable, args...)
}

func (m *Manager) environment() []string {
	return collieruntime.Environment(collieruntime.Runtime{ConfigDir: m.config.ConfigDir, StateDir: m.config.StateDir, SocketPath: m.config.SocketPath, Port: m.config.Port}, os.Environ())
}

func validateMemberID(id string) error {
	if !memberIDPattern.MatchString(id) {
		return errors.New("invalid Collie Pack member ID")
	}
	return nil
}

func packAddress(host string) (string, error) {
	if strings.TrimSpace(host) != host || host == "" || strings.Contains(host, "/") {
		return "", errors.New("invalid manager Pack host")
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil {
		return "", errors.New("invalid manager Pack host")
	}
	return parsed.String(), nil
}

func parseInvite(output []byte) (string, time.Time) {
	var invite string
	var expiry time.Time
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if invite == "" && invitePattern.MatchString(trimmed) {
			invite = trimmed
		}
		if match := expiryPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			expiry, _ = time.Parse(time.RFC3339Nano, match[1])
		}
	}
	return invite, expiry
}
