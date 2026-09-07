package scripts_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabDeployReportsMissingHerdrBeforeBuildOrCF(t *testing.T) {
	fixture := newLabDeployFixture(t)
	os.Remove(filepath.Join(fixture.bin, "herdr"))

	output, err := fixture.run(t)
	if err == nil {
		t.Fatal("lab-deploy.sh succeeded without herdr")
	}
	if !strings.Contains(output, "required tool herdr was not found") || !strings.Contains(output, "HERDR_RUNTIME_BIN") {
		t.Fatalf("output = %q, want actionable herdr error", output)
	}
	if events := fixture.events(t); len(events) != 0 {
		t.Fatalf("events = %#v, want no build or CF calls", events)
	}
}

func TestLabDeployUsesExplicitCFBinaryOutsidePATH(t *testing.T) {
	fixture := newLabDeployFixture(t)
	os.Remove(filepath.Join(fixture.bin, "cf"))
	explicitCF := filepath.Join(t.TempDir(), "explicit cf")
	writeFakeCF(t, explicitCF, "explicit-cf")
	fixture.env = append(fixture.env, "CF_BIN="+explicitCF)

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	for _, event := range fixture.events(t) {
		if strings.HasPrefix(event, "cf\t") && !strings.HasPrefix(event, "explicit-cf\t") {
			t.Fatalf("CF event = %q, want explicit CF binary", event)
		}
	}
	if !containsEvent(fixture.events(t), "explicit-cf\tstart\tmanager") {
		t.Fatal("explicit CF binary did not execute the full deployment")
	}
}

func TestLabDeployDiscoversOperatorProfileTools(t *testing.T) {
	fixture := newLabDeployFixture(t)
	profileBin := filepath.Join(t.TempDir(), "profile bin")
	if err := os.Mkdir(profileBin, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(fixture.bin, "cf"))
	os.Remove(filepath.Join(fixture.bin, "herdr"))
	writeFakeCF(t, filepath.Join(profileBin, "cf"), "profile-cf")
	writeExecutable(t, filepath.Join(profileBin, "herdr"), "#!/bin/sh\nexit 0\n")
	fixture.env = append(fixture.env, "LAB_PROFILE_BIN_DIR="+profileBin)

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	buildEnv, err := os.ReadFile(fixture.buildEnvLog)
	if err != nil {
		t.Fatal(err)
	}
	want := "bun=" + filepath.Join(fixture.bin, "bun") + "\nherdr=" + filepath.Join(profileBin, "herdr") + "\ncf=" + filepath.Join(profileBin, "cf") + "\n"
	if string(buildEnv) != want {
		t.Fatalf("build runtime paths = %q, want %q", buildEnv, want)
	}
	if !containsEvent(fixture.events(t), "profile-cf\tstart\tmanager") {
		t.Fatal("profile CF binary was not used")
	}
}

func TestLabDeployExplicitRuntimeOverridesPATH(t *testing.T) {
	fixture := newLabDeployFixture(t)
	explicitDir := t.TempDir()
	explicitBun := filepath.Join(explicitDir, "explicit bun")
	explicitHerdr := filepath.Join(explicitDir, "explicit herdr")
	writeExecutable(t, explicitBun, "#!/bin/sh\nexit 0\n")
	writeExecutable(t, explicitHerdr, "#!/bin/sh\nexit 0\n")
	fixture.env = append(fixture.env, "BUN_RUNTIME_BIN="+explicitBun, "HERDR_RUNTIME_BIN="+explicitHerdr)

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	buildEnv, err := os.ReadFile(fixture.buildEnvLog)
	if err != nil {
		t.Fatal(err)
	}
	want := "bun=" + explicitBun + "\nherdr=" + explicitHerdr + "\ncf=" + filepath.Join(fixture.bin, "cf") + "\n"
	if string(buildEnv) != want {
		t.Fatalf("build runtime paths = %q, want explicit paths %q", buildEnv, want)
	}
}

func TestLabDeployRejectsInvalidExplicitTool(t *testing.T) {
	fixture := newLabDeployFixture(t)
	invalid := filepath.Join(t.TempDir(), "cf-directory")
	if err := os.Mkdir(invalid, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.env = append(fixture.env, "CF_BIN="+invalid)

	output, err := fixture.run(t)
	if err == nil {
		t.Fatal("lab-deploy.sh succeeded with a directory as CF_BIN")
	}
	if !strings.Contains(output, "CF_BIN must name an executable file") || !strings.Contains(output, invalid) {
		t.Fatalf("output = %q, want actionable CF_BIN error", output)
	}
	if events := fixture.events(t); len(events) != 0 {
		t.Fatalf("events = %#v, want no build or CF calls", events)
	}
}

func TestLabDeployAcceptsExecutableSymlink(t *testing.T) {
	fixture := newLabDeployFixture(t)
	target := filepath.Join(t.TempDir(), "herdr-target")
	link := filepath.Join(t.TempDir(), "herdr-link")
	writeExecutable(t, target, "#!/bin/sh\nexit 0\n")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	fixture.env = append(fixture.env, "HERDR_RUNTIME_BIN="+link)

	if output, err := fixture.run(t); err != nil {
		t.Fatalf("lab-deploy.sh rejected executable symlink: %v: %s", err, output)
	}
}

func TestLabDeployPreservesBuildAndCFSequence(t *testing.T) {
	fixture := newLabDeployFixture(t)

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	want := []string{
		"build",
		"cf\tpush\tmanager\t--no-manifest\t-p\tdist\t-b\tbinary_buildpack\t-c\t./manager\t--no-route\t--no-start\t-u\thttp\t--endpoint\t/manager/healthz\t--redact-env",
		"cf\tapp\tmanager\t--guid",
		"cf\tset-env\tmanager\tMANAGER_WEB_DIR\t./web",
		"cf\tset-env\tmanager\tMANAGER_COLLIE_DIR\t./sandbox/runtime/collie",
		"cf\tset-env\tmanager\tMANAGER_RUNTIME_DIR\t./manager-runtime",
		"cf\tset-env\tmanager\tMANAGER_BUN_EXECUTABLE\t./manager-runtime/bin/bun",
		"cf\tset-env\tmanager\tMANAGER_COLLIE_EXECUTABLE\t./manager-runtime/bin/collie",
		"cf\tset-env\tmanager\tMANAGER_CF_EXECUTABLE\t./manager-runtime/bin/cf",
		"cf\tset-env\tmanager\tCF_IDENTITY_DOMAIN\tapps.identity",
		"cf\tset-env\tmanager\tSANDBOX_BUILDPACKS\truby_buildpack",
		"cf\tset-env\tmanager\tMANAGER_APP_NAME\tmanager",
		"cf\tset-env\tmanager\tMANAGER_APP_GUID\tmanager-guid",
		"cf\tset-env\tmanager\tMANAGER_PACK_HOST\tmanager-pack.apps.identity",
		"cf\tset-env\tmanager\tMANAGER_API_TOKEN\tsecret-token",
		"cf\tcreate-route\tapps.example\t--hostname\tmanager",
		"cf\tmap-route\tmanager\tapps.example\t--hostname\tmanager",
		"cf\tcreate-route\tapps.identity\t--hostname\tmanager-pack",
		"cf\tmap-route\tmanager\tapps.identity\t--hostname\tmanager-pack",
		"cf\tstart\tmanager",
	}
	if got := fixture.events(t); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	for _, phase := range []string{"build distribution", "push manager", "configure routes", "start manager"} {
		if !strings.Contains(output, phase) {
			t.Errorf("output missing phase %q: %s", phase, output)
		}
	}
	for _, secret := range []string{"old-secret-token", "secret-token"} {
		if strings.Contains(output, secret) {
			t.Fatalf("deploy output exposed %q", secret)
		}
	}
}

func TestLabDeployUsesSafeTemporaryDirectory(t *testing.T) {
	fixture := newLabDeployFixture(t)
	fixture.env = append(fixture.env, "TMPDIR="+filepath.Join(packageRoot(t), "tmp.leaked"))

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	contents, err := os.ReadFile(fixture.tmpdirLog)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(contents))
	if got != "/tmp" || strings.HasPrefix(got, packageRoot(t)+string(os.PathSeparator)) {
		t.Fatalf("build TMPDIR = %q, want /tmp outside repository", got)
	}
}

func TestLabDeployRejectsNonexistentTemporaryDirectory(t *testing.T) {
	fixture := newLabDeployFixture(t)
	tmpdir := filepath.Join(t.TempDir(), "missing")
	fixture.env = append(fixture.env, "DEPLOY_TMPDIR="+tmpdir)

	output, err := fixture.run(t)
	if err == nil {
		t.Fatal("lab-deploy.sh succeeded with nonexistent DEPLOY_TMPDIR")
	}
	if !strings.Contains(output, "cannot create temporary files") || !strings.Contains(output, "DEPLOY_TMPDIR") {
		t.Fatalf("output = %q, want actionable temporary-directory error", output)
	}
	if events := fixture.events(t); len(events) != 0 {
		t.Fatalf("events = %#v, want no build or CF calls", events)
	}
}

func TestLabDeployRejectsNonsearchableTemporaryDirectory(t *testing.T) {
	fixture := newLabDeployFixture(t)
	tmpdir := t.TempDir()
	if err := os.Chmod(tmpdir, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tmpdir, 0o700) })
	permissionProbe := filepath.Join(tmpdir, "permission-probe")
	if err := os.WriteFile(permissionProbe, nil, 0o600); err == nil {
		_ = os.Remove(permissionProbe)
		t.Skip("executing identity bypasses directory search permission")
	}
	fixture.env = append(fixture.env, "DEPLOY_TMPDIR="+tmpdir)

	output, err := fixture.run(t)
	if err == nil {
		t.Fatal("lab-deploy.sh succeeded with nonsearchable DEPLOY_TMPDIR")
	}
	if !strings.Contains(output, "cannot create temporary files") {
		t.Fatalf("output = %q, want actionable temporary-directory error", output)
	}
}

func TestLabDeployReportsMktempFailure(t *testing.T) {
	fixture := newLabDeployFixture(t)
	fixture.env = append(fixture.env, "FAKE_MKTEMP_STATUS=24")

	output, err := fixture.run(t)
	if err == nil {
		t.Fatal("lab-deploy.sh succeeded when mktemp failed")
	}
	if !strings.Contains(output, "cannot create temporary files") || !strings.Contains(output, "DEPLOY_TMPDIR") {
		t.Fatalf("output = %q, want actionable mktemp error", output)
	}
}

func TestLabDeployDoesNotExposeTokenFromSuccessfulCFOutput(t *testing.T) {
	fixture := newLabDeployFixture(t)
	tmpdir := t.TempDir()
	fixture.env = append(fixture.env, "FAKE_CF_ECHO_TOKEN=1", "DEPLOY_TMPDIR="+tmpdir)

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("deploy output exposed token: %s", output)
	}
	if !containsEvent(fixture.events(t), "cf\tset-env\tmanager\tMANAGER_API_TOKEN\tsecret-token") {
		t.Fatal("token-setting CF command was not called with the token")
	}
	assertNoSecretOutputFile(t, fixture, tmpdir)
}

func TestLabDeployRedactsExistingEnvironmentDuringPush(t *testing.T) {
	fixture := newLabDeployFixture(t)
	fixture.env = append(fixture.env, "FAKE_CF_EXISTING_TOKEN=old-secret-token")

	output, err := fixture.run(t)
	if err != nil {
		t.Fatalf("lab-deploy.sh: %v: %s", err, output)
	}
	for _, secret := range []string{"old-secret-token", "secret-token"} {
		if strings.Contains(output, secret) {
			t.Fatalf("deploy output exposed %q: %s", secret, output)
		}
	}
	if !containsEvent(fixture.events(t), "cf\tpush\tmanager\t--no-manifest\t-p\tdist\t-b\tbinary_buildpack\t-c\t./manager\t--no-route\t--no-start\t-u\thttp\t--endpoint\t/manager/healthz\t--redact-env") {
		t.Fatal("push did not use the exact environment-preserving redacted arguments")
	}
}

func TestLabDeployDoesNotExposeTokenFromFailedCFOutput(t *testing.T) {
	fixture := newLabDeployFixture(t)
	tmpdir := t.TempDir()
	fixture.env = append(fixture.env, "FAKE_CF_ECHO_TOKEN=1", "FAKE_CF_TOKEN_STATUS=25", "DEPLOY_TMPDIR="+tmpdir)

	output, err := fixture.run(t)
	if err == nil {
		t.Fatal("lab-deploy.sh succeeded when token-setting CF command failed")
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("deploy failure output exposed token: %s", output)
	}
	if !strings.Contains(output, "failed to set MANAGER_API_TOKEN") {
		t.Fatalf("output = %q, want generic token-setting failure", output)
	}
	if !containsEvent(fixture.events(t), "cf\tset-env\tmanager\tMANAGER_API_TOKEN\tsecret-token") {
		t.Fatal("token-setting CF command was not called with the token")
	}
	assertNoSecretOutputFile(t, fixture, tmpdir)
}

func TestLabDeployBuildFailurePreventsCFCalls(t *testing.T) {
	fixture := newLabDeployFixture(t)
	fixture.env = append(fixture.env, "FAKE_BUILD_STATUS=23")

	if output, err := fixture.run(t); err == nil {
		t.Fatalf("lab-deploy.sh succeeded after build failure: %s", output)
	}
	if got := fixture.events(t); len(got) != 1 || got[0] != "build" {
		t.Fatalf("events = %#v, want only failed build", got)
	}
}

type labDeployFixture struct {
	bin         string
	eventLog    string
	tmpdirLog   string
	buildEnvLog string
	env         []string
}

func newLabDeployFixture(t *testing.T) *labDeployFixture {
	t.Helper()
	temp := t.TempDir()
	fixture := &labDeployFixture{
		bin:         filepath.Join(temp, "bin"),
		eventLog:    filepath.Join(temp, "events.log"),
		tmpdirLog:   filepath.Join(temp, "tmpdir.log"),
		buildEnvLog: filepath.Join(temp, "build-env.log"),
	}
	if err := os.Mkdir(fixture.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fixture.bin, "bash"), `#!/bin/sh
printf 'build\n' >> "$EVENT_LOG"
printf '%s\n' "$TMPDIR" > "$TMPDIR_LOG"
	printf 'bun=%s\nherdr=%s\ncf=%s\n' "$BUN_RUNTIME_BIN" "$HERDR_RUNTIME_BIN" "$CF_BIN" > "$BUILD_ENV_LOG"
exit "${FAKE_BUILD_STATUS:-0}"
`)
	writeFakeCF(t, filepath.Join(fixture.bin, "cf"), "cf")
	writeExecutable(t, filepath.Join(fixture.bin, "mktemp"), `#!/bin/sh
if [ "${FAKE_MKTEMP_STATUS:-}" != "" ]; then exit "$FAKE_MKTEMP_STATUS"; fi
template=$1
printf '%s\n' "$template" >> "$MKTEMP_LOG"
path="${template%XXXXXX}fixture"
: > "$path" || exit 1
printf '%s\n' "$path"
`)
	rm, err := exec.LookPath("rm")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rm, filepath.Join(fixture.bin, "rm")); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"bun", "herdr", "go", "patchelf", "readelf", "ldd", "nix-store"} {
		writeExecutable(t, filepath.Join(fixture.bin, tool), "#!/bin/sh\nexit 0\n")
	}
	fixture.env = []string{
		"PATH=" + fixture.bin, "EVENT_LOG=" + fixture.eventLog, "TMPDIR_LOG=" + fixture.tmpdirLog,
		"BUILD_ENV_LOG=" + fixture.buildEnvLog,
		"BUILD_MODE=nix-relocation",
		"MKTEMP_LOG=" + filepath.Join(temp, "mktemp.log"),
		"MANAGER_APP_NAME=manager", "PUBLIC_DOMAIN=apps.example", "MANAGER_PUBLIC_HOST=manager",
		"CF_IDENTITY_DOMAIN=apps.identity", "MANAGER_PACK_HOST=manager-pack.apps.identity",
		"SANDBOX_BUILDPACKS=ruby_buildpack", "MANAGER_API_TOKEN=secret-token",
	}
	return fixture
}

func writeFakeCF(t *testing.T, path, label string) {
	t.Helper()
	body := `#!/bin/sh
CF_LABEL='__LABEL__'
printf '%s' "$CF_LABEL" >> "$EVENT_LOG"
printf '\t%s' "$@" >> "$EVENT_LOG"
printf '\n' >> "$EVENT_LOG"
if [ "$1" = app ] && [ "$3" = --guid ]; then printf 'manager-guid\n'; fi
if [ "$1" = push ] && [ "${FAKE_CF_EXISTING_TOKEN:-}" != "" ]; then
  args=" $* "
  case "$args" in *" --no-manifest "*) no_manifest=1;; *) no_manifest=0;; esac
  case "$args" in *" --redact-env "*) redact_env=1;; *) redact_env=0;; esac
  if [ "$no_manifest" != 1 ] || [ "$redact_env" != 1 ]; then
    printf -- '- MANAGER_API_TOKEN: %s\n' "$FAKE_CF_EXISTING_TOKEN"
  fi
fi
if [ "$1" = set-env ] && [ "$3" = MANAGER_API_TOKEN ] && [ "${FAKE_CF_ECHO_TOKEN:-}" = 1 ]; then
  printf 'cf echoed token argument: %s\n' "$4"
  printf 'cf echoed token error: %s\n' "$4" >&2
  exit "${FAKE_CF_TOKEN_STATUS:-0}"
fi
`
	writeExecutable(t, path, strings.Replace(body, "__LABEL__", label, 1))
}

func assertNoSecretOutputFile(t *testing.T, fixture *labDeployFixture, tmpdir string) {
	t.Helper()
	entries, err := os.ReadDir(tmpdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("DEPLOY_TMPDIR contains residual files: %#v", entries)
	}
	contents, err := os.ReadFile(envValue(fixture.env, "MKTEMP_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.TrimSpace(string(contents)), "\n") + 1; got != 1 {
		t.Fatalf("mktemp was called %d times, want only the directory probe", got)
	}
}

func envValue(env []string, name string) string {
	prefix := name + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func (fixture *labDeployFixture) run(t *testing.T) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bash, filepath.Join(packageRoot(t), "scripts", "lab-deploy.sh"))
	command.Dir = packageRoot(t)
	command.Env = fixture.env
	output, err := command.CombinedOutput()
	return string(output), err
}

func (fixture *labDeployFixture) events(t *testing.T) []string {
	t.Helper()
	file, err := os.Open(fixture.eventLog)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		events = append(events, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
