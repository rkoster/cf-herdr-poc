package scripts_test

import (
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

func TestBuildUsesAtomicStagingAndValidatesArtifactContract(t *testing.T) {
	script := readPackageFile(t, "scripts/build.sh")
	for _, required := range []string{
		"mktemp -d", "DIST_STAGING", "trap", "mv", "CGO_ENABLED=0", "GOOS=", "GOARCH=",
		"scripts/build-runtime.sh", "BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "RUNTIME_DIR=",
		"web/dist", "sandbox/runtime", "collie/bridge", "collie/node_modules", "collie/package.json",
		"test -x", "test -f", "${name}.previous",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build.sh missing %q", required)
		}
	}
}

func TestBuildAssemblesExpectedLayoutWithFixtureTools(t *testing.T) {
	dist, output, err := runFixtureBuild(t, false)
	if err != nil {
		t.Fatalf("build.sh failed: %v\n%s", err, output)
	}
	for _, executable := range []string{"manager", "sandbox/runtime/bin/bun", "sandbox/runtime/bin/herdr", "sandbox/runtime/bin/collie", "sandbox/runtime/bin/sandbox-bootstrap", "sandbox/runtime/start.sh"} {
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

func runFixtureBuild(t *testing.T, failRuntime bool) (string, string, error) {
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
