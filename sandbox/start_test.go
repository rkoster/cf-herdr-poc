package sandbox_test

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLauncherContract(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "start-bash.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lines := executableLines(string(contents))
	ordered := []*regexp.Regexp{
		regexp.MustCompile(`^"\$BIN_DIR/herdr" server &$`),
		regexp.MustCompile(`^"\$BIN_DIR/sandbox-bootstrap" &$`),
		regexp.MustCompile(`^wait "\$bootstrap_pid" \|\| true$`),
		regexp.MustCompile(`^\(exec "\$BIN_DIR/bun" run "\$COLLIE_DIR/bridge/index\.ts"\) &$`),
	}
	position := -1
	for _, operation := range ordered {
		next := matchingLine(lines, operation)
		if next < 0 {
			t.Fatalf("launcher does not contain executable command %q", operation)
		}
		if next <= position {
			t.Fatalf("%q is not after the previous operation", operation)
		}
		position = next
	}
	script := strings.Join(lines, "\n")
	for _, required := range []string{
		"export HOME=/home/vcap",
		"export SHELL=/bin/bash",
		"export PATH=\"$BIN_DIR:$PATH\"",
		`local bashrc="$HOME/.bashrc"`,
		`SANDBOX_STATE_DIR="${SANDBOX_STATE_DIR:-/home/vcap/app/.sandbox-state}"`,
		`export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"`,
		`export XDG_STATE_HOME="${XDG_STATE_HOME:-$SANDBOX_STATE_DIR/state}"`,
		`export XDG_DATA_HOME="${XDG_DATA_HOME:-$SANDBOX_STATE_DIR/data}"`,
		`export COLLIE_STATE_DIR="${COLLIE_STATE_DIR:-$XDG_STATE_HOME/collie}"`,
		`export HERDR_PLUGIN_CONFIG_DIR="${HERDR_PLUGIN_CONFIG_DIR:-$XDG_CONFIG_HOME/collie}"`,
		`export HERDR_SOCKET_PATH="${HERDR_SOCKET_PATH:-$SANDBOX_STATE_DIR/herdr.sock}"`,
		"printf 'export SHELL=/bin/bash\\n'",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("launcher does not contain %q", required)
		}
	}
	configDir := strings.Index(script, `"$HOME/.config/opencode"`)
	integration := strings.Index(script, `"$BIN_DIR/herdr" integration install opencode`)
	herdr := strings.Index(script, `"$BIN_DIR/herdr" server &`)
	if configDir < 0 || integration < 0 || herdr < 0 {
		t.Fatal("launcher is missing OpenCode integration setup")
	}
	if configDir >= integration || integration >= herdr {
		t.Fatalf("OpenCode integration setup is out of order: config=%d integration=%d herdr=%d", configDir, integration, herdr)
	}
	if strings.Contains(script, "SANDBOX_HOME") {
		t.Fatal("launcher must not support SANDBOX_HOME")
	}

	if strings.Contains(script, `pack join`) {
		t.Fatal("launcher performs enrollment before route trigger")
	}
	for _, required := range []string{`bootstrap_pid=""`, `SANDBOX_BOOTSTRAP_READY_FILE`, `export COLLIE_PACK_TRUST_STORE="$COLLIE_STATE_DIR/pack-trust.json"`, `trust_store="$COLLIE_PACK_TRUST_STORE"`, `kill "$bootstrap_pid"`, `wait "$bootstrap_pid" || true`, `if [[ ! -f "$trust_store" ]]`} {
		if !strings.Contains(script, required) {
			t.Fatalf("launcher missing %q", required)
		}
	}
	if strings.Contains(script, "echo $COLLIE_JOIN_TOKEN") || strings.Contains(script, "set -x") {
		t.Fatal("launcher may print the token")
	}
	for _, required := range []string{"trap cleanup", "SANDBOX_STATE_DIR=", "HOME=", "XDG_CONFIG_HOME=", "XDG_STATE_HOME=", "XDG_DATA_HOME=", "COLLIE_STATE_DIR=", "HERDR_PLUGIN_CONFIG_DIR=", "HERDR_SOCKET_PATH", "PATH=", "CF HERDR SANDBOX RUNTIME", "COLLIE_PLUGIN_ROOT=", "COLLIE_PORT=", "COLLIE_HOST=", "COLLIE_MUX=", "COLLIE_PACK_TRANSPORT="} {
		if !strings.Contains(script, required) {
			t.Errorf("launcher does not contain %q", required)
		}
	}
	for _, required := range []string{
		`collie_pid=""`,
		`kill -"$signal" "$collie_pid"`,
		`wait "$collie_pid"`,
		`trap 'terminate TERM 143' TERM`,
		`trap 'terminate INT 130' INT`,
		"trap cleanup EXIT",
		"status=$?",
		`exit "$status"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("launcher does not contain child cleanup contract %q", required)
		}
	}
	terminateStart := strings.Index(script, "terminate() {")
	terminateEnd := strings.Index(script, "trap 'terminate TERM 143' TERM")
	if terminateStart < 0 || terminateEnd < 0 || terminateEnd <= terminateStart {
		t.Fatal("launcher does not define terminate handler before signal traps")
	}
	terminate := script[terminateStart:terminateEnd]
	if strings.Index(terminate, `kill -"$signal" "$collie_pid"`) < 0 || strings.Index(terminate, `wait "$collie_pid"`) < 0 {
		t.Fatal("signal handler does not terminate and wait for Collie")
	}
	if trapDisabled := strings.Index(script, "trap - EXIT"); trapDisabled >= 0 && strings.Index(script, `wait "$collie_pid"`) > trapDisabled {
		t.Fatal("launcher disables cleanup trap before Collie exits")
	}
}

func TestLauncherEntryPointIsStagingSafe(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(readLauncherPath(t)), "start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	if !strings.HasPrefix(script, "#!/bin/sh\n") {
		t.Fatalf("start.sh must use /bin/sh, got %q", script)
	}
	if !strings.Contains(script, `exec /bin/bash "$SCRIPT_DIR/start-bash.sh"`) {
		t.Fatalf("start.sh must exec the absolute Bash launcher, got %q", script)
	}
}

func TestLauncherMaintainsIdempotentBashrcRuntimeBlock(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		runLauncherHelper()
		return
	}

	root := prepareLauncher(t)
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	bashrc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(bashrc, []byte("export UNRELATED=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	ignoredHome := filepath.Join(root, "ignored-home")
	for range 2 {
		command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
		command.Env = append(environmentWithout("HERDR_SOCKET_PATH"), "SANDBOX_LAUNCHER_HELPER=1", "SANDBOX_STATE_DIR="+stateDir, "SIGNAL_LOG="+filepath.Join(root, "signals.log"), "SANDBOX_HOME="+ignoredHome, "PORT=8080")
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		waitForLog(t, filepath.Join(root, "signals.log"), "bun started")
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
	}
	contents, err := os.ReadFile(bashrc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if strings.Count(text, "# BEGIN CF HERDR SANDBOX RUNTIME") != 1 || strings.Count(text, "# END CF HERDR SANDBOX RUNTIME") != 1 {
		t.Fatalf("bashrc = %q, want one managed block", text)
	}
	if !strings.Contains(text, "export UNRELATED=value") || !strings.Contains(text, "export PATH="+filepath.Join(root, "bin")+":$PATH") || !strings.Contains(text, "export HERDR_SOCKET_PATH="+filepath.Join(stateDir, "herdr.sock")) || !strings.Contains(text, "export SHELL=/bin/bash") {
		t.Fatalf("bashrc = %q, unrelated or socket settings missing", text)
	}
	if _, err := os.Stat(filepath.Join(ignoredHome, ".bashrc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SANDBOX_HOME affected launcher shell configuration: %v", err)
	}
	if strings.Count(text, "export SHELL=/bin/bash") != 1 {
		t.Fatalf("bashrc = %q, want one managed shell setting", text)
	}
}

func TestLauncherExportsCFPeerRuntimeBeforeBootstrapAndCollie(t *testing.T) {
	lines := executableLines(readLauncher(t))
	script := strings.Join(lines, "\n")
	required := []string{
		`export COLLIE_PLUGIN_ROOT="$COLLIE_DIR"`,
		`export COLLIE_PORT="${PORT:?Cloud Foundry PORT is required}"`,
		`export COLLIE_HOST=0.0.0.0`,
		`export COLLIE_ALLOW_NON_LOOPBACK_BIND=1`,
		`export COLLIE_PACK_TRANSPORT=cf-identity`,
	}
	bootstrap := strings.Index(script, `"$BIN_DIR/sandbox-bootstrap" &`)
	collie := strings.Index(script, `(exec "$BIN_DIR/bun" run "$COLLIE_DIR/bridge/index.ts") &`)
	for _, assignment := range required {
		position := strings.Index(script, assignment)
		if position < 0 || position > bootstrap || position > collie {
			t.Fatalf("launcher must export %q before bootstrap and Collie", assignment)
		}
	}
}

func TestLauncherPassesJoinInputsToBootstrapAndConsumesToken(t *testing.T) {
	lines := executableLines(readLauncher(t))
	script := strings.Join(lines, "\n")
	for _, required := range []string{
		`COLLIE_JOIN_TOKEN_FILE`,
		`COLLIE_PACK_LEAD_ADDRESS`,
		`COLLIE_PACK_SELF_ADDRESS`,
		`export COLLIE_EXECUTABLE="$BIN_DIR/collie"`,
		`SANDBOX_MEMBER_ID`,
		`export COLLIE_PACK_TRUST_STORE="$COLLIE_STATE_DIR/pack-trust.json"`,
		`rm -f -- "${COLLIE_JOIN_TOKEN_FILE:-}"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("launcher missing join bootstrap contract %q", required)
		}
	}
}

func TestExecutableLinesIgnoreComments(t *testing.T) {
	lines := executableLines("\n# herdr server\n  # collie pack join\n# bun run bridge/index.ts\n")
	if len(lines) != 0 {
		t.Fatalf("executableLines returned comments: %q", lines)
	}
}

func TestLauncherForwardsSignalsAndReapsChildren(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		runLauncherHelper()
		return
	}

	for _, test := range []struct {
		name       string
		signal     syscall.Signal
		signalName string
		exitCode   int
	}{
		{name: "interrupt", signal: syscall.SIGINT, signalName: "interrupt", exitCode: 130},
		{name: "terminate", signal: syscall.SIGTERM, signalName: "terminated", exitCode: 143},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := prepareLauncher(t)
			state := filepath.Join(root, "state")
			socket := shortLauncherSocket(t)
			logPath := filepath.Join(root, "signals.log")
			command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
			command.Env = append(os.Environ(),
				"SANDBOX_LAUNCHER_HELPER=1",
				"SANDBOX_STATE_DIR="+state,
				"HERDR_SOCKET_PATH="+socket,
				"SIGNAL_LOG="+logPath,
				"SANDBOX_MEMBER_ID=demo",
				"PORT=8080",
				"COLLIE_PACK_LEAD_ADDRESS=https://manager.identity.example",
				"COLLIE_JOIN_TOKEN_FILE="+filepath.Join(root, "token"),
			)
			if err := os.WriteFile(filepath.Join(root, "token"), []byte("token\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waitForLog(t, logPath, "bun started")
			if err := command.Process.Signal(test.signal); err != nil {
				t.Fatal(err)
			}
			err := command.Wait()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != test.exitCode {
				t.Fatalf("launcher error = %v, exit = %d, want %d", err, exitCode(err), test.exitCode)
			}
			log := waitForLog(t, logPath, "herdr terminated")
			if !strings.Contains(log, "bun "+test.signalName) {
				t.Fatalf("signal log = %q, want Bun %s", log, test.signalName)
			}
			assertReaped(t, log, "bun")
			assertReaped(t, log, "herdr")
		})
	}
}

func TestLauncherStartsStandaloneWithoutBootstrap(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		runLauncherHelper()
		return
	}

	root := prepareLauncher(t)
	logPath := filepath.Join(root, "signals.log")
	command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
	command.Env = append(os.Environ(), "SANDBOX_LAUNCHER_HELPER=1", "SANDBOX_STATE_DIR="+filepath.Join(root, "state"), "HERDR_SOCKET_PATH="+shortLauncherSocket(t), "SIGNAL_LOG="+logPath, "EXPECTED_HOME="+filepath.Join(root, "home"), "PORT=8080")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, logPath, "bun started")
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "bootstrap started") {
		t.Fatalf("standalone launcher invoked bootstrap: %s", log)
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	_ = command.Wait()
}

func TestLauncherInstallsOpenCodeIntegrationBeforeStartingChildren(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		runLauncherHelper()
		return
	}

	root := prepareLauncher(t)
	logPath := filepath.Join(root, "signals.log")
	command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
	command.Env = append(os.Environ(), "SANDBOX_LAUNCHER_HELPER=1", "SANDBOX_STATE_DIR="+filepath.Join(root, "state"), "HERDR_SOCKET_PATH="+shortLauncherSocket(t), "SIGNAL_LOG="+logPath, "PORT=8080")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, logPath, "integration installed")
	waitForLog(t, logPath, "bun started")
	_ = command.Process.Signal(syscall.SIGTERM)
	_ = command.Wait()
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(log)
	if strings.Index(text, "integration installed") >= strings.Index(text, "bun started") {
		t.Fatalf("integration installation did not precede Bun startup: %s", text)
	}
}

func TestLauncherRejectsPartialEnrollmentWithoutStartingChildren(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		runLauncherHelper()
		return
	}

	root := prepareLauncher(t)
	secret := "join-token-must-not-leak"
	command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
	command.Env = append(os.Environ(), "SANDBOX_STATE_DIR="+filepath.Join(root, "state"), "HERDR_SOCKET_PATH="+shortLauncherSocket(t), "SIGNAL_LOG="+filepath.Join(root, "signals.log"), "PORT=8080", "COLLIE_JOIN_TOKEN_FILE="+filepath.Join(root, secret))
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("partial enrollment unexpectedly succeeded")
	}
	if !strings.Contains(string(out), "must be supplied together") {
		t.Fatalf("output = %q, want clear partial enrollment error", out)
	}
	if strings.Contains(string(out), secret) {
		t.Fatalf("output leaked enrollment token path: %q", out)
	}
	if log, readErr := os.ReadFile(filepath.Join(root, "signals.log")); readErr == nil && len(log) != 0 {
		t.Fatalf("partial enrollment started children: %s", log)
	}
}

func TestLauncherUsesRegularTrustStoreWhenMarkerIsMissing(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		return
	}
	root := prepareLauncher(t)
	state := filepath.Join(root, "state")
	logPath := filepath.Join(root, "signals.log")
	token := filepath.Join(root, "token")
	if err := os.WriteFile(token, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
	command.Env = append(os.Environ(), "SANDBOX_LAUNCHER_HELPER=1", "SANDBOX_BOOTSTRAP_TRUST_ONLY=1", "SANDBOX_STATE_DIR="+state, "HERDR_SOCKET_PATH="+shortLauncherSocket(t), "SIGNAL_LOG="+logPath, "COLLIE_JOIN_TOKEN_FILE="+token, "COLLIE_PACK_LEAD_ADDRESS=https://manager.identity.example", "SANDBOX_MEMBER_ID=demo", "PORT=8080")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, logPath, "bun started")
	if _, err := os.Stat(token); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale token remains: %v", err)
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	_ = command.Wait()
}

func TestLauncherRejectsSymlinkTrustStore(t *testing.T) {
	if os.Getenv("SANDBOX_LAUNCHER_HELPER") != "" {
		return
	}
	root := prepareLauncher(t)
	state := filepath.Join(root, "state")
	trustDir := filepath.Join(state, "state", "collie")
	if err := os.MkdirAll(trustDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(trustDir, "pack-trust.json")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", filepath.Join(root, "start-bash.sh"))
	command.Env = append(os.Environ(), "SANDBOX_LAUNCHER_HELPER=1", "SANDBOX_STATE_DIR="+state, "HERDR_SOCKET_PATH="+shortLauncherSocket(t), "SIGNAL_LOG="+filepath.Join(root, "signals.log"), "PORT=8080", "COLLIE_JOIN_TOKEN_FILE="+filepath.Join(root, "token"), "COLLIE_PACK_LEAD_ADDRESS=https://manager.identity.example")
	err := command.Run()
	if err == nil {
		t.Fatal("launcher accepted symlink trust store")
	}
}

func TestShortLauncherSocketIsUnixPathLengthSafe(t *testing.T) {
	path := shortLauncherSocket(t)
	if len(path) >= 100 {
		t.Fatalf("socket path is too long: %d bytes: %s", len(path), path)
	}
}

func shortLauncherSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

func prepareLauncher(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := readLauncher(t)
	launcher = strings.Replace(launcher, "export HOME=/home/vcap", "export HOME="+home, 1)
	if err := os.WriteFile(filepath.Join(root, "start-bash.sh"), []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	entrypoint, err := os.ReadFile(filepath.Join(filepath.Dir(readLauncherPath(t)), "start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "start.sh"), entrypoint, 0o755); err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"herdr", "bun", "collie", "sandbox-bootstrap"} {
		wrapper := fmt.Sprintf("#!/usr/bin/env bash\nSANDBOX_HELPER_ROLE=%q exec %q -test.run=TestLauncherForwardsSignalsAndReapsChildren -- \"$@\"\n", role, testBinary)
		if err := os.WriteFile(filepath.Join(root, "bin", role), []byte(wrapper), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readLauncher(t *testing.T) string {
	t.Helper()
	return readLauncherFile(t, "start-bash.sh")
}

func readLauncherPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filename
}

func readLauncherFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(readLauncherPath(t)), name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func environmentWithout(keys ...string) []string {
	var environment []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		keep := true
		for _, key := range keys {
			if name == key {
				keep = false
				break
			}
		}
		if keep {
			environment = append(environment, entry)
		}
	}
	return environment
}

func runLauncherHelper() {
	role := os.Getenv("SANDBOX_HELPER_ROLE")
	if role == "herdr" {
		separator := -1
		for index, argument := range os.Args {
			if argument == "--" {
				separator = index
				break
			}
		}
		arguments := os.Args[separator+1:]
		if separator >= 0 && len(arguments) > 0 && arguments[0] == "integration" {
			if len(arguments) != 3 || arguments[1] != "install" || arguments[2] != "opencode" || (os.Getenv("EXPECTED_HOME") != "" && os.Getenv("HOME") != os.Getenv("EXPECTED_HOME")) {
				os.Exit(2)
			}
			logLine(os.Getenv("SIGNAL_LOG"), "integration installed")
			return
		}
	}
	if role == "sandbox-bootstrap" {
		logLine(os.Getenv("SIGNAL_LOG"), "bootstrap started")
		for key, want := range map[string]string{"COLLIE_PLUGIN_ROOT": filepath.Join(filepath.Dir(os.Getenv("SIGNAL_LOG")), "collie"), "COLLIE_PORT": "8080", "COLLIE_HOST": "0.0.0.0", "COLLIE_ALLOW_NON_LOOPBACK_BIND": "1", "COLLIE_PACK_TRANSPORT": "cf-identity", "COLLIE_JOIN_TOKEN_FILE": filepath.Join(filepath.Dir(os.Getenv("SIGNAL_LOG")), "token"), "COLLIE_PACK_LEAD_ADDRESS": "https://manager.identity.example", "SANDBOX_MEMBER_ID": "demo"} {
			if os.Getenv(key) != want {
				os.Exit(2)
			}
		}
		if os.Getenv("SANDBOX_BOOTSTRAP_TRUST_ONLY") != "" {
			path := os.Getenv("COLLIE_PACK_TRUST_STORE")
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			_ = os.WriteFile(path, []byte("opaque\n"), 0o600)
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
			<-signals
			os.Exit(0)
		}
		ready := os.Getenv("SANDBOX_BOOTSTRAP_READY_FILE")
		if ready == "" {
			os.Exit(1)
		}
		_ = os.WriteFile(ready, []byte("ready\n"), 0o600)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		os.Exit(0)
	}
	if role != "herdr" && role != "bun" {
		os.Exit(0)
	}
	logPath := os.Getenv("SIGNAL_LOG")
	logLine(logPath, fmt.Sprintf("%s pid %d", role, os.Getpid()))
	var listener net.Listener
	if role == "herdr" {
		var err error
		listener, err = net.Listen("unix", os.Getenv("HERDR_SOCKET_PATH"))
		if err != nil {
			logLine(logPath, "herdr listen failed: "+err.Error())
			os.Exit(1)
		}
		defer listener.Close()
	} else {
		for key, want := range map[string]string{"COLLIE_PORT": "8080", "COLLIE_HOST": "0.0.0.0", "COLLIE_ALLOW_NON_LOOPBACK_BIND": "1", "COLLIE_PACK_TRANSPORT": "cf-identity"} {
			if os.Getenv(key) != want {
				os.Exit(2)
			}
		}
		logLine(logPath, "bun started")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	received := <-signals
	logLine(logPath, role+" "+received.String())
}

func logLine(path, line string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(1)
	}
	fmt.Fprintln(file, line)
	file.Close()
}

func waitForLog(t *testing.T, path, wanted string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, _ := os.ReadFile(path)
		if strings.Contains(string(contents), wanted) {
			return string(contents)
		}
		time.Sleep(10 * time.Millisecond)
	}
	contents, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %q in %q", wanted, contents)
	return ""
}

func assertReaped(t *testing.T, log, role string) {
	t.Helper()
	match := regexp.MustCompile(role + ` pid (\d+)`).FindStringSubmatch(log)
	if len(match) != 2 {
		t.Fatalf("missing %s pid in %q", role, log)
	}
	pid, _ := strconv.Atoi(match[1])
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("%s process %d still exists: %v", role, pid, err)
	}
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 0
}

func executableLines(script string) []string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func matchingLine(lines []string, pattern *regexp.Regexp) int {
	for index, line := range lines {
		if pattern.MatchString(line) {
			return index
		}
	}
	return -1
}
