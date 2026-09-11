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
	command.Env = append(command.Env, "BUILD_MODE=nix-relocation")
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
		"scripts/build-runtime.sh", "BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "OPENCODE_RUNTIME_BIN", "RUNTIME_DIR=",
		"TARGET_INSTALL_DIR=", "MANAGER_RUNTIME_DIR=", "MANAGER_TARGET_INSTALL_DIR=",
		"web/dist", "sandbox/runtime", "manager-runtime", "collie/bridge", "collie/cli", "collie/node_modules", "collie/package.json",
		"test -x", "test -f", "${name}.previous",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("build.sh missing %q", required)
		}
	}
}

func TestCFLinuxFS5DockerignoreExcludesGeneratedCollieFiles(t *testing.T) {
	ignore := readPackageFile(t, ".dockerignore")
	for _, required := range []string{
		".git",
		"collie/node_modules/",
		"collie/web/node_modules/",
		"collie/bin/",
		"collie/web/dist/",
		"collie/dist-staging/",
		"collie/.devbox/",
		"dist/",
		"sandbox/runtime/",
		"manager-runtime/",
		"tmp.*",
		"nix-*/",
		"collie-activity-*/",
	} {
		if !strings.Contains(ignore, required) {
			t.Errorf(".dockerignore missing %q", required)
		}
	}
	for _, required := range []string{
		"collie/bridge",
		"collie/cli",
		"collie/web/src",
		"collie/package.json",
		"collie/bun.lock",
	} {
		if !strings.Contains(ignore, required) {
			continue
		}
		t.Errorf(".dockerignore excludes required Collie source %q", required)
	}
}

func TestManifestPinsVerifiedHerdrArtifacts(t *testing.T) {
	manifest := readPackageFile(t, "docker/cflinuxfs5-builder/artifacts.env")
	for _, required := range []string{
		"GO_VERSION=1.27.1",
		"GO_URL_AMD64=https://go.dev/dl/go1.27.1.linux-amd64.tar.gz",
		"GO_SHA256_AMD64=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445",
		"GO_URL_ARM64=https://go.dev/dl/go1.27.1.linux-arm64.tar.gz",
		"GO_SHA256_ARM64=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec",
		"HERDR_VERSION=0.8.2",
		"HERDR_URL_AMD64=https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-x86_64",
		"HERDR_SHA256_AMD64=976150a14d490c94b243ea2e1a7eb2dfb67f12e36b182db90936f6728e6aecf4",
		"HERDR_URL_ARM64=https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-aarch64",
		"HERDR_SHA256_ARM64=f55610658e1c2e0d2aaef730b4b2ab885f7f8ba00285ab372bfb14f2e3d5b40d",
		"CF_VERSION=8.19.0",
		"CF_URL_AMD64=https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_x86-64.tgz",
		"CF_SHA256_AMD64=98268ab3134bb3a1c97ffce797b4e6d35590a82e006cd098ad7a29f0a5cae7d8",
		"CF_URL_ARM64=https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_arm64.tgz",
		"CF_SHA256_ARM64=454c29a44a51c8edc9696678403e2e40808357a397033af5a018e6ca8ee32117",
		"OPENCODE_VERSION=1.18.30",
		"OPENCODE_URL_AMD64=https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-x64.tar.gz",
		"OPENCODE_SHA256_AMD64=55007246858165496ff85ba1c2b648f7421e8e2013bf4189a680c9ff8e699d17",
		"OPENCODE_URL_ARM64=https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-arm64.tar.gz",
		"OPENCODE_SHA256_ARM64=4111a55c2a02c0fac314bd51e9a2330280e6d29d2b85b9554fff6d62612566ed",
	} {
		if !strings.Contains(manifest, required) {
			t.Errorf("artifacts.env missing %q", required)
		}
	}
}

func TestCFLinuxFS5SelectorUsesPinnedArtifactsForEachArchitecture(t *testing.T) {
	root := packageRoot(t)
	for _, test := range []struct {
		arch  string
		cfURL string
		cfSHA string
	}{
		{"amd64", "https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_x86-64.tgz", "98268ab3134bb3a1c97ffce797b4e6d35590a82e006cd098ad7a29f0a5cae7d8"},
		{"arm64", "https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_arm64.tgz", "454c29a44a51c8edc9696678403e2e40808357a397033af5a018e6ca8ee32117"},
	} {
		t.Run(test.arch, func(t *testing.T) {
			command := exec.Command("bash", filepath.Join(root, "scripts", "select-cflinuxfs5-artifacts.sh"), test.arch)
			command.Dir = root
			command.Env = []string{"PATH=" + os.Getenv("PATH")}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("selector failed: %v\n%s", err, output)
			}
			for _, required := range []string{"CF_URL=" + test.cfURL, "CF_SHA256=" + test.cfSHA} {
				if !strings.Contains(string(output), required) {
					t.Errorf("selector output missing %q: %s", required, output)
				}
			}
		})
	}
}

func TestCFLinuxFS5SelectorUsesPinnedOpenCodeForEachArchitecture(t *testing.T) {
	root := packageRoot(t)
	for _, test := range []struct {
		arch string
		url  string
		sha  string
	}{
		{"amd64", "https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-x64.tar.gz", "55007246858165496ff85ba1c2b648f7421e8e2013bf4189a680c9ff8e699d17"},
		{"arm64", "https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-arm64.tar.gz", "4111a55c2a02c0fac314bd51e9a2330280e6d29d2b85b9554fff6d62612566ed"},
	} {
		t.Run(test.arch, func(t *testing.T) {
			command := exec.Command("bash", filepath.Join(root, "scripts", "select-cflinuxfs5-artifacts.sh"), test.arch)
			command.Dir = root
			command.Env = []string{"PATH=" + os.Getenv("PATH")}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("selector failed: %v\n%s", err, output)
			}
			for _, required := range []string{"OPENCODE_URL=" + test.url, "OPENCODE_SHA256=" + test.sha} {
				if !strings.Contains(string(output), required) {
					t.Errorf("selector output missing %q: %s", required, output)
				}
			}
		})
	}
}

func TestCFLinuxFS5SelectorUsesPinnedGoForEachArchitecture(t *testing.T) {
	root := packageRoot(t)
	for _, test := range []struct {
		arch string
		url  string
		sha  string
	}{
		{"amd64", "https://go.dev/dl/go1.27.1.linux-amd64.tar.gz", "63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445"},
		{"arm64", "https://go.dev/dl/go1.27.1.linux-arm64.tar.gz", "3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec"},
	} {
		t.Run(test.arch, func(t *testing.T) {
			command := exec.Command("bash", filepath.Join(root, "scripts", "select-cflinuxfs5-artifacts.sh"), test.arch)
			command.Dir = root
			command.Env = []string{"PATH=" + os.Getenv("PATH")}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("selector failed: %v\n%s", err, output)
			}
			for _, required := range []string{"GO_URL=" + test.url, "GO_SHA256=" + test.sha} {
				if !strings.Contains(string(output), required) {
					t.Errorf("selector output missing %q: %s", required, output)
				}
			}
		})
	}
}

func TestCFLinuxFS5DockerfileBootstrapsGoWithoutNode(t *testing.T) {
	dockerfile := readPackageFile(t, "docker/cflinuxfs5-builder/Dockerfile")
	for _, required := range []string{
		"ARG GO_URL",
		"ARG GO_SHA256",
		"/usr/local/go",
		"sha256sum -c",
		"PATH=/usr/local/go/bin:/tools/bin:/usr/local/bin:$PATH",
		"go build",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing %q", required)
		}
	}
	if strings.Contains(dockerfile, "command -v node") {
		t.Fatal("Dockerfile still requires Node.js")
	}
	if strings.Contains(dockerfile, "COPY --from=build /usr/local/go") || strings.Contains(dockerfile, "COPY --from=cflinuxfs5-tools /usr/local/go") {
		t.Fatal("Dockerfile packages Go in the final output")
	}
}

func TestCFLinuxFS5DockerfileCarriesBunxIntoBuildStage(t *testing.T) {
	dockerfile := readPackageFile(t, "docker/cflinuxfs5-builder/Dockerfile")
	for _, required := range []string{
		"ln -s /tools/bin/bun /tools/bin/bunx",
		"test -x /tools/bin/bunx",
		"COPY --from=cflinuxfs5-tools /tools /tools",
		"ln -sf /tools/bin/bun /usr/local/bin/bunx",
		"command -v bunx",
		"bunx --version",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing %q", required)
		}
	}
}

func TestCFLinuxFS5SelectorAllowsExplicitNonEmptyOverrides(t *testing.T) {
	root := packageRoot(t)
	command := exec.Command("bash", filepath.Join(root, "scripts", "select-cflinuxfs5-artifacts.sh"), "amd64")
	command.Dir = root
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"BUN_URL=https://example.test/bun.zip", "BUN_SHA256=bun-override",
		"HERDR_URL=https://example.test/herdr", "HERDR_SHA256=herdr-override",
		"CF_URL=https://example.test/cf.tgz", "CF_SHA256=cf-override",
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("selector failed with explicit overrides: %v\n%s", err, output)
	}
	for _, required := range []string{
		"BUN_URL=https://example.test/bun.zip", "BUN_SHA256=bun-override",
		"HERDR_URL=https://example.test/herdr", "HERDR_SHA256=herdr-override",
		"CF_URL=https://example.test/cf.tgz", "CF_SHA256=cf-override",
	} {
		if !strings.Contains(string(output), required) {
			t.Errorf("selector output missing override %q: %s", required, output)
		}
	}
}

func TestCFLinuxFS5BuildDoesNotPassEmptyArtifactOverrides(t *testing.T) {
	script := readPackageFile(t, "scripts/build-cflinuxfs5.sh")
	for _, forbidden := range []string{
		`BUN_URL="${BUN_URL:-}"`, `BUN_SHA256="${BUN_SHA256:-}"`,
		`HERDR_URL="${HERDR_URL:-}"`, `HERDR_SHA256="${HERDR_SHA256:-}"`,
		`CF_URL="${CF_URL:-}"`, `CF_SHA256="${CF_SHA256:-}"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("build script still manufactures empty artifact override %q", forbidden)
		}
	}
}

func TestReadmeDocumentsPinnedHerdrBuilderArtifacts(t *testing.T) {
	readme := readPackageFile(t, "README.md")
	for _, required := range []string{
		"docker/cflinuxfs5-builder/artifacts.env",
		"HERDR_URL_AMD64=https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-x86_64",
		"HERDR_SHA256_AMD64=976150a14d490c94b243ea2e1a7eb2dfb67f12e36b182db90936f6728e6aecf4",
		"HERDR_URL_ARM64=https://github.com/herdrdev/herdr/releases/download/v0.8.2/herdr-linux-aarch64",
		"HERDR_SHA256_ARM64=f55610658e1c2e0d2aaef730b4b2ab885f7f8ba00285ab372bfb14f2e3d5b40d",
		"CF CLI v8.19.0 artifacts are verified",
		"CF_URL_AMD64=https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_x86-64.tgz",
		"CF_SHA256_AMD64=98268ab3134bb3a1c97ffce797b4e6d35590a82e006cd098ad7a29f0a5cae7d8",
		"CF_URL_ARM64=https://github.com/cloudfoundry/cli/releases/download/v8.19.0/cf8-cli_8.19.0_linux_arm64.tgz",
		"CF_SHA256_ARM64=454c29a44a51c8edc9696678403e2e40808357a397033af5a018e6ca8ee32117",
		"fails closed",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README.md missing %q", required)
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
	if deploy != "bash -c 'if [[ -f .secrets ]]; then source .secrets; fi; exec bash scripts/lab-deploy.sh'" {
		t.Fatalf("deploy script = %q, want external lab deploy delegation", deploy)
	}
	labDeploy := readPackageFile(t, "scripts/lab-deploy.sh")
	build := strings.Index(labDeploy, "scripts/build.sh")
	push := strings.Index(labDeploy, `"$CF_BIN" push`)
	if build < 0 || push < 0 || build > push {
		t.Fatalf("lab deploy must build before push")
	}
	for _, required := range []string{"CF_BIN", "BUN_RUNTIME_BIN", "HERDR_RUNTIME_BIN", "MANAGER_CF_EXECUTABLE", "ALLOW_NIX_RUNTIME_RELOCATION=1", "DEPLOY_TMPDIR", "build distribution", "push manager", "configure routes", "start manager"} {
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
	for _, executable := range []string{"manager", "manager-runtime/bin/bun", "manager-runtime/bin/herdr", "manager-runtime/bin/cf", "manager-runtime/bin/collie", "sandbox/runtime/bin/bun", "sandbox/runtime/bin/herdr", "sandbox/runtime/bin/collie", "sandbox/runtime/bin/opencode", "sandbox/runtime/bin/cf", "sandbox/runtime/bin/sandbox-bootstrap", "sandbox/runtime/start.sh"} {
		info, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(executable)))
		if statErr != nil || info.Mode()&0o111 == 0 {
			t.Errorf("executable %s: info=%v err=%v", executable, info, statErr)
		}
	}
	for _, file := range []string{"web/index.html", "sandbox/runtime/collie/bridge/index.ts", "sandbox/runtime/collie/cli/install-kind.ts", "sandbox/runtime/collie/cli/link.ts", "sandbox/runtime/collie/cli/sys.ts", "sandbox/runtime/collie/package.json", "sandbox/runtime/collie/node_modules/fixture/package.json", "sandbox/runtime/collie/web/dist/index.html"} {
		if _, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(file))); statErr != nil {
			t.Errorf("artifact %s: %v", file, statErr)
		}
	}
	for _, excluded := range []string{"sandbox/runtime/collie/cli/install-kind.test.ts", "sandbox/runtime/collie/cli/testdata"} {
		if _, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(excluded))); !os.IsNotExist(statErr) {
			t.Errorf("excluded artifact %s exists or cannot be checked: %v", excluded, statErr)
		}
	}
}

func TestBuildPackagesManagerCFCLIAndRelocatedSmokeCheck(t *testing.T) {
	script := readPackageFile(t, "scripts/build-runtime.sh")
	for _, required := range []string{`relocate_cf_wrapper "$cf_bin" "$MANAGER_RUNTIME_DIR/bin/cf"`, `"$MANAGER_RUNTIME_DIR/bin/cf" version >/dev/null 2>&1`} {
		if !strings.Contains(script, required) {
			t.Fatalf("build-runtime.sh missing CF CLI wrapper contract %q", required)
		}
	}
	dist, output, err := runFixtureBuild(t, false)
	if err != nil {
		t.Fatalf("build.sh failed: %v\n%s", err, output)
	}
	if info, statErr := os.Stat(filepath.Join(dist, "manager-runtime/bin/cf")); statErr != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("manager CF CLI artifact is not executable: info=%v err=%v", info, statErr)
	}
	for _, artifact := range []string{"manager-runtime/bin/cf.real", "manager-runtime/bin/.cf-libs"} {
		if _, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(artifact))); statErr != nil {
			t.Fatalf("manager CF CLI wrapper artifact %s missing: %v", artifact, statErr)
		}
	}
}

func TestBuildKeepsNonCFManagerRuntimesAsDirectELFContract(t *testing.T) {
	script := readPackageFile(t, "scripts/build-runtime.sh")
	if !strings.Contains(script, `relocate_runtime "$bun_bin" "$MANAGER_RUNTIME_DIR/bin/bun"`) || !strings.Contains(script, `relocate_runtime "$collie_bin" "$MANAGER_RUNTIME_DIR/bin/collie"`) {
		t.Fatal("manager Bun and Collie no longer use direct ELF relocation")
	}
}

func TestManifestUsesManagerSpecificExecutablesAndSharedCollieAssets(t *testing.T) {
	manifest := readPackageFile(t, "manifest.yml")
	for _, required := range []string{
		"MANAGER_COLLIE_DIR: ./sandbox/runtime/collie",
		"MANAGER_RUNTIME_DIR: ./manager-runtime",
		"MANAGER_BUN_EXECUTABLE: ./manager-runtime/bin/bun",
		"MANAGER_HERDR_EXECUTABLE: ./manager-runtime/bin/herdr",
		"MANAGER_COLLIE_EXECUTABLE: ./manager-runtime/bin/collie",
		"MANAGER_CF_EXECUTABLE: ./manager-runtime/bin/cf",
	} {
		if !strings.Contains(manifest, required) {
			t.Errorf("manifest.yml missing %q", required)
		}
	}
}

func TestBuildBindsSandboxAndManagerExecutablesToDifferentCFLayouts(t *testing.T) {
	script := readPackageFile(t, "scripts/build.sh")
	for _, required := range []string{
		"SANDBOX_TARGET_INSTALL_DIR=\"${SANDBOX_TARGET_INSTALL_DIR:-/home/vcap/app/sandbox-runtime/bin}\"",
		"DIRECT_SANDBOX",
		"/home/vcap/app/sandbox-runtime/bin",
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
	for _, required := range []string{"cf push", "--no-route", "--no-start", "cf set-env", "MANAGER_CF_EXECUTABLE", "cf start", "cf create-route", "cf map-route", "PUBLIC_DOMAIN", "CF_IDENTITY_DOMAIN", "MANAGER_PUBLIC_HOST", "MANAGER_PACK_HOST", "SANDBOX_BUILDPACKS", "MANAGER_API_TOKEN", "MANAGER_APP_GUID"} {
		if !strings.Contains(deploy, required) {
			t.Errorf("deploy.sh missing %q", required)
		}
	}
	for _, line := range strings.Split(deploy, "\n") {
		if (strings.Contains(line, "create-route") || strings.Contains(line, "map-route")) && strings.Contains(line, "sandbox") {
			t.Fatalf("deploy.sh must not create or map sandbox routes: %q", line)
		}
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
if [ "$1" = push ]; then
  args=" $* "
  case "$args" in *" --no-manifest "*" --redact-env "*) ;; *) printf -- '- MANAGER_API_TOKEN: old-secret\n';; esac
fi
if [ "$1" = set-env ] && [ "$3" = MANAGER_API_TOKEN ]; then printf 'new token: %s\n' "$4"; fi
`)
	command := exec.Command("bash", filepath.Join(packageRoot(t), "scripts", "deploy.sh"))
	command.Dir = packageRoot(t)
	command.Env = []string{
		"PATH=" + temp + ":" + os.Getenv("PATH"), "CF_LOG=" + logPath,
		"MANAGER_APP_NAME=manager", "PUBLIC_DOMAIN=apps.example", "MANAGER_PUBLIC_HOST=manager",
		"CF_IDENTITY_DOMAIN=apps.identity", "MANAGER_PACK_HOST=manager-pack.apps.identity",
		"SANDBOX_BUILDPACKS=ruby_buildpack", "MANAGER_API_TOKEN=secret",
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("deploy.sh: %v: %s", err, output)
	}
	for _, secret := range []string{"old-secret", "secret"} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("deploy output exposed %q: %s", secret, output)
		}
	}
	want := [][]string{
		{"push", "manager", "--no-manifest", "-p", "dist", "-b", "binary_buildpack", "-c", "./manager", "--no-route", "--no-start", "-u", "http", "--endpoint", "/manager/healthz", "--redact-env"},
		{"app", "manager", "--guid"},
		{"set-env", "manager", "MANAGER_WEB_DIR", "./web"},
		{"set-env", "manager", "MANAGER_COLLIE_DIR", "./sandbox/runtime/collie"},
		{"set-env", "manager", "MANAGER_RUNTIME_DIR", "./manager-runtime"},
		{"set-env", "manager", "MANAGER_SANDBOX_RUNTIME_DIR", "./sandbox/runtime"},
		{"set-env", "manager", "MANAGER_BUN_EXECUTABLE", "./manager-runtime/bin/bun"},
		{"set-env", "manager", "MANAGER_COLLIE_EXECUTABLE", "./manager-runtime/bin/collie"},
		{"set-env", "manager", "MANAGER_CF_EXECUTABLE", "./manager-runtime/bin/cf"},
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
		for _, required := range []string{"DT_NEEDED", "dlopen", "live", "GLIBC_PRIVATE", "/home/vcap/app/sandbox-runtime/bin", "/home/vcap/app/manager-runtime/bin"} {
			if !strings.Contains(doc, required) {
				t.Errorf("%s missing relocation limitation %q", name, required)
			}
		}
	}
}

func TestRuntimeSpikeExercisesPackagedSandboxAtItsTargetAppLayout(t *testing.T) {
	doc := readPackageFile(t, "docs/spikes/cf-buildpack-runtime.md")
	for _, required := range []string{
		"cp -R dist/sandbox/runtime dist/runtime-spike/sandbox-runtime",
		"-p dist/runtime-spike",
		"-c './sandbox-runtime/start.sh'",
		"/home/vcap/app/sandbox-runtime/bin/.bun-libs/ld-linux-x86-64.so.2",
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
	mkdir -p "$RUNTIME_DIR/bin" "$RUNTIME_DIR/collie/bridge" "$RUNTIME_DIR/collie/cli" "$RUNTIME_DIR/collie/node_modules/fixture" "$RUNTIME_DIR/collie/web/dist"
for name in bun herdr collie opencode cf sandbox-bootstrap; do printf '#!/bin/sh\n' > "$RUNTIME_DIR/bin/$name"; chmod +x "$RUNTIME_DIR/bin/$name"; done
printf '#!/bin/sh\n' > "$RUNTIME_DIR/start.sh"; chmod +x "$RUNTIME_DIR/start.sh"
printf '#!/bin/sh\n' > "$RUNTIME_DIR/start-bash.sh"; chmod +x "$RUNTIME_DIR/start-bash.sh"
printf '%s\n' "${SANDBOX_TARGET_INSTALL_DIR:-/home/vcap/app/sandbox-runtime/bin}" > "$RUNTIME_DIR/target-install-dir"
printf fixture > "$RUNTIME_DIR/collie/bridge/index.ts"
for name in install-kind link sys; do printf fixture > "$RUNTIME_DIR/collie/cli/$name.ts"; done
printf '{}' > "$RUNTIME_DIR/collie/package.json"
printf '{}' > "$RUNTIME_DIR/collie/node_modules/fixture/package.json"
printf '<html>collie</html>' > "$RUNTIME_DIR/collie/web/dist/index.html"
 mkdir -p "$MANAGER_RUNTIME_DIR/bin/.cf-libs"
  for name in bun herdr collie; do printf '#!/bin/sh\n' > "$MANAGER_RUNTIME_DIR/bin/$name"; chmod +x "$MANAGER_RUNTIME_DIR/bin/$name"; done
 printf '#!/bin/sh\n' > "$MANAGER_RUNTIME_DIR/bin/cf"
 printf '#!/bin/sh\n' > "$MANAGER_RUNTIME_DIR/bin/cf.real"
 chmod +x "$MANAGER_RUNTIME_DIR/bin/cf" "$MANAGER_RUNTIME_DIR/bin/cf.real"
`
	writeExecutable(t, runtimeScript, runtimeBody)
	fakeRuntime := filepath.Join(temp, "portable")
	writeExecutable(t, fakeRuntime, "#!/bin/sh\n")
	command := exec.Command("bash", filepath.Join(root, "scripts", "build.sh"))
	command.Dir = root
	command.Env = []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"), "DIST_DIR=" + dist,
		"BUILD_RUNTIME_SCRIPT=" + runtimeScript, "BUN_RUNTIME_BIN=" + fakeRuntime,
		"HERDR_RUNTIME_BIN=" + fakeRuntime, "OPENCODE_RUNTIME_BIN=" + fakeRuntime, "CF_BIN=" + fakeRuntime, "FIXTURE_WEB_DIST=" + filepath.Join(root, "web", "dist"),
		"BUILD_MODE=nix-relocation",
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
