package scripts_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectSandboxPushesStandaloneAppWithExactContract(t *testing.T) {
	f := newDirectSandboxFixture(t)
	out, err := f.run()
	if err != nil {
		t.Fatalf("direct-sandbox.sh: %v: %s", err, out)
	}
	events := f.events(t)
	if !containsPrefix(events, "git\tclone\t--depth\t1\t--\thttps://github.com/cloudfoundry-samples/cf-sample-app-nodejs.git\t") {
		t.Fatalf("events = %#v, want safe clone arguments", events)
	}
	if !containsEvent(events, "cf\tpush\tdirect-sandbox\t--no-route\t--no-start\t-b\tnodejs_buildpack\t-p\tapp\t-c\t./sandbox-runtime/start.sh") {
		t.Fatalf("events = %#v, want exact standalone push", events)
	}
	if containsPrefix(events, "cf\tcreate-route") || containsPrefix(events, "cf\tmap-route") {
		t.Fatalf("events = %#v, direct workflow must not create routes", events)
	}
	if strings.Contains(out, "secret-token") || strings.Contains(out, "join-token") {
		t.Fatalf("output exposed a secret: %s", out)
	}
	if strings.Contains(f.eventText(t), "COLLIE_JOIN_TOKEN_FILE") || strings.Contains(f.eventText(t), "COLLIE_PACK_LEAD_ADDRESS") {
		t.Fatalf("standalone workflow configured Pack join: %s", f.eventText(t))
	}
	for _, text := range []string{"cf logs direct-sandbox", "cf app direct-sandbox", "cf ssh direct-sandbox", "cf delete direct-sandbox -f -r"} {
		if !strings.Contains(out, text) {
			t.Errorf("output missing %q: %s", text, out)
		}
	}
}

func TestDirectSandboxRefusesExistingAppWithoutDeletingIt(t *testing.T) {
	f := newDirectSandboxFixture(t)
	f.env = append(f.env, "FAKE_CF_EXISTING=1")
	out, err := f.run()
	if err == nil || !strings.Contains(out, "already exists") {
		t.Fatalf("output = %q, err = %v, want existing-app refusal", out, err)
	}
	if containsEvent(f.events(t), "cf\tdelete\tdirect-sandbox\t-f\t-r") {
		t.Fatalf("events = %#v, pre-existing app must not be deleted", f.events(t))
	}
}

func TestDirectSandboxCleansFailedPushUnlessKept(t *testing.T) {
	for _, keep := range []string{"", "1"} {
		t.Run("keep="+keep, func(t *testing.T) {
			f := newDirectSandboxFixture(t)
			f.env = append(f.env, "FAKE_CF_PUSH_STATUS=9", "SANDBOX_KEEP="+keep)
			out, err := f.run()
			if err == nil {
				t.Fatalf("direct-sandbox.sh succeeded: %s", out)
			}
			deleted := containsEvent(f.events(t), "cf\tdelete\tdirect-sandbox\t-f\t-r")
			if deleted != (keep == "") {
				t.Fatalf("events = %#v, keep=%q, unexpected deletion", f.events(t), keep)
			}
		})
	}
}

func TestDirectSandboxRejectsPartialJoinBeforeCreatingApp(t *testing.T) {
	f := newDirectSandboxFixture(t)
	tokenPath := filepath.Join(f.root, "join-token")
	if err := os.WriteFile(tokenPath, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env, "COLLIE_JOIN_TOKEN_FILE="+tokenPath)

	out, err := f.run()
	if err == nil || !strings.Contains(out, "COLLIE_PACK_LEAD_ADDRESS is required") {
		t.Fatalf("output = %q, err = %v, want partial-join refusal", out, err)
	}
	if len(f.events(t)) != 0 {
		t.Fatalf("events = %#v, partial join must fail before CF or clone", f.events(t))
	}
	if strings.Contains(out, "secret-token") {
		t.Fatalf("output exposed token: %s", out)
	}
}

func TestDirectSandboxStagesJoinTokenBeforePushWithoutPrintingIt(t *testing.T) {
	f := newDirectSandboxFixture(t)
	tokenPath := filepath.Join(f.root, "join-token")
	if err := os.WriteFile(tokenPath, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env,
		"COLLIE_JOIN_TOKEN_FILE="+tokenPath,
		"COLLIE_PACK_LEAD_ADDRESS=lead.example:443",
	)

	out, err := f.run()
	if err != nil {
		t.Fatalf("direct-sandbox.sh: %v: %s", err, out)
	}
	events := f.events(t)
	push := eventIndex(events, "cf\tpush\tdirect-sandbox\t--no-route\t--no-start\t-b\tnodejs_buildpack\t-p\tapp\t-c\t./sandbox-runtime/start.sh")
	if push < 0 {
		t.Fatalf("events = %#v, want push", events)
	}
	clone := eventIndexPrefix(events, "git\tclone\t")
	if clone < 0 || clone > push {
		t.Fatalf("events = %#v, want clone before push", events)
	}
	if eventIndex(events, "cf\tset-env\tdirect-sandbox\tCOLLIE_PACK_LEAD_ADDRESS\tlead.example:443") < push {
		t.Fatalf("events = %#v, join configuration must not be sent before push", events)
	}
	if !containsEvent(events, "token-staged") {
		t.Fatalf("events = %#v, want join token staged before push", events)
	}
	if eventIndex(events, "token-staged") > push {
		t.Fatalf("events = %#v, join token must be staged before push", events)
	}
	if eventIndex(events, "cf\tstart\tdirect-sandbox") <= eventIndex(events, "cf\tset-env\tdirect-sandbox\tCOLLIE_JOIN_TOKEN_FILE\t/home/vcap/app/sandbox-runtime/join-token") {
		t.Fatalf("events = %#v, want start after join environment", events)
	}
	if !strings.Contains(out, "cf logs direct-sandbox") || strings.Contains(out, "secret-token") {
		t.Fatalf("output = %q, want diagnostics without token", out)
	}
	if strings.Contains(f.eventText(t), "secret-token") {
		t.Fatalf("events exposed token: %s", f.eventText(t))
	}
}

func TestDirectSandboxUsesPackagedLauncher(t *testing.T) {
	script := string(mustReadDirectSandboxScript(t))
	if strings.Contains(script, "cat >\"$WORK_DIR/app/sandbox-runtime/start.sh\"") {
		t.Fatal("direct workflow replaces the packaged launcher")
	}
}

func TestDirectSandboxRejectsManagerRuntimeArtifact(t *testing.T) {
	f := newDirectSandboxFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "dist", "sandbox", "runtime", "target-install-dir"), []byte("/home/vcap/app/manager-runtime/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := f.run()
	if err == nil || !strings.Contains(out, "do not use a manager artifact") {
		t.Fatalf("output = %q, err = %v, want manager artifact refusal", out, err)
	}
}

func mustReadDirectSandboxScript(t *testing.T) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(packageRoot(t), "scripts", "direct-sandbox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

type directSandboxFixture struct {
	root, bin, eventsPath string
	env                   []string
}

func newDirectSandboxFixture(t *testing.T) *directSandboxFixture {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dist", "sandbox", "runtime", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mktemp", "rm", "cp", "find", "readlink", "chmod"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	mkdirPath, err := exec.LookPath("mkdir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mkdirPath, filepath.Join(bin, "mkdir")); err != nil {
		t.Fatal(err)
	}
	catPath, err := exec.LookPath("cat")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(catPath, filepath.Join(bin, "cat")); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(packageRoot(t), "scripts", "direct-sandbox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "direct-sandbox.sh"), source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "git"), `#!/bin/sh
printf 'git' >> "$EVENTS"
printf '\t%s' "$@" >> "$EVENTS"
printf '\n' >> "$EVENTS"
if [ "$1" = clone ]; then mkdir -p "$6"; fi
`)
	writeExecutable(t, filepath.Join(bin, "cf"), `#!/bin/sh
if [ "$1" = push ] && [ -f app/sandbox-runtime/join-token ]; then printf 'token-staged\n' >> "$EVENTS"; fi
printf 'cf' >> "$EVENTS"
printf '\t%s' "$@" >> "$EVENTS"
printf '\n' >> "$EVENTS"
if [ "$1" = app ] && [ "${FAKE_CF_EXISTING:-}" = 1 ]; then exit 0; fi
if [ "$1" = push ]; then
  if [ "${FAKE_CF_PUSH_STATUS:-}" != "" ]; then exit "$FAKE_CF_PUSH_STATUS"; fi
fi
if [ "$1" = app ]; then exit 1; fi
exit 0
`)
	writeExecutable(t, filepath.Join(root, "dist", "sandbox", "runtime", "start.sh"), "#!/bin/sh\n")
	if err := os.WriteFile(filepath.Join(root, "dist", "sandbox", "runtime", "target-install-dir"), []byte("/home/vcap/app/sandbox-runtime/bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dist", "sandbox", "runtime", "collie", "bridge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "sandbox", "runtime", "collie", "bridge", "index.ts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "sandbox", "runtime", "collie", "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bun", "herdr", "collie", "sandbox-bootstrap"} {
		writeExecutable(t, filepath.Join(root, "dist", "sandbox", "runtime", "bin", name), "#!/bin/sh\n")
	}
	events := filepath.Join(root, "events.log")
	return &directSandboxFixture{root: root, bin: bin, eventsPath: events, env: []string{
		"PATH=" + bin + ":/usr/bin:/bin", "EVENTS=" + events, "HOME=" + filepath.Join(root, "home"), "TMPDIR=" + root,
		"CF_API=https://api.example", "CF_ORG=org", "CF_SPACE=space", "DIRECT_SANDBOX=1",
	}}
}

func (f *directSandboxFixture) run() (string, error) {
	command := exec.Command("bash", filepath.Join(f.root, "scripts", "direct-sandbox.sh"))
	command.Dir = f.root
	command.Env = f.env
	return stringOutput(command)
}

func stringOutput(command *exec.Cmd) (string, error) {
	out, err := command.CombinedOutput()
	return string(out), err
}

func (f *directSandboxFixture) events(t *testing.T) []string {
	t.Helper()
	file, err := os.Open(f.eventsPath)
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
	return events
}

func (f *directSandboxFixture) eventText(t *testing.T) string { return strings.Join(f.events(t), "\n") }

func containsPrefix(events []string, prefix string) bool {
	for _, event := range events {
		if strings.HasPrefix(event, prefix) {
			return true
		}
	}
	return false
}

func eventIndex(events []string, want string) int {
	for i, event := range events {
		if event == want {
			return i
		}
	}
	return -1
}

func eventIndexPrefix(events []string, prefix string) int {
	for i, event := range events {
		if strings.HasPrefix(event, prefix) {
			return i
		}
	}
	return -1
}
