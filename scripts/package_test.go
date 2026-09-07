package scripts_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildRequiresPortableRuntimeBinaries(t *testing.T) {
	root := packageRoot(t)
	command := exec.Command("bash", filepath.Join(root, "scripts", "build.sh"))
	command.Dir = root
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("build.sh succeeded without portable runtime binaries")
	}
	if !strings.Contains(string(output), "BUN_RUNTIME_BIN is required") {
		t.Fatalf("output = %q, want BUN_RUNTIME_BIN error", output)
	}
}

func TestBuildUsesTransactionalStagingAndValidatesArtifactContract(t *testing.T) {
	script := readPackageFile(t, "scripts/build.sh")
	for _, required := range []string{
		"mktemp -d", "DIST_STAGING", "trap", "mv", "CGO_ENABLED=0", "GOOS=", "GOARCH=",
		"scripts/build-runtime.sh", "BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "RUNTIME_DIR=",
		"TARGET_INSTALL_DIR=", "MANAGER_RUNTIME_DIR=", "MANAGER_TARGET_INSTALL_DIR=",
		"web/dist", "sandbox/runtime", "manager-runtime", "collie/bridge", "collie/node_modules", "collie/package.json",
		"test -x", "test -f", "${name}.previous",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build.sh missing %q", required)
		}
	}
}

func TestBuildForwardsExplicitNixRelocationMode(t *testing.T) {
	script := readPackageFile(t, "scripts/build.sh")
	if !strings.Contains(script, `ALLOW_NIX_RUNTIME_RELOCATION="${ALLOW_NIX_RUNTIME_RELOCATION:-}"`) {
		t.Fatal("build.sh does not explicitly forward Nix relocation mode")
	}
}

func TestDevboxDeployDelegatesToLabDeployScript(t *testing.T) {
	var config struct {
		Packages []string `json:"packages"`
		Shell    struct {
			Scripts map[string]string `json:"scripts"`
		} `json:"shell"`
	}
	if err := json.Unmarshal([]byte(readPackageFile(t, "devbox.json")), &config); err != nil {
		t.Fatal(err)
	}
	deploy := config.Shell.Scripts["deploy"]
	if deploy != "bash scripts/lab-deploy.sh" {
		t.Fatalf("deploy script = %q, want external lab deploy delegation", deploy)
	}
	labDeploy := readPackageFile(t, "scripts/lab-deploy.sh")
	build := strings.Index(labDeploy, "scripts/build.sh")
	push := strings.Index(labDeploy, `"$CF_BIN" push`)
	if build < 0 || push < 0 || build > push {
		t.Fatalf("lab deploy must build before push")
	}
	for _, required := range []string{"CF_BIN", "BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "ALLOW_NIX_RUNTIME_RELOCATION=1", "DEPLOY_TMPDIR", "build distribution", "push manager", "configure routes", "start manager"} {
		if !strings.Contains(labDeploy, required) {
			t.Errorf("lab deploy script missing %q", required)
		}
	}
	for _, required := range []string{"patchelf", "binutils", "gcc"} {
		if !contains(config.Packages, required) {
			t.Errorf("devbox packages missing %q", required)
		}
	}
}

func TestBuildAssemblesExpectedLayoutWithFixtureTools(t *testing.T) {
	dist, output, err := runFixtureBuild(t, false)
	if err != nil {
		t.Fatalf("build.sh failed: %v\n%s", err, output)
	}
	for _, executable := range []string{"manager", "manager-runtime/bin/bun", "manager-runtime/bin/collie", "sandbox/runtime/bin/bun", "sandbox/runtime/bin/herdr", "sandbox/runtime/bin/collie", "sandbox/runtime/bin/sandbox-bootstrap", "sandbox/runtime/start.sh"} {
		info, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(executable)))
		if statErr != nil || info.Mode()&0o111 == 0 {
			t.Errorf("executable %s: info=%v err=%v", executable, info, statErr)
		}
	}
	for _, file := range []string{"web/index.html", "sandbox/runtime/collie/bridge/index.ts", "sandbox/runtime/collie/package.json", "sandbox/runtime/collie/node_modules/fixture/package.json", "sandbox/runtime/collie/web/dist/index.html"} {
		if _, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(file))); statErr != nil {
			t.Errorf("artifact %s: %v", file, statErr)
		}
	}
}

func TestManifestUsesManagerSpecificExecutablesAndSharedCollieAssets(t *testing.T) {
	manifest := readPackageFile(t, "manifest.yml")
	for _, required := range []string{
		"MANAGER_COLLIE_DIR: ./sandbox/runtime/collie",
		"MANAGER_RUNTIME_DIR: ./manager-runtime",
		"MANAGER_BUN_EXECUTABLE: ./manager-runtime/bin/bun",
		"MANAGER_COLLIE_EXECUTABLE: ./manager-runtime/bin/collie",
	} {
		if !strings.Contains(manifest, required) {
			t.Errorf("manifest.yml missing %q", required)
		}
	}
}

func TestBuildBindsSandboxAndManagerExecutablesToDifferentCFLayouts(t *testing.T) {
	script := readPackageFile(t, "scripts/build.sh")
	for _, required := range []string{
		"TARGET_INSTALL_DIR=/home/vcap/app/.sandbox/bin",
		"MANAGER_TARGET_INSTALL_DIR=/home/vcap/app/manager-runtime/bin",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build.sh missing %q", required)
		}
	}
}

func TestFailedBuildPreservesPreviousDist(t *testing.T) {
	dist, output, err := runFixtureBuild(t, true)
	if err == nil {
		t.Fatalf("build.sh succeeded with failing runtime builder: %s", output)
	}
	contents, readErr := os.ReadFile(filepath.Join(dist, "previous"))
	if readErr != nil || string(contents) != "keep" {
		t.Fatalf("previous dist was not preserved: contents=%q err=%v", contents, readErr)
	}
}

func TestFailedDistributionSwapRestoresPreviousDist(t *testing.T) {
	dist, output, err := runFixtureBuild(t, false, true)
	if err == nil {
		t.Fatalf("build.sh succeeded when staging rename failed: %s", output)
	}
	contents, readErr := os.ReadFile(filepath.Join(dist, "previous"))
	if readErr != nil || string(contents) != "keep" {
		t.Fatalf("previous dist was not restored: contents=%q err=%v", contents, readErr)
	}
}

func TestDeploymentMapsOnlyManagerPublicAndIdentityRoutes(t *testing.T) {
	manifest := readPackageFile(t, "manifest.yml")
	for _, required := range []string{"binary_buildpack", "no-route: true", "./manager", "/manager/healthz"} {
		if !strings.Contains(manifest, required) {
			t.Errorf("manifest.yml missing %q", required)
		}
	}
	deploy := readPackageFile(t, "scripts/deploy.sh")
	for _, required := range []string{"cf push", "--no-route", "--no-start", "cf set-env", "cf start", "cf create-route", "cf map-route", "PUBLIC_DOMAIN", "CF_IDENTITY_DOMAIN", "MANAGER_PUBLIC_HOST", "MANAGER_PACK_HOST", "SANDBOX_BUILDPACKS", "MANAGER_API_TOKEN", "MANAGER_APP_GUID"} {
		if !strings.Contains(deploy, required) {
			t.Errorf("deploy.sh missing %q", required)
		}
	}
	if strings.Contains(deploy, "sandbox") {
		t.Fatal("deploy.sh must not create or map sandbox routes")
	}
	for _, required := range []string{`MANAGER_ROUTE_HOST="${MANAGER_PACK_HOST%.$CF_IDENTITY_DOMAIN}"`, `"$CF_IDENTITY_DOMAIN" --hostname "$MANAGER_ROUTE_HOST"`} {
		if !strings.Contains(deploy, required) {
			t.Errorf("deploy.sh does not derive identity route label: missing %q", required)
		}
	}
}

func TestDeployUsesExactCFCLISequence(t *testing.T) {
	temp := t.TempDir()
	logPath := filepath.Join(temp, "cf.log")
	writeExecutable(t, filepath.Join(temp, "cf"), `#!/bin/sh
printf '%s\t' "$@" >> "$CF_LOG"
printf '\n' >> "$CF_LOG"
if [ "$1" = app ] && [ "$3" = --guid ]; then printf 'manager-guid\n'; fi
`)
	command := exec.Command("bash", filepath.Join(packageRoot(t), "scripts", "deploy.sh"))
	command.Dir = packageRoot(t)
	command.Env = []string{
		"PATH=" + temp + ":" + os.Getenv("PATH"), "CF_LOG=" + logPath,
		"MANAGER_APP_NAME=manager", "PUBLIC_DOMAIN=apps.example", "MANAGER_PUBLIC_HOST=manager",
		"CF_IDENTITY_DOMAIN=apps.identity", "MANAGER_PACK_HOST=manager-pack.apps.identity",
		"SANDBOX_BUILDPACKS=ruby_buildpack", "MANAGER_API_TOKEN=secret",
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("deploy.sh: %v: %s", err, output)
	}
	want := [][]string{
		{"push", "manager", "-f", "manifest.yml", "--no-route", "--no-start"},
		{"app", "manager", "--guid"},
		{"set-env", "manager", "CF_IDENTITY_DOMAIN", "apps.identity"},
		{"set-env", "manager", "SANDBOX_BUILDPACKS", "ruby_buildpack"},
		{"set-env", "manager", "MANAGER_APP_NAME", "manager"},
		{"set-env", "manager", "MANAGER_APP_GUID", "manager-guid"},
		{"set-env", "manager", "MANAGER_PACK_HOST", "manager-pack.apps.identity"},
		{"set-env", "manager", "MANAGER_API_TOKEN", "secret"},
		{"create-route", "apps.example", "--hostname", "manager"},
		{"map-route", "manager", "apps.example", "--hostname", "manager"},
		{"create-route", "apps.identity", "--hostname", "manager-pack"},
		{"map-route", "manager", "apps.identity", "--hostname", "manager-pack"},
		{"start", "manager"},
	}
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var got [][]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		got = append(got, strings.Fields(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("CF calls = %#v, want %#v", got, want)
	}
	for i := range want {
		if strings.Join(got[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Errorf("call %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestDeferredSpikeDocumentsRecordCommandsWithoutObservations(t *testing.T) {
	readme := readPackageFile(t, "README.md")
	for _, name := range []string{"cf-buildpack-runtime.md", "cf-pack-identity.md"} {
		if !strings.Contains(readme, "docs/spikes/"+name) {
			t.Errorf("README does not link %s", name)
		}
		doc := readPackageFile(t, "docs/spikes/"+name)
		for _, required := range []string{"Status: Deferred", "2026-09-04", "Commands", "Assertions", "Capture"} {
			if !strings.Contains(doc, required) {
				t.Errorf("%s missing %q", name, required)
			}
		}
		if strings.Contains(doc, "Status: Complete") || strings.Contains(doc, "Observed:") {
			t.Errorf("%s fabricates completed observations", name)
		}
	}
}

func TestRuntimeDocsDescribeNeededClosureAndResidualDlopenRisk(t *testing.T) {
	for _, name := range []string{"README.md", "docs/spikes/cf-buildpack-runtime.md"} {
		doc := readPackageFile(t, name)
		for _, required := range []string{"DT_NEEDED", "dlopen", "live", "GLIBC_PRIVATE", "/home/vcap/app/.sandbox/bin", "/home/vcap/app/manager-runtime/bin"} {
			if !strings.Contains(doc, required) {
				t.Errorf("%s missing relocation limitation %q", name, required)
			}
		}
	}
}

func TestRuntimeSpikeExercisesPackagedSandboxAtItsTargetAppLayout(t *testing.T) {
	doc := readPackageFile(t, "docs/spikes/cf-buildpack-runtime.md")
	for _, required := range []string{
		"cp -R dist/sandbox/runtime dist/runtime-spike/.sandbox",
		"-p dist/runtime-spike",
		"-c './.sandbox/start.sh'",
		"/home/vcap/app/.sandbox/bin/.bun-libs/ld-linux-x86-64.so.2",
	} {
		if !strings.Contains(doc, required) {
			t.Errorf("runtime spike missing sandbox layout contract %q", required)
		}
	}
	if strings.Contains(doc, "lab-relocated executables use cflinuxfs interpreters") {
		t.Fatal("runtime spike incorrectly asserts the cflinuxfs interpreter for Nix relocation")
	}
}

func TestDeploymentDocsUseExplicitCFCLIPath(t *testing.T) {
	readme := readPackageFile(t, "README.md")
	start := strings.Index(readme, "## Deploy")
	end := strings.Index(readme, "## Verification")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("README deployment and cleanup sections not found")
	}
	commands := readme[start:end]
	if !strings.Contains(commands, "CF_BIN=/path/to/cf") {
		t.Fatal("README deployment commands must define an explicit CF_BIN")
	}
	for _, line := range strings.Split(commands, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "cf ") || strings.Contains(trimmed, "$(cf ") {
			t.Errorf("README deployment command uses unresolved CF CLI: %q", trimmed)
		}
	}
}

func TestProcfileStartsPackagedManager(t *testing.T) {
	if got := strings.TrimSpace(readPackageFile(t, "Procfile")); got != "web: ./manager" {
		t.Fatalf("Procfile = %q", got)
	}
}

func packageRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Dir(filepath.Dir(filename))
}

func readPackageFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(packageRoot(t), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func runFixtureBuild(t *testing.T, failRuntime bool, failSwap ...bool) (string, string, error) {
	t.Helper()
	root := packageRoot(t)
	temp := t.TempDir()
	dist := filepath.Join(temp, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "previous"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(temp, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out=$2; shift 2; else shift; fi
done
mkdir -p "$(dirname "$out")"
printf '#!/bin/sh\n' > "$out"
chmod +x "$out"
`)
	writeExecutable(t, filepath.Join(bin, "bun"), `#!/bin/sh
mkdir -p "$FIXTURE_WEB_DIST"
printf '<html>fixture</html>\n' > "$FIXTURE_WEB_DIST/index.html"
`)
	if len(failSwap) > 0 && failSwap[0] {
		writeExecutable(t, filepath.Join(bin, "mv"), `#!/bin/sh
case "$1:$2" in
  *.staging.*:*/dist) exit 29 ;;
esac
exec /bin/mv "$@"
`)
	}
	runtimeScript := filepath.Join(temp, "build-runtime.sh")
	runtimeBody := `#!/bin/sh
set -eu
if [ "${FAIL_RUNTIME:-}" = 1 ]; then exit 23; fi
mkdir -p "$RUNTIME_DIR/bin" "$RUNTIME_DIR/collie/bridge" "$RUNTIME_DIR/collie/node_modules/fixture" "$RUNTIME_DIR/collie/web/dist"
for name in bun herdr collie sandbox-bootstrap; do printf '#!/bin/sh\n' > "$RUNTIME_DIR/bin/$name"; chmod +x "$RUNTIME_DIR/bin/$name"; done
printf '#!/bin/sh\n' > "$RUNTIME_DIR/start.sh"; chmod +x "$RUNTIME_DIR/start.sh"
printf fixture > "$RUNTIME_DIR/collie/bridge/index.ts"
printf '{}' > "$RUNTIME_DIR/collie/package.json"
printf '{}' > "$RUNTIME_DIR/collie/node_modules/fixture/package.json"
printf '<html>collie</html>' > "$RUNTIME_DIR/collie/web/dist/index.html"
mkdir -p "$MANAGER_RUNTIME_DIR/bin"
for name in bun collie; do printf '#!/bin/sh\n' > "$MANAGER_RUNTIME_DIR/bin/$name"; chmod +x "$MANAGER_RUNTIME_DIR/bin/$name"; done
`
	writeExecutable(t, runtimeScript, runtimeBody)
	fakeRuntime := filepath.Join(temp, "portable")
	writeExecutable(t, fakeRuntime, "#!/bin/sh\n")
	command := exec.Command("bash", filepath.Join(root, "scripts", "build.sh"))
	command.Dir = root
	command.Env = []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"), "DIST_DIR=" + dist,
		"BUILD_RUNTIME_SCRIPT=" + runtimeScript, "BUN_RUNTIME_BIN=" + fakeRuntime,
		"HERDR_RUNTIME_BIN=" + fakeRuntime, "FIXTURE_WEB_DIST=" + filepath.Join(root, "web", "dist"),
	}
	if failRuntime {
		command.Env = append(command.Env, "FAIL_RUNTIME=1")
	}
	output, err := command.CombinedOutput()
	return dist, string(output), err
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
