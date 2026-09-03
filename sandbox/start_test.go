package sandbox_test

import (
	"os"
	"path/filepath"
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
	script := string(contents)

	ordered := []string{"herdr server", "collie pack join", "bun run bridge/index.ts"}
	position := -1
	for _, operation := range ordered {
		next := strings.Index(script, operation)
		if next < 0 {
			t.Fatalf("launcher does not contain %q", operation)
		}
		if next <= position {
			t.Fatalf("%q is not after the previous operation", operation)
		}
		position = next
	}

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
	if trapDisabled := strings.Index(script, "trap - EXIT"); trapDisabled >= 0 && strings.Index(script, `wait "$collie_pid"`) > trapDisabled {
		t.Fatal("launcher disables cleanup trap before Collie exits")
	}
}
