package sandbox_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
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
		regexp.MustCompile(`^"\$BIN_DIR/collie" pack join "\$COLLIE_PACK_LEAD_ADDRESS" - < "\$COLLIE_JOIN_TOKEN_FILE"$`),
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

	if !strings.Contains(script, `"$COLLIE_JOIN_TOKEN_FILE"`) {
		t.Fatal("launcher does not quote COLLIE_JOIN_TOKEN_FILE")
	}
	if !strings.Contains(script, `< "$COLLIE_JOIN_TOKEN_FILE"`) {
		t.Fatal("launcher does not read token from the file on stdin")
	}
	if strings.Contains(script, `$(cat "$COLLIE_JOIN_TOKEN_FILE")`) {
		t.Fatal("launcher expands token into argv")
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
		`kill "$collie_pid"`,
		`wait "$collie_pid"`,
		`trap 'terminate 143' TERM`,
		`trap 'terminate 130' INT`,
		"trap cleanup EXIT",
		"status=$?",
		`exit "$status"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("launcher does not contain child cleanup contract %q", required)
		}
	}
	terminateStart := strings.Index(script, "terminate() {")
	terminateEnd := strings.Index(script, "trap 'terminate 143' TERM")
	if terminateStart < 0 || terminateEnd < 0 || terminateEnd <= terminateStart {
		t.Fatal("launcher does not define terminate handler before signal traps")
	}
	terminate := script[terminateStart:terminateEnd]
	if strings.Index(terminate, `kill "$collie_pid"`) < 0 || strings.Index(terminate, `wait "$collie_pid"`) < 0 {
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
