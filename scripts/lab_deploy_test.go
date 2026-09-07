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
	if !strings.Contains(output, "required runtime herdr was not found in PATH") || !strings.Contains(output, "HERDR_RUNTIME_BIN") {
		t.Fatalf("output = %q, want actionable herdr error", output)
	}
	if events := fixture.events(t); len(events) != 0 {
		t.Fatalf("events = %#v, want no build or CF calls", events)
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
		"cf\tpush\tmanager\t-f\tmanifest.yml\t--no-route\t--no-start",
		"cf\tapp\tmanager\t--guid",
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
	if strings.Contains(output, "secret-token") {
		t.Fatal("deploy output exposed MANAGER_API_TOKEN")
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
	bin       string
	eventLog  string
	tmpdirLog string
	env       []string
}

func newLabDeployFixture(t *testing.T) *labDeployFixture {
	t.Helper()
	temp := t.TempDir()
	fixture := &labDeployFixture{
		bin:       filepath.Join(temp, "bin"),
		eventLog:  filepath.Join(temp, "events.log"),
		tmpdirLog: filepath.Join(temp, "tmpdir.log"),
	}
	if err := os.Mkdir(fixture.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fixture.bin, "bash"), `#!/bin/sh
printf 'build\n' >> "$EVENT_LOG"
printf '%s\n' "$TMPDIR" > "$TMPDIR_LOG"
exit "${FAKE_BUILD_STATUS:-0}"
`)
	writeExecutable(t, filepath.Join(fixture.bin, "cf"), `#!/bin/sh
printf 'cf' >> "$EVENT_LOG"
printf '\t%s' "$@" >> "$EVENT_LOG"
printf '\n' >> "$EVENT_LOG"
if [ "$1" = app ] && [ "$3" = --guid ]; then printf 'manager-guid\n'; fi
`)
	for _, tool := range []string{"bun", "herdr", "go", "patchelf", "readelf", "ldd", "nix-store"} {
		writeExecutable(t, filepath.Join(fixture.bin, tool), "#!/bin/sh\nexit 0\n")
	}
	fixture.env = []string{
		"PATH=" + fixture.bin, "EVENT_LOG=" + fixture.eventLog, "TMPDIR_LOG=" + fixture.tmpdirLog,
		"MANAGER_APP_NAME=manager", "PUBLIC_DOMAIN=apps.example", "MANAGER_PUBLIC_HOST=manager",
		"CF_IDENTITY_DOMAIN=apps.identity", "MANAGER_PACK_HOST=manager-pack.apps.identity",
		"SANDBOX_BUILDPACKS=ruby_buildpack", "MANAGER_API_TOKEN=secret-token",
	}
	return fixture
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
