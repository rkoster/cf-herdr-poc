package pack

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"cf-herdr-poc/internal/runner"
	"cf-herdr-poc/internal/supervisor"
)

type call struct {
	name string
	args []string
	env  []string
}
type fakeRunner struct {
	output []byte
	err    error
	calls  []call
}

func (r *fakeRunner) RunEnv(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)})
	return append([]byte(nil), r.output...), r.err
}

type fakeSupervisor struct {
	restarts int
	err      error
}

func (*fakeSupervisor) Start(context.Context) error     { return nil }
func (s *fakeSupervisor) Restart(context.Context) error { s.restarts++; return s.err }
func (*fakeSupervisor) Ready(context.Context) error     { return nil }
func (*fakeSupervisor) Stop(context.Context) error      { return nil }

const invite = "uY4Jf9P2_abc-DEF.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestPrepareEnrollmentCreatesPrivateTokenFileAndRestarts(t *testing.T) {
	temp := t.TempDir()
	r := &fakeRunner{output: []byte(invite + "\n\n  single-use · expires 2026-09-03T18:00:00.000Z (10 minutes)\n")}
	s := &fakeSupervisor{}
	m := New(r, s, Config{Executable: "collie", TempDir: temp, PluginRoot: "/manager/collie", ConfigDir: "/manager/config", StateDir: "/manager/state", SocketPath: "/manager/herdr.sock", Port: 8787})
	handle, err := m.PrepareEnrollment(context.Background(), "pack.apps.example", "sandbox-a")
	if err != nil {
		t.Fatal(err)
	}
	if r.calls[0].name != "collie" || !reflect.DeepEqual(r.calls[0].args, []string{"pack", "invite", "--address", "https://pack.apps.example"}) {
		t.Fatalf("calls = %#v", r.calls)
	}
	env := environmentMap(r.calls[0].env)
	if env["COLLIE_PLUGIN_ROOT"] != "/manager/collie" || env["HERDR_PLUGIN_CONFIG_DIR"] != "/manager/config" || env["HERDR_PLUGIN_STATE_DIR"] != "/manager/state" || env["COLLIE_STATE_DIR"] != "/manager/state" || env["HERDR_SOCKET_PATH"] != "/manager/herdr.sock" || env["COLLIE_HOST"] != "127.0.0.1" || env["COLLIE_PORT"] != "8787" {
		t.Fatalf("CLI environment = %#v", env)
	}
	if s.restarts != 1 {
		t.Fatalf("restarts = %d", s.restarts)
	}
	info, err := os.Stat(handle.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	contents, _ := os.ReadFile(handle.Path)
	if string(contents) != invite+"\n" {
		t.Fatalf("token file = %q", contents)
	}
	if handle.ExpiresAt.IsZero() {
		t.Fatal("expiry was not parsed")
	}
	if err := handle.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(handle.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token still exists: %v", err)
	}
}

func TestPackCLIUsesConfiguredLoopbackHost(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n")}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir(), Host: "127.0.0.2", Port: 8787})
	handle, err := m.PrepareEnrollment(context.Background(), "pack.example", "sandbox-a")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Cleanup()
	if got := environmentMap(r.calls[0].env)["COLLIE_HOST"]; got != "127.0.0.2" {
		t.Fatalf("COLLIE_HOST = %q", got)
	}
}

func TestPrepareEnrollmentRedactsTokenFromErrorsAndCleansUp(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n"), err: errors.New("failed: " + invite)}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir()})
	_, err := m.PrepareEnrollment(context.Background(), "pack.example", "sandbox-a")
	if err == nil || strings.Contains(err.Error(), invite) {
		t.Fatalf("error leaked token: %v", err)
	}
	entries, _ := os.ReadDir(m.config.TempDir)
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}

func TestEnrollmentFileHasBoundedLifetime(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n")}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir(), TokenLifetime: 15 * time.Millisecond})
	h, err := m.PrepareEnrollment(context.Background(), "pack.example", "sandbox-a")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, err = os.Stat(h.Path)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("token file outlived configured lifetime")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEnrollmentCleanupRetriesAfterRemoveFailure(t *testing.T) {
	attempts := 0
	handle := newEnrollment("token-file", time.Hour, func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary remove failure")
		}
		return nil
	})
	if err := handle.Cleanup(); err == nil {
		t.Fatal("first cleanup succeeded")
	}
	if err := handle.Cleanup(); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	if err := handle.Cleanup(); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("remove attempts = %d", attempts)
	}
}

func TestEnrollmentExpiryRetriesAfterRemoveFailure(t *testing.T) {
	attempts := make(chan int, 2)
	count := 0
	handle := newEnrollment("token-file", 5*time.Millisecond, func(string) error {
		count++
		attempts <- count
		if count == 1 {
			return errors.New("temporary remove failure")
		}
		return nil
	})
	for want := 1; want <= 2; want++ {
		select {
		case got := <-attempts:
			if got != want {
				t.Fatalf("attempt = %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for remove attempt %d", want)
		}
	}
	if err := handle.Cleanup(); err != nil {
		t.Fatalf("cleanup after expiry: %v", err)
	}
}

func TestMemberPresentUsesNoProbeAndNarrowRosterParsing(t *testing.T) {
	r := &fakeRunner{output: []byte("pack p (id)\nmembers:\n  sandbox-a  (peer)  sandbox.example\n    pinned abc\n")}
	m := New(r, &fakeSupervisor{}, Config{Executable: "collie", TempDir: t.TempDir()})
	present, err := m.MemberPresent(context.Background(), "sandbox-a")
	if err != nil || !present {
		t.Fatalf("present = %v, err = %v", present, err)
	}
	if !reflect.DeepEqual(r.calls[0].args, []string{"pack", "status", "--no-probe"}) {
		t.Fatalf("args = %v", r.calls[0].args)
	}
}

func TestRemoveMemberValidatesIDAndRestarts(t *testing.T) {
	r := &fakeRunner{}
	s := &fakeSupervisor{}
	m := New(r, s, Config{Executable: "collie", TempDir: t.TempDir()})
	if err := m.RemoveMember(context.Background(), "bad id; rm -rf"); err == nil {
		t.Fatal("invalid member ID accepted")
	}
	if len(r.calls) != 0 {
		t.Fatal("runner called for invalid member ID")
	}
	if err := m.RemoveMember(context.Background(), "sandbox-a"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls[0].args, []string{"pack", "remove", "sandbox-a"}) {
		t.Fatalf("args = %v", r.calls[0].args)
	}
	if s.restarts != 1 {
		t.Fatalf("restarts = %d", s.restarts)
	}
}

func TestMemberIDMatchesCollieContract(t *testing.T) {
	valid := []string{"a", "0", strings.Repeat("a", 63), "sandbox-1"}
	invalid := []string{"A", "a.b", "a_b", strings.Repeat("a", 64), "-a", "a-" + strings.Repeat("b", 62)}
	for _, id := range valid {
		if err := validateMemberID(id); err != nil {
			t.Errorf("valid %q rejected: %v", id, err)
		}
	}
	for _, id := range invalid {
		if err := validateMemberID(id); err == nil {
			t.Errorf("invalid %q accepted", id)
		}
	}
}

func TestCapturedCollieOutputFixtureExercisesParsers(t *testing.T) {
	fixture, err := os.ReadFile("testdata/collie-pack-output.txt")
	if err != nil {
		t.Fatal(err)
	}
	parsedInvite, expiry := parseInvite(fixture)
	if parsedInvite != invite || expiry.IsZero() {
		t.Fatalf("parseInvite() = %q, %v", parsedInvite, expiry)
	}
	r := &fakeRunner{output: fixture}
	present, err := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir()}).MemberPresent(context.Background(), "sandbox-a")
	if err != nil || !present {
		t.Fatalf("MemberPresent() = %v, %v", present, err)
	}
}

func TestBridgeAndCLIUseIdenticalManagedRuntimeEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable fixture")
	}
	temp := t.TempDir()
	configDir, stateDir := filepath.Join(temp, "config"), filepath.Join(temp, "state")
	for _, dir := range []string{configDir, stateDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(temp, "fake-collie")
	bridgeEnv, cliEnv := filepath.Join(temp, "bridge.env"), filepath.Join(temp, "cli.env")
	scriptBody := `#!/bin/sh
set -eu
case "${1:-}" in
  bridge) record='` + bridgeEnv + `' ;;
  pack) record='` + cliEnv + `' ;;
  *) exit 64 ;;
esac
{
  printf 'HERDR_PLUGIN_CONFIG_DIR=%s\n' "$HERDR_PLUGIN_CONFIG_DIR"
  printf 'HERDR_PLUGIN_STATE_DIR=%s\n' "$HERDR_PLUGIN_STATE_DIR"
	  printf 'COLLIE_STATE_DIR=%s\n' "$COLLIE_STATE_DIR"
	  printf 'COLLIE_PLUGIN_ROOT=%s\n' "$COLLIE_PLUGIN_ROOT"
  printf 'HERDR_SOCKET_PATH=%s\n' "$HERDR_SOCKET_PATH"
  printf 'COLLIE_HOST=%s\n' "$COLLIE_HOST"
  printf 'COLLIE_PORT=%s\n' "$COLLIE_PORT"
  printf 'COLLIE_PACK_TRANSPORT=%s\n' "$COLLIE_PACK_TRANSPORT"
  printf 'TRUST_PATH=%s\n' "$HERDR_PLUGIN_STATE_DIR/pack-trust.json"
} > "$record"
if [ "${1:-}" = bridge ]; then
  trap 'exit 0' TERM
  while :; do sleep 1; done
fi
[ "$*" = 'pack invite --address https://pack.example' ] || exit 65
printf '%s\n\n  single-use · expires 2026-09-03T18:00:00.000Z (10 minutes)\n' '` + invite + `'
`
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(temp, "collie")
	if err := os.Mkdir(pluginRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	s := supervisor.New(supervisor.Config{Executable: script, Args: []string{"bridge"}, Dir: pluginRoot, PluginRoot: pluginRoot, ConfigDir: configDir, StateDir: stateDir, SocketPath: filepath.Join(temp, "herdr.sock"), Port: 8787, PackTransport: "cf-identity", StopTimeout: time.Second, Stdout: io.Discard, Stderr: io.Discard}, nil, func(context.Context, string) error { return nil })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background())
	waitForPath(t, bridgeEnv)
	m := New(runner.Exec{}, s, Config{Executable: script, TempDir: temp, PluginRoot: pluginRoot, ConfigDir: configDir, StateDir: stateDir, SocketPath: filepath.Join(temp, "herdr.sock"), Port: 8787})
	handle, err := m.PrepareEnrollment(context.Background(), "pack.example", "sandbox-a")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Cleanup()
	waitForPath(t, cliEnv)
	bridgeValues, err := os.ReadFile(bridgeEnv)
	if err != nil {
		t.Fatal(err)
	}
	cliValues, err := os.ReadFile(cliEnv)
	if err != nil {
		t.Fatal(err)
	}
	if string(bridgeValues) != string(cliValues) {
		t.Fatalf("bridge environment:\n%s\nCLI environment:\n%s", bridgeValues, cliValues)
	}
	values := string(cliValues)
	if !strings.Contains(values, "COLLIE_HOST=127.0.0.1\n") || !strings.Contains(values, "COLLIE_PORT=8787\n") || !strings.Contains(values, "COLLIE_PACK_TRANSPORT=cf-identity\n") {
		t.Fatalf("manager Collie environment is not loopback CF identity mode:\n%s", values)
	}
	if !strings.Contains(values, "COLLIE_PLUGIN_ROOT="+pluginRoot+"\n") {
		t.Fatalf("manager Collie environment has wrong plugin root:\n%s", values)
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

func waitForPath(t *testing.T, path string) {
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

func TestPrepareEnrollmentUsesUniqueFiles(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n")}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir()})
	a, _ := m.PrepareEnrollment(context.Background(), "pack.example", filepath.Base("sandbox"))
	b, _ := m.PrepareEnrollment(context.Background(), "pack.example", filepath.Base("sandbox"))
	defer a.Cleanup()
	defer b.Cleanup()
	if a.Path == b.Path {
		t.Fatal("token files are not unique")
	}
}
