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
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "start.sh"))
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

	if strings.Contains(script, `pack join`) {
		t.Fatal("launcher performs enrollment before route trigger")
	}
	for _, required := range []string{`bootstrap_pid=""`, `SANDBOX_BOOTSTRAP_READY_FILE`, `kill "$bootstrap_pid"`, `wait "$bootstrap_pid" || true`, `if [[ ! -f "$trust_store" ]]`} {
		if !strings.Contains(script, required) {
			t.Fatalf("launcher missing %q", required)
		}
	}
	if strings.Contains(script, "echo $COLLIE_JOIN_TOKEN") || strings.Contains(script, "set -x") {
		t.Fatal("launcher may print the token")
	}
	for _, required := range []string{"trap cleanup", "HOME=", "XDG_CONFIG_HOME=", "XDG_STATE_HOME=", "HERDR_SOCKET_PATH"} {
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
			logPath := filepath.Join(root, "signals.log")
			command := exec.Command("bash", filepath.Join(root, "start.sh"))
			command.Env = append(os.Environ(),
				"SANDBOX_LAUNCHER_HELPER=1",
				"SANDBOX_STATE_DIR="+state,
				"SIGNAL_LOG="+logPath,
				"SANDBOX_MEMBER_ID=demo",
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

func prepareLauncher(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := readLauncher(t)
	if err := os.WriteFile(filepath.Join(root, "start.sh"), []byte(launcher), 0o755); err != nil {
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
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func runLauncherHelper() {
	role := os.Getenv("SANDBOX_HELPER_ROLE")
	if role == "sandbox-bootstrap" {
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
