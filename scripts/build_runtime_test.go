package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildRuntimeRequiresPortableBinaryPaths(t *testing.T) {
	output, err := runBuild(t, nil)
	if err == nil {
		t.Fatal("build-runtime.sh succeeded without runtime binary paths")
	}
	if !strings.Contains(output, "BUN_RUNTIME_BIN is required") {
		t.Fatalf("output = %q, want missing BUN_RUNTIME_BIN error", output)
	}
}

func TestBuildRuntimeRejectsSymlinkBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "bun")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "bun-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	output, err := runBuild(t, []string{"BUN_RUNTIME_BIN=" + link, "HERDR_RUNTIME_BIN=" + target})
	if err == nil {
		t.Fatal("build-runtime.sh accepted symlink runtime binary")
	}
	if !strings.Contains(output, "BUN_RUNTIME_BIN must not be a symlink") {
		t.Fatalf("output = %q, want symlink rejection", output)
	}
}

func TestBuildRuntimeScriptChecksLinuxDependencies(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{"BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "COLLIE_RUNTIME_BIN", "readelf", "ldd", "/nix/store"} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh does not contain %q", required)
		}
	}
}

func TestBuildRuntimeBuildsStaticSandboxBootstrap(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{"CGO_ENABLED=0", "go build", "./cmd/sandbox-bootstrap", "$RUNTIME_DIR/bin/sandbox-bootstrap", "test -x"} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing %q", required)
		}
	}
}

func TestBuildRuntimeBuildsNativeConstrainedCopier(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{"./cmd/copytree", "GOHOSTOS", "GOHOSTARCH", "mktemp -d", "trap", "COLLIE_DIR"} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing native copier contract %q", required)
		}
	}
	if strings.Contains(script, "cp -RL") || strings.Contains(script, "go run ./cmd/copytree") {
		t.Fatal("build-runtime.sh uses unsafe or target-architecture tree copying")
	}
}

func runBuild(t *testing.T, env []string) (string, error) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	command := exec.Command("bash", filepath.Join(filepath.Dir(filename), "build-runtime.sh"))
	command.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func readBuildScript(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "build-runtime.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
