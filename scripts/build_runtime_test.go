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

func TestBuildRuntimeRejectsNixBinaryByDefault(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skipf("bun is unavailable: %v", err)
	}
	bun, err = filepath.EvalSymlinks(bun)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bun, "/nix/store/") {
		t.Skipf("bun is not Nix-installed: %s", bun)
	}
	output, err := runBuild(t, []string{"BUN_RUNTIME_BIN=" + bun, "HERDR_RUNTIME_BIN=" + bun})
	if err == nil {
		t.Fatal("build-runtime.sh accepted a Nix runtime without explicit relocation")
	}
	if !strings.Contains(output, "BUN_RUNTIME_BIN must not come from /nix/store") {
		t.Fatalf("output = %q, want default Nix rejection", output)
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

func TestBuildRuntimeRelocationIsExplicitAndCoversEveryNixRuntime(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{
		"ALLOW_NIX_RUNTIME_RELOCATION", "relocate-nix-runtime.sh",
		`TARGET_INSTALL_DIR="$3"`,
		`relocate_runtime "$bun_bin" "$RUNTIME_DIR/bin/bun" "$TARGET_INSTALL_DIR"`,
		`relocate_runtime "$herdr_bin" "$RUNTIME_DIR/bin/herdr" "$TARGET_INSTALL_DIR"`,
		`relocate_runtime "$collie_bin" "$RUNTIME_DIR/bin/collie" "$TARGET_INSTALL_DIR"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing relocation contract %q", required)
		}
	}
}

func TestBuildRuntimeRequiresAbsoluteNormalizedTargetInstallDir(t *testing.T) {
	for _, target := range []string{"", "relative", "/home/vcap/app/../bin", "/home/vcap/app/with space"} {
		output, err := runBuild(t, []string{"TARGET_INSTALL_DIR=" + target})
		if err == nil || !strings.Contains(output, "TARGET_INSTALL_DIR") {
			t.Fatalf("target %q: output=%q err=%v", target, output, err)
		}
	}
}

func TestBuildRuntimeValidatesAndForwardsTargetArchitecture(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{"Machine:", "Advanced Micro Devices X86-64", "AArch64", `TARGET_ARCH="$GOARCH"`} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing architecture contract %q", required)
		}
	}
}

func TestBuildRuntimeScansPackagedELFMetadataForNixPaths(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{"scan_elf_metadata", "--print-interpreter", "--print-rpath", "$RUNTIME_DIR"} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing packaged ELF scan %q", required)
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

func TestBuildRuntimePackagesAndChecksCollieCLISources(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{
		`./cmd/checkimports`,
		`-print0`,
		`readarray -d ''`,
		`"$COLLIE_DIR/$source" "$RUNTIME_DIR/collie/$source"`,
		`"$RUNTIME_DIR/collie" "$RUNTIME_DIR/collie/bridge/index.ts"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing Collie source closure contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`"$COLLIE_DIR/bridge" "$RUNTIME_DIR/collie/bridge"`,
		`"$COLLIE_DIR/cli" "$RUNTIME_DIR/collie/cli"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("build-runtime.sh copies whole source tree %q", forbidden)
		}
	}
}

func TestBuildRuntimeNeverDownloadsCollieDependencies(t *testing.T) {
	script := readBuildScript(t)
	if !strings.Contains(script, `install --frozen-lockfile --offline`) {
		t.Fatal("build-runtime.sh does not fail closed when Collie dependencies are absent")
	}
}

func runBuild(t *testing.T, env []string) (string, error) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	hasTarget := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "TARGET_INSTALL_DIR=") {
			hasTarget = true
		}
	}
	if !hasTarget {
		env = append(env, "TARGET_INSTALL_DIR=/home/vcap/app/.sandbox/bin")
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
