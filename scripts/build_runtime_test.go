package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCFLinuxFS5BuilderContract(t *testing.T) {
	root := packageRoot(t)
	dockerfile := readFile(t, filepath.Join(root, "docker", "cflinuxfs5-builder", "Dockerfile"))
	extractor := readFile(t, filepath.Join(root, "docker", "cflinuxfs5-builder", "extract-cf-cli.sh"))
	for _, required := range []string{
		"BASE_IMAGE=ghcr.io/cloudfoundry/k8s/cflinuxfs5:0.53.0",
		"FROM cflinuxfs5-tools AS build",
		"FROM scratch AS output",
		"COPY --from=build /work/dist/ /",
		"ARG TARGETARCH",
		"sha256sum -c",
		"COPY docker/cflinuxfs5-builder/extract-cf-cli.sh /usr/local/bin/extract-cf-cli.sh",
		"HERDR_URL is required",
		"CF_URL is required",
		"/work/dist/manager",
		"sandbox/runtime/bin/herdr",
		"sandbox/runtime/start.sh",
		"manager-runtime/bin/cf",
		"extract-cf-cli.sh",
		"cf_format=tgz",
		"cf_format=zip",
		"/usr/local/bin/extract-cf-cli.sh /tools/downloads/cf /tools/bin \"$cf_format\"",
		"ln -s /tools/bin/bun /usr/local/bin/bunx",
		"ENV PATH=/usr/local/go/bin:/tools/bin:/usr/local/bin:$PATH",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing %q", required)
		}
	}
	for _, forbidden := range []string{"COPY .git", "COPY tests", "apt-get install", "latest"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Dockerfile contains forbidden %q", forbidden)
		}
	}
	for _, required := range []string{
		`case "$format" in`,
		"tgz)",
		"zip)",
		`usage: extract-cf-cli.sh ARCHIVE DESTINATION FORMAT`,
		"$extract_dir/cf",
		"$extract_dir/cf8",
		"find \"$extract_dir\" -type f",
		"CF CLI archive contains no regular executable named cf or cf8",
		"install -m 0755 \"$candidate\" \"$destination/cf\"",
	} {
		if !strings.Contains(extractor, required) {
			t.Errorf("CF CLI extractor missing %q", required)
		}
	}
}

func TestCFLinuxFS5BuilderCleanupIsIdempotent(t *testing.T) {
	root := packageRoot(t)
	dockerfile := readFile(t, filepath.Join(root, "docker", "cflinuxfs5-builder", "Dockerfile"))
	for _, required := range []string{
		"rm -rf /work/dist/sandbox/runtime/collie/.git",
		"rm -rf /work/dist/sandbox/runtime/collie/web/node_modules",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile cleanup missing idempotent removal %q", required)
		}
	}
	for _, forbidden := range []string{
		"test -e /work/dist/sandbox/runtime/collie/.git",
		"test -e /work/dist/sandbox/runtime/collie/web/node_modules",
	} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Dockerfile cleanup probes missing paths with %q", forbidden)
		}
	}
}

func TestCFCLIExtractionAcceptsArchiveRootBinariesWithExplicitFormat(t *testing.T) {
	root := packageRoot(t)
	for _, name := range []string{"cf", "cf8"} {
		t.Run(name, func(t *testing.T) {
			archiveDir := t.TempDir()
			archiveRoot := filepath.Join(archiveDir, "archive")
			if err := os.Mkdir(archiveRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(archiveRoot, name)
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(archiveDir, "cf-archive")
			command := exec.Command("tar", "-czf", archive, "-C", archiveRoot, name)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("create archive: %v\n%s", err, output)
			}

			outputDir := filepath.Join(t.TempDir(), "bin")
			command = exec.Command("bash", filepath.Join(root, "docker", "cflinuxfs5-builder", "extract-cf-cli.sh"), archive, outputDir, "tgz")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("extract root %s binary: %v\n%s", name, err, output)
			}
			installed := filepath.Join(outputDir, "cf")
			if info, err := os.Stat(installed); err != nil || info.Mode()&0o111 == 0 {
				t.Fatalf("installed CF CLI is not executable: info=%v err=%v", info, err)
			}
		})
	}
}

func TestBuildDefaultsToDockerAndLegacyRequiresExplicitMode(t *testing.T) {
	script := readFile(t, filepath.Join(packageRoot(t), "scripts", "build.sh"))
	if !strings.Contains(script, `BUILD_MODE="${BUILD_MODE:-cflinuxfs5}"`) {
		t.Fatal("build.sh does not default BUILD_MODE to cflinuxfs5")
	}
	if strings.Contains(script, "BUILD_RUNTIME_SCRIPT") && strings.Contains(script, "BUILD_MODE=nix-relocation") {
		t.Fatal("build.sh selects Nix relocation from BUILD_RUNTIME_SCRIPT")
	}
}

func TestCFLinuxFS5BuildPropagatesTargetArchitecture(t *testing.T) {
	script := readFile(t, filepath.Join(packageRoot(t), "scripts", "build-cflinuxfs5.sh"))
	for _, required := range []string{"TARGETARCH", "amd64", "arm64", "--build-arg", "GOARCH"} {
		if !strings.Contains(script, required) {
			t.Errorf("builder script missing architecture contract %q", required)
		}
	}
}

func TestCFLinuxFS5ArtifactManifestHasPerArchitectureBunInputs(t *testing.T) {
	manifest := readFile(t, filepath.Join(packageRoot(t), "docker", "cflinuxfs5-builder", "artifacts.env"))
	for _, required := range []string{"BUN_URL_AMD64", "BUN_SHA256_AMD64", "BUN_URL_ARM64", "BUN_SHA256_ARM64", "HERDR_URL_AMD64", "CF_URL_AMD64"} {
		if !strings.Contains(manifest, required) {
			t.Errorf("artifact manifest missing %q", required)
		}
	}
}

func TestCFLinuxFS5ArtifactSelectorUsesManifestKeysForBothArchitectures(t *testing.T) {
	root := packageRoot(t)
	for _, arch := range []string{"amd64", "arm64"} {
		command := exec.Command("bash", filepath.Join(root, "scripts", "select-cflinuxfs5-artifacts.sh"), arch)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("architecture %s selector failed: %v\n%s", arch, err, output)
		}
		if !strings.Contains(string(output), "HERDR_URL") || !strings.Contains(string(output), "CF_URL") {
			t.Fatalf("architecture %s output = %q, want Herdr/CF inputs", arch, output)
		}
	}
}

func TestCFLinuxFS5BuildScriptUsesDockerAndDoesNotPassSecrets(t *testing.T) {
	script := readFile(t, filepath.Join(packageRoot(t), "scripts", "build-cflinuxfs5.sh"))
	for _, required := range []string{"docker", "--output", "dist", "BUILD_MODE", "Docker is required"} {
		if !strings.Contains(script, required) {
			t.Errorf("builder script missing %q", required)
		}
	}
	if strings.Contains(script, "MANAGER_API_TOKEN") || strings.Contains(script, "--env-file") {
		t.Fatal("builder script passes deployment secrets to Docker")
	}
}

func TestCFLinuxFS5DockerignoreExcludesGeneratedAndSecrets(t *testing.T) {
	ignore := readFile(t, filepath.Join(packageRoot(t), ".dockerignore"))
	for _, required := range []string{".git", ".env", "dist", "*.previous", "*.staging.*", "bosh/", "cf.yml"} {
		if !strings.Contains(ignore, required) {
			t.Errorf(".dockerignore missing %q", required)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

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
	for _, required := range []string{"BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "COLLIE_RUNTIME_BIN", "CF_BIN", "readelf", "ldd", "/nix/store"} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh does not contain %q", required)
		}
	}
}

func TestBuildRuntimeRelocationIsExplicitAndCoversEveryNixRuntime(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{
		"ALLOW_NIX_RUNTIME_RELOCATION", "relocate-nix-runtime.sh",
		"SANDBOX_TARGET_INSTALL_DIR",
		`TARGET_INSTALL_DIR="$3"`,
		`relocate_runtime "$bun_bin" "$RUNTIME_DIR/bin/bun" "$TARGET_INSTALL_DIR"`,
		`relocate_runtime "$herdr_bin" "$RUNTIME_DIR/bin/herdr" "$TARGET_INSTALL_DIR"`,
		`relocate_runtime "$collie_bin" "$RUNTIME_DIR/bin/collie" "$TARGET_INSTALL_DIR"`,
		`relocate_cf_wrapper "$cf_bin" "$MANAGER_RUNTIME_DIR/bin/cf" "$MANAGER_TARGET_INSTALL_DIR"`,
		`"$MANAGER_RUNTIME_DIR/bin/cf" version >/dev/null 2>&1`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing relocation contract %q", required)
		}
	}
}

func TestBuildTargetsUseVisibleDirectRuntimeAndSeparateManagerRuntime(t *testing.T) {
	build := readFile(t, filepath.Join(packageRoot(t), "scripts", "build.sh"))
	if !strings.Contains(build, `SANDBOX_TARGET_INSTALL_DIR="${SANDBOX_TARGET_INSTALL_DIR:-/home/vcap/app/.sandbox/bin}"`) {
		t.Fatal("build.sh does not default to the manager sandbox target")
	}
	if !strings.Contains(build, "MANAGER_TARGET_INSTALL_DIR=/home/vcap/app/manager-runtime/bin") {
		t.Fatal("build.sh changed the manager runtime target")
	}
	if !strings.Contains(build, "DIRECT_SANDBOX") || !strings.Contains(build, "/home/vcap/app/sandbox-runtime/bin") {
		t.Fatal("build.sh does not support an explicit direct sandbox target")
	}
	runtime := readBuildScript(t)
	if !strings.Contains(runtime, `SANDBOX_TARGET_INSTALL_DIR="${SANDBOX_TARGET_INSTALL_DIR-/home/vcap/app/.sandbox/bin}"`) || !strings.Contains(runtime, "/home/vcap/app/sandbox-runtime/bin") {
		t.Fatal("build-runtime.sh does not expose the sandbox target override")
	}
}

func TestCFLinuxFS5BuilderDefaultsToManagerSandboxInterpreter(t *testing.T) {
	root := packageRoot(t)
	dockerfile := readFile(t, filepath.Join(root, "docker", "cflinuxfs5-builder", "Dockerfile"))
	build := readFile(t, filepath.Join(root, "scripts", "build-cflinuxfs5.sh"))
	for _, required := range []string{
		"ARG DIRECT_SANDBOX=",
		"ARG SANDBOX_TARGET_INSTALL_DIR=",
		"sandbox_target=/home/vcap/app/.sandbox/bin",
		"DIRECT_SANDBOX=${DIRECT_SANDBOX:-}",
		"SANDBOX_TARGET_INSTALL_DIR=${SANDBOX_TARGET_INSTALL_DIR:-}",
	} {
		if !strings.Contains(dockerfile+build, required) {
			t.Errorf("cflinuxfs5 builder missing target contract %q", required)
		}
	}
}

func TestRuntimeLaunchersUseManagerAndDirectInterpreters(t *testing.T) {
	root := packageRoot(t)
	spike := readFile(t, filepath.Join(root, "docs", "spikes", "cf-buildpack-runtime.md"))
	for _, required := range []string{
		"/home/vcap/app/.sandbox/bin",
		"/home/vcap/app/sandbox-runtime/bin",
		"./.sandbox/start.sh",
		"./sandbox-runtime/start.sh",
	} {
		if !strings.Contains(spike, required) {
			t.Errorf("runtime path documentation missing %q", required)
		}
	}
}

func TestBuildRuntimeDocumentsCFCLIWrapperException(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{"relocate_cf_wrapper", "--wrapper"} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing CF wrapper contract %q", required)
		}
	}
}

func TestBuildRuntimeSmokeFailureIsActionableAndDoesNotPrintOutput(t *testing.T) {
	script := readBuildScript(t)
	for _, required := range []string{
		"manager CF CLI smoke test failed",
		"CF_BIN must execute cf version successfully",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build-runtime.sh missing actionable CF smoke contract %q", required)
		}
	}
}

func TestBuildRuntimeRequiresAbsoluteNormalizedTargetInstallDir(t *testing.T) {
	for _, target := range []string{"", "relative", "/home/vcap/app/../bin", "/home/vcap/app/with space"} {
		output, err := runBuild(t, []string{"SANDBOX_TARGET_INSTALL_DIR=" + target})
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
		if strings.HasPrefix(entry, "TARGET_INSTALL_DIR=") || strings.HasPrefix(entry, "SANDBOX_TARGET_INSTALL_DIR=") {
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
