package cf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cf-herdr-poc/internal/runner"
)

const (
	appGUID     = "123e4567-e89b-12d3-a456-426614174000"
	managerGUID = "123e4567-e89b-12d3-a456-426614174001"
)

type command struct {
	name string
	args []string
}

type recordingRunner struct {
	commands []command
	outputs  [][]byte
	errors   []error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, command{name: name, args: append([]string(nil), args...)})
	index := len(r.commands) - 1
	var output []byte
	var err error
	if index < len(r.outputs) {
		output = r.outputs[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return output, err
}

func TestStageUsesExactArgvWithoutStartingOrAdoptingGUID(t *testing.T) {
	_, statErr := os.Stat("/tmp/work/demo")
	if errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll("/tmp/work/demo", 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove("/tmp/work/demo"); _ = os.Remove("/tmp/work") })
	} else if statErr != nil {
		t.Fatal(statErr)
	}
	run := &recordingRunner{outputs: [][]byte{[]byte("App 'demo' not found"), nil}, errors: []error{errors.New("exit status 1"), nil}}
	provider := Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}

	operation, err := provider.Stage(context.Background(), PushRequest{
		Name: "demo", Buildpack: "ruby_buildpack", BitsPath: "/tmp/work/demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []command{{name: "cf", args: []string{"app", "demo", "--guid"}}, {name: "cf", args: []string{"push", "demo", "--no-route", "--no-start", "-b", "ruby_buildpack", "-p", "/tmp/work/demo", "-c", "./.sandbox/start.sh"}}}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want %#v", run.commands, want)
	}
	if !operation.Success || operation.Name != "stage" {
		t.Fatalf("Stage() = %#v, want successful stage operation", operation)
	}
}

func TestStageRefusesExistingAppNameWithoutPush(t *testing.T) {
	bitsPath := t.TempDir()
	run := &recordingRunner{outputs: [][]byte{[]byte(appGUID)}}

	operation, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)

	var providerErr *Error
	if !errors.As(err, &providerErr) || !providerErr.AlreadyExists() {
		t.Fatalf("Stage() error = %T %v, want typed existing-app conflict", err, err)
	}
	if err.Error() != "stage app name already exists" {
		t.Fatalf("Stage() error = %q", err)
	}
	if operation.Success || len(run.commands) != 1 || !reflect.DeepEqual(run.commands[0].args, []string{"app", "demo", "--guid"}) {
		t.Fatalf("Stage() = %#v, commands = %#v", operation, run.commands)
	}
}

func TestStagePushesWhenAppNameIsAbsent(t *testing.T) {
	bitsPath := t.TempDir()
	run := &recordingRunner{
		outputs: [][]byte{[]byte("App 'demo' not found"), nil},
		errors:  []error{errors.New("exit status 1"), nil},
	}

	operation, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)

	if err != nil || !operation.Success {
		t.Fatalf("Stage() = (%#v, %v)", operation, err)
	}
	want := []command{
		{name: "cf", args: []string{"app", "demo", "--guid"}},
		{name: "cf", args: []string{"push", "demo", "--no-route", "--no-start", "-b", "ruby_buildpack", "-p", bitsPath, "-c", "./.sandbox/start.sh"}},
	}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want %#v", run.commands, want)
	}
}

func TestConfigureEnrollmentAndStartAppUseSeparateExactCommands(t *testing.T) {
	run := &recordingRunner{}
	provider := Provider{Run: run}
	configure, err := provider.ConfigureEnrollment(context.Background(), "demo", "/home/vcap/app/.sandbox/join-token", "https://manager.identity.example")
	if err != nil {
		t.Fatal(err)
	}
	start, err := provider.StartApp(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	want := []command{{name: "cf", args: []string{"set-env", "demo", "COLLIE_JOIN_TOKEN_FILE", "/home/vcap/app/.sandbox/join-token"}}, {name: "cf", args: []string{"set-env", "demo", "COLLIE_PACK_LEAD_ADDRESS", "https://manager.identity.example"}}, {name: "cf", args: []string{"set-env", "demo", "SANDBOX_MEMBER_ID", "demo"}}, {name: "cf", args: []string{"start", "demo"}}}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want %#v", run.commands, want)
	}
	if configure.Name != "configure-enrollment" || start.Name != "start-app" {
		t.Fatalf("operations = %#v, %#v", configure, start)
	}
}

func TestProviderClassifiesCommonCFResourceErrors(t *testing.T) {
	tests := []struct {
		name, operation, output string
		exists, absent          bool
	}{{"route exists", "secure-route", "Route demo.identity.example already exists", true, false}, {"policy exists", "add-route-policy", "Route policy already exists.", true, false}, {"app missing", "delete-app", "App 'demo' not found", false, true}, {"route missing", "remove-route", "Route demo.identity.example does not exist", false, true}, {"policy missing", "remove-route-policy", "Route policy not found", false, true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &recordingRunner{outputs: [][]byte{[]byte(tt.output)}, errors: []error{errors.New("exit status 1")}}
			operation, _, err := (Provider{Run: run}).execute(context.Background(), tt.operation, "ignored")
			var providerErr *Error
			if !errors.As(err, &providerErr) {
				t.Fatalf("error=%T %v", err, err)
			}
			if providerErr.AlreadyExists() != tt.exists || providerErr.Absent() != tt.absent {
				t.Fatalf("classification=(%v,%v), want (%v,%v)", providerErr.AlreadyExists(), providerErr.Absent(), tt.exists, tt.absent)
			}
			if !strings.Contains(operation.Summary, tt.output) {
				t.Fatalf("summary=%q", operation.Summary)
			}
		})
	}
}

func TestSecureRouteContinuesAfterCreateAlreadyExists(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte("Route already exists"), nil, nil}, errors: []error{errors.New("exit status 1"), nil, nil}}
	operation, err := (Provider{Run: run}).SecureRoute(context.Background(), RouteRequest{AppName: "demo", AppGUID: appGUID, Domain: "identity.example", Host: "demo", SourceAppGUID: managerGUID})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.commands) != 3 || !operation.Success {
		t.Fatalf("commands=%#v operation=%#v", run.commands, operation)
	}
}

func TestRemoveRouteContinuesAfterResourcesAreAbsent(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte("Route policy not found"), []byte(appGUID), []byte("Route mapping does not exist"), []byte("Route not found")}, errors: []error{errors.New("exit 1"), nil, errors.New("exit 1"), errors.New("exit 1")}}
	operation, err := (Provider{Run: run}).RemoveRoute(context.Background(), RouteRequest{AppName: "demo", AppGUID: appGUID, Domain: "identity.example", Host: "demo", SourceAppGUID: managerGUID})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.commands) != 4 || !operation.Success {
		t.Fatalf("commands=%#v operation=%#v", run.commands, operation)
	}
}

func TestStageRetainsSanitizedDiagnostics(t *testing.T) {
	bitsPath := t.TempDir()
	run := &recordingRunner{outputs: [][]byte{[]byte("App 'demo' not found"), []byte("stage diagnostic")}, errors: []error{errors.New("exit status 1"), nil}}
	operation, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Summary != "stage diagnostic" {
		t.Fatalf("Summary = %q", operation.Summary)
	}
	if len(operation.Summary) > maxOperationSummaryBytes {
		t.Fatalf("Summary length = %d, want <= %d", len(operation.Summary), maxOperationSummaryBytes)
	}
}

func TestStageRetainsSanitizedFailureDiagnostic(t *testing.T) {
	bitsPath := t.TempDir()
	run := &recordingRunner{
		outputs: [][]byte{[]byte("App 'demo' not found"), []byte("stage failed\nAuthorization: Bearer secret-value")},
		errors:  []error{errors.New("exit status 1"), errors.New("exit status 1")},
	}
	operation, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)
	if err == nil {
		t.Fatal("Stage succeeded")
	}
	if !strings.Contains(operation.Summary, "stage failed") {
		t.Fatalf("Summary = %q", operation.Summary)
	}
	if strings.Contains(operation.Summary, "secret-value") || !strings.Contains(operation.Summary, "[REDACTED]") {
		t.Fatalf("Summary does not preserve redaction: %q", operation.Summary)
	}
	if len(operation.Summary) > maxOperationSummaryBytes {
		t.Fatalf("Summary length = %d, want <= %d", len(operation.Summary), maxOperationSummaryBytes)
	}
}

func TestRouteOperationsUseExactArgv(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{nil, nil, nil, nil, []byte(appGUID)}}
	provider := Provider{Run: run}
	request := RouteRequest{AppName: "demo", AppGUID: appGUID, Domain: "apps.identity", Host: "demo", SourceAppGUID: managerGUID}

	if _, err := provider.SecureRoute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RemoveRoute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	want := []command{
		{name: "cf", args: []string{"create-route", "apps.identity", "--hostname", "demo"}},
		{name: "cf", args: []string{"map-route", "demo", "apps.identity", "--hostname", "demo"}},
		{name: "cf", args: []string{"add-route-policy", "apps.identity", "--hostname", "demo", "--source", "cf:app:" + managerGUID}},
		{name: "cf", args: []string{"remove-route-policy", "apps.identity", "--hostname", "demo", "--source", "cf:app:" + managerGUID}},
		{name: "cf", args: []string{"app", "demo", "--guid"}},
		{name: "cf", args: []string{"unmap-route", "demo", "apps.identity", "--hostname", "demo"}},
		{name: "cf", args: []string{"delete-route", "apps.identity", "--hostname", "demo", "-f"}},
	}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want %#v", run.commands, want)
	}
}

func TestGenericRoutePolicySupportsSandboxToManagerEnrollment(t *testing.T) {
	run := &recordingRunner{}
	provider := Provider{Run: run}
	request := RoutePolicyRequest{Domain: "apps.identity", Host: "manager", SourceAppGUID: appGUID}

	if _, err := provider.AddRoutePolicy(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RemoveRoutePolicy(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	want := []command{
		{name: "cf", args: []string{"add-route-policy", "apps.identity", "--hostname", "manager", "--source", "cf:app:" + appGUID}},
		{name: "cf", args: []string{"remove-route-policy", "apps.identity", "--hostname", "manager", "--source", "cf:app:" + appGUID}},
	}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want %#v", run.commands, want)
	}
}

func TestDeleteAppUsesExactArgv(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte(appGUID), nil}}
	operation, err := (Provider{Run: run}).DeleteApp(context.Background(), "demo", appGUID)
	if err != nil {
		t.Fatal(err)
	}
	want := []command{{name: "cf", args: []string{"app", "demo", "--guid"}}, {name: "cf", args: []string{"delete", "demo", "-f"}}}
	if !reflect.DeepEqual(run.commands, want) || !operation.Success {
		t.Fatalf("DeleteApp commands/operation = (%#v, %#v)", run.commands, operation)
	}
}

func TestDeleteAppDoesNotDeleteReplacement(t *testing.T) {
	replacement := "123e4567-e89b-12d3-a456-426614174099"
	run := &recordingRunner{outputs: [][]byte{[]byte(replacement)}}
	operation, err := (Provider{Run: run}).DeleteApp(context.Background(), "demo", appGUID)
	var providerErr *Error
	if !errors.As(err, &providerErr) || !providerErr.IdentityMismatch() || operation.Success {
		t.Fatalf("DeleteApp() = (%#v, %v), want identity mismatch", operation, err)
	}
	want := []command{{name: "cf", args: []string{"app", "demo", "--guid"}}}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want no replacement delete", run.commands)
	}
}

func TestDeleteAppTreatsOriginalAbsenceAsSuccess(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte("App 'demo' not found")}, errors: []error{errors.New("exit 1")}}
	operation, err := (Provider{Run: run}).DeleteApp(context.Background(), "demo", appGUID)
	if err != nil || !operation.Success || len(run.commands) != 1 {
		t.Fatalf("DeleteApp() = (%#v, %v), commands %#v", operation, err, run.commands)
	}
}

func TestRemoveRouteNeverUnmapsReplacement(t *testing.T) {
	replacement := "123e4567-e89b-12d3-a456-426614174099"
	run := &recordingRunner{outputs: [][]byte{nil, []byte(replacement), nil}}
	operation, err := (Provider{Run: run}).RemoveRoute(context.Background(), RouteRequest{AppName: "demo", AppGUID: appGUID, Domain: "apps.identity", Host: "demo", SourceAppGUID: managerGUID})
	var providerErr *Error
	if !errors.As(err, &providerErr) || !providerErr.IdentityMismatch() || operation.Success {
		t.Fatalf("RemoveRoute() = (%#v, %v), want identity mismatch", operation, err)
	}
	want := []command{
		{name: "cf", args: []string{"remove-route-policy", "apps.identity", "--hostname", "demo", "--source", "cf:app:" + managerGUID}},
		{name: "cf", args: []string{"app", "demo", "--guid"}},
		{name: "cf", args: []string{"delete-route", "apps.identity", "--hostname", "demo", "-f"}},
	}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want replacement-safe cleanup", run.commands)
	}
}

func TestRemoveRouteTreatsAbsentOriginalAsSuccessfulCleanup(t *testing.T) {
	run := &recordingRunner{
		outputs: [][]byte{nil, []byte("App 'demo' not found"), nil},
		errors:  []error{nil, errors.New("exit 1"), nil},
	}
	operation, err := (Provider{Run: run}).RemoveRoute(context.Background(), RouteRequest{AppName: "demo", AppGUID: appGUID, Domain: "apps.identity", Host: "demo", SourceAppGUID: managerGUID})
	if err != nil || !operation.Success {
		t.Fatalf("RemoveRoute() = (%#v, %v), want successful absent-app cleanup", operation, err)
	}
	for _, invoked := range run.commands {
		if len(invoked.args) > 0 && invoked.args[0] == "unmap-route" {
			t.Fatalf("absent app was unmapped: %#v", run.commands)
		}
	}
}

func TestValidationHappensBeforeRunnerCalls(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		run      func(Provider) error
	}{
		{name: "malicious app", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, err := p.Stage(context.Background(), PushRequest{Name: "demo;rm-rf", Buildpack: "ruby_buildpack", BitsPath: "/tmp/work/demo"})
			return err
		}},
		{name: "unknown buildpack", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, err := p.Stage(context.Background(), PushRequest{Name: "demo", Buildpack: "evil", BitsPath: "/tmp/work/demo"})
			return err
		}},
		{name: "relative bits", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, err := p.Stage(context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: "demo"})
			return err
		}},
		{name: "escaped bits", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, err := p.Stage(context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: "/tmp/elsewhere/demo"})
			return err
		}},
		{name: "invalid domain", provider: Provider{}, run: func(p Provider) error {
			_, err := p.AddRoutePolicy(context.Background(), RoutePolicyRequest{Domain: "apps.identity;bad", Host: "demo", SourceAppGUID: appGUID})
			return err
		}},
		{name: "invalid guid", provider: Provider{}, run: func(p Provider) error {
			_, err := p.AddRoutePolicy(context.Background(), RoutePolicyRequest{Domain: "apps.identity", Host: "demo", SourceAppGUID: "not-a-guid"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingRunner{}
			test.provider.Run = recorder
			if err := test.run(test.provider); err == nil {
				t.Fatal("operation succeeded, want validation error")
			}
			if len(recorder.commands) != 0 {
				t.Fatalf("validation ran commands: %#v", recorder.commands)
			}
		})
	}
}

func TestPushRejectsBitsPathEscapingThroughSymlink(t *testing.T) {
	workRoot := t.TempDir()
	link := filepath.Join(workRoot, "demo")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	run := &recordingRunner{}
	_, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: workRoot}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: link},
	)
	if err == nil {
		t.Fatal("Push succeeded through symlink outside work root")
	}
	if len(run.commands) != 0 {
		t.Fatalf("Push ran commands before rejecting symlink: %#v", run.commands)
	}
}

func TestPushRejectsMissingBitsDirectory(t *testing.T) {
	workRoot := t.TempDir()
	run := &recordingRunner{}
	_, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: workRoot}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: filepath.Join(workRoot, "missing")},
	)
	if err == nil || !strings.Contains(err.Error(), "bits path") {
		t.Fatalf("Push error = %v, want missing bits path error", err)
	}
	if len(run.commands) != 0 {
		t.Fatalf("Push ran commands for missing bits: %#v", run.commands)
	}
}

func TestPushRejectsBitsPathThatIsNotDirectory(t *testing.T) {
	workRoot := t.TempDir()
	bitsPath := filepath.Join(workRoot, "archive.zip")
	if err := os.WriteFile(bitsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := &recordingRunner{}
	_, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: workRoot}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("Push error = %v, want bits directory error", err)
	}
	if len(run.commands) != 0 {
		t.Fatalf("Push ran commands for non-directory bits: %#v", run.commands)
	}
}

func TestPushRejectsMissingBitsUnderEscapingSymlinkAncestor(t *testing.T) {
	workRoot := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workRoot, "redirect")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	run := &recordingRunner{}
	_, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: workRoot}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: filepath.Join(link, "missing")},
	)
	if err == nil || !strings.Contains(err.Error(), "work root") {
		t.Fatalf("Push error = %v, want physical confinement error", err)
	}
	if len(run.commands) != 0 {
		t.Fatalf("Push ran commands through escaping ancestor: %#v", run.commands)
	}
}

func TestInspectAppUsesCAPIStatsEnvelopeAndExplicitReadiness(t *testing.T) {
	processGUID := "123e4567-e89b-12d3-a456-426614174002"
	tests := []struct {
		name        string
		stats       string
		wantRunning bool
		wantReady   bool
	}{
		{name: "all desired running and routable", stats: `{"resources":[{"type":"web","index":0,"state":"RUNNING","routable":true},{"type":"web","index":1,"state":"RUNNING","routable":true}]}`, wantRunning: true, wantReady: true},
		{name: "missing desired instance", stats: `{"resources":[{"type":"web","index":0,"state":"RUNNING","routable":true}]}`, wantRunning: true, wantReady: false},
		{name: "running but not routable", stats: `{"resources":[{"type":"web","index":0,"state":"RUNNING","routable":true},{"type":"web","index":1,"state":"RUNNING","routable":false}]}`, wantRunning: true, wantReady: false},
		{name: "starting", stats: `{"resources":[{"type":"web","index":0,"state":"STARTING","routable":false}]}`},
		{name: "empty resources", stats: `{"resources":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := &recordingRunner{outputs: [][]byte{
				[]byte(`{"resources":[{"guid":"` + processGUID + `","type":"web","instances":2}]}`),
				[]byte(test.stats),
			}}
			app, err := (Provider{Run: run}).InspectApp(context.Background(), appGUID)
			if err != nil {
				t.Fatal(err)
			}
			if app.Running != test.wantRunning || app.Ready != test.wantReady {
				t.Fatalf("InspectApp = %#v, want Running=%v Ready=%v", app, test.wantRunning, test.wantReady)
			}
			want := []command{
				{name: "cf", args: []string{"curl", "/v3/apps/" + appGUID + "/processes"}},
				{name: "cf", args: []string{"curl", "/v3/processes/" + processGUID + "/stats"}},
			}
			if !reflect.DeepEqual(run.commands, want) {
				t.Fatalf("commands = %#v, want %#v", run.commands, want)
			}
		})
	}
}

func TestInspectAppIgnoresFakeProcessStateAndNonWebProcesses(t *testing.T) {
	webGUID := "123e4567-e89b-12d3-a456-426614174002"
	workerGUID := "123e4567-e89b-12d3-a456-426614174003"
	run := &recordingRunner{outputs: [][]byte{
		[]byte(`{"resources":[{"guid":"` + webGUID + `","type":"web","instances":1,"state":"STOPPED"},{"guid":"` + workerGUID + `","type":"worker","instances":1,"state":"STARTED"}]}`),
		[]byte(`{"resources":[{"type":"web","index":0,"state":"RUNNING","routable":true}]}`),
	}}
	app, err := (Provider{Run: run}).InspectApp(context.Background(), appGUID)
	if err != nil {
		t.Fatal(err)
	}
	if !app.Running || !app.Ready || app.State != "" {
		t.Fatalf("InspectApp = %#v, want web stats only and no inferred process state", app)
	}
	if len(run.commands) != 2 {
		t.Fatalf("commands = %#v, want no worker stats lookup", run.commands)
	}
}

func TestInspectAppRejectsMalformedStatsEnvelope(t *testing.T) {
	processGUID := "123e4567-e89b-12d3-a456-426614174002"
	run := &recordingRunner{outputs: [][]byte{
		[]byte(`{"resources":[{"guid":"` + processGUID + `","type":"web","instances":1}]}`),
		[]byte(`{"resources":[{"state":`),
	}}
	_, err := (Provider{Run: run}).InspectApp(context.Background(), appGUID)
	if err == nil || !strings.Contains(err.Error(), "process stats JSON") {
		t.Fatalf("InspectApp error = %v, want explicit stats JSON error", err)
	}
}

func TestInspectAppRejectsMalformedJSON(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte(`{"resources":`)}}
	_, err := (Provider{Run: run}).InspectApp(context.Background(), appGUID)
	if err == nil || !strings.Contains(err.Error(), "processes JSON") {
		t.Fatalf("InspectApp error = %v, want explicit processes JSON error", err)
	}
}

func TestOperationErrorsAreBoundedAndDoNotContainOutput(t *testing.T) {
	secret := "SUPER-SECRET-TOKEN"
	run := &recordingRunner{outputs: [][]byte{[]byte(secret + strings.Repeat("x", 100000))}, errors: []error{errors.New("exit status 1")}}
	operation, _, err := (Provider{Run: run}).execute(context.Background(), "delete-app", "delete", "demo", "-f")
	if err == nil {
		t.Fatal("DeleteApp succeeded")
	}
	if strings.Contains(operation.Error, secret) || strings.Contains(err.Error(), secret) || len(operation.Error) > 1024 {
		t.Fatalf("unsafe operation error length/content: %q", operation.Error)
	}
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Operation != "delete-app" {
		t.Fatalf("DeleteApp error = %T %v, want typed provider error", err, err)
	}
}

func TestOperationRetainsSanitizedOutputOnSuccessAndFailure(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		wantErr bool
	}{
		{name: "success"},
		{name: "failure", runErr: errors.New("exit status 1"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := &recordingRunner{
				outputs: [][]byte{[]byte("upload complete\nAuthorization: Bearer bearer-value\nJOIN_TOKEN=join-value\nCF_INSTANCE_KEY=key-value\nCF_INSTANCE_CERT=cert-value\n-----BEGIN CERTIFICATE-----\ncertificate-body\n-----END CERTIFICATE-----\n\x1b[31mstaging\x1b[0m\x00done\n")},
				errors:  []error{test.runErr},
			}
			operation, _, err := (Provider{Run: run}).execute(context.Background(), "delete-app", "delete", "demo", "-f")
			if (err != nil) != test.wantErr {
				t.Fatalf("DeleteApp error = %v, wantErr %v", err, test.wantErr)
			}
			if !strings.Contains(operation.Summary, "upload complete") || !strings.Contains(operation.Summary, "staging done") {
				t.Fatalf("Summary = %q, want retained friction output", operation.Summary)
			}
			for _, secret := range []string{"bearer-value", "join-value", "key-value", "cert-value", "certificate-body"} {
				if strings.Contains(operation.Summary, secret) {
					t.Fatalf("Summary contains secret %q: %q", secret, operation.Summary)
				}
			}
			if !strings.Contains(operation.Summary, "[REDACTED]") || strings.Contains(operation.Summary, "\x1b") || strings.ContainsRune(operation.Summary, '\x00') {
				t.Fatalf("Summary is not sanitized: %q", operation.Summary)
			}
		})
	}
}

func TestMultiCommandOperationCombinesSanitizedOutput(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{
		[]byte("route created"),
		[]byte("route mapped"),
		[]byte("policy added"),
	}}
	operation, err := (Provider{Run: run}).SecureRoute(context.Background(), RouteRequest{
		AppName: "demo", AppGUID: appGUID, Domain: "apps.identity", Host: "demo", SourceAppGUID: managerGUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []string{"route created", "route mapped", "policy added"} {
		if !strings.Contains(operation.Summary, evidence) {
			t.Fatalf("Summary = %q, want %q", operation.Summary, evidence)
		}
	}
}

func TestOperationSummaryPrefersNewestFailureDiagnostic(t *testing.T) {
	run := &recordingRunner{
		outputs: [][]byte{
			[]byte("old:" + strings.Repeat("x", maxOperationSummaryBytes)),
			[]byte("final policy failure"),
		},
		errors: []error{nil, errors.New("exit status 1")},
	}
	operation, err := (Provider{Run: run}).SecureRoute(context.Background(), RouteRequest{
		AppName: "demo", AppGUID: appGUID, Domain: "apps.identity", Host: "demo", SourceAppGUID: managerGUID,
	})
	if err == nil {
		t.Fatal("SecureRoute succeeded, want mapping failure")
	}
	if !strings.Contains(operation.Summary, "[older output truncated]") || !strings.Contains(operation.Summary, "final policy failure") {
		t.Fatalf("Summary = %q, want truncation marker and newest failure", operation.Summary)
	}
	if len(operation.Summary) > maxOperationSummaryBytes {
		t.Fatalf("Summary length = %d, want <= %d", len(operation.Summary), maxOperationSummaryBytes)
	}
}

func TestOutputRedactionHandlesStructuredAndEmbeddedKnownSecrets(t *testing.T) {
	output := `useful status
export JOIN_TOKEN=export-secret
{"client_secret":"json-secret","status":"useful-json"}
request Authorization: Bearer embedded-bearer sent
token response Bearer standalone-bearer
credentials: password=embedded-password user=demo
CF_INSTANCE_CERT="-----BEGIN CERTIFICATE-----
certificate-body
-----END CERTIFICATE-----"
useful tail`
	run := &recordingRunner{outputs: [][]byte{[]byte(output)}}
	operation, _, err := (Provider{Run: run}).execute(context.Background(), "delete-app", "delete", "demo", "-f")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"export-secret", "json-secret", "embedded-bearer", "standalone-bearer", "embedded-password", "certificate-body"} {
		if strings.Contains(operation.Summary, secret) {
			t.Fatalf("Summary contains %q: %q", secret, operation.Summary)
		}
	}
	for _, useful := range []string{"useful status", "useful-json", "user=demo", "useful tail"} {
		if !strings.Contains(operation.Summary, useful) {
			t.Fatalf("Summary dropped %q: %q", useful, operation.Summary)
		}
	}
}

func TestOutputRedactionRemovesCompleteQuotedAndEscapedValues(t *testing.T) {
	output := `useful start
export PASSWORD='shell multi word suffix leak'
PRIVATE_KEY="quoted private key suffix leak"
{"client_secret":"json multi word suffix leak","private_key":"-----BEGIN PRIVATE KEY-----\njson-key-body\n-----END PRIVATE KEY-----","message":"useful-json"}
payload "message":"-----BEGIN RSA PRIVATE KEY-----\nescaped-key-body\n-----END RSA PRIVATE KEY-----"
Authorization: Bearer header-secret
token and secret are harmless words in prose
useful end`
	run := &recordingRunner{outputs: [][]byte{[]byte(output)}}
	operation, _, err := (Provider{Run: run}).execute(context.Background(), "delete-app", "delete", "demo", "-f")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"shell multi word", "suffix leak", "quoted private key", "json multi word", "json-key-body", "escaped-key-body", "header-secret"} {
		if strings.Contains(operation.Summary, secret) {
			t.Fatalf("Summary contains %q: %q", secret, operation.Summary)
		}
	}
	for _, useful := range []string{"useful start", "useful-json", "token and secret are harmless words in prose", "useful end"} {
		if !strings.Contains(operation.Summary, useful) {
			t.Fatalf("Summary dropped %q: %q", useful, operation.Summary)
		}
	}
}

func TestOversizedSingleCommandSummaryPreservesNewestDiagnostic(t *testing.T) {
	final := "FINAL FAILURE: staging rejected"
	run := &recordingRunner{outputs: [][]byte{[]byte(strings.Repeat("old output ", 1000) + final)}}
	operation, _, err := (Provider{Run: run}).execute(context.Background(), "delete-app", "delete", "demo", "-f")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(operation.Summary, "[older output truncated]\n") || !strings.Contains(operation.Summary, final) {
		t.Fatalf("Summary does not preserve newest diagnostic: %q", operation.Summary)
	}
	if len(operation.Summary) > maxOperationSummaryBytes {
		t.Fatalf("Summary length = %d, want <= %d", len(operation.Summary), maxOperationSummaryBytes)
	}
}

func TestOperationSummaryHasSmallExplicitCap(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte(strings.Repeat("x", 100000))}}
	operation, _, err := (Provider{Run: run}).execute(context.Background(), "delete-app", "delete", "demo", "-f")
	if err != nil {
		t.Fatal(err)
	}
	if len(operation.Summary) != maxOperationSummaryBytes {
		t.Fatalf("Summary length = %d, want %d", len(operation.Summary), maxOperationSummaryBytes)
	}
	if !strings.HasPrefix(operation.Summary, "[older output truncated]\n") {
		t.Fatalf("Summary = %q, want truncation marker", operation.Summary)
	}
}

func TestCommandDisplayIsBoundedAndSanitized(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{[]byte("App 'demo' not found"), nil}, errors: []error{errors.New("exit status 1"), nil}}
	bitsPath := t.TempDir()
	for i := 0; i < 6; i++ {
		bitsPath = filepath.Join(bitsPath, strings.Repeat("x", 200)+"\nforged")
		if err := os.Mkdir(bitsPath, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	operation, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}}).Stage(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(operation.Command) > 1024 || strings.ContainsAny(operation.Command, "\r\n") {
		t.Fatalf("unsafe command display length/content: %q", operation.Command)
	}
	if run.commands[1].args[7] != bitsPath {
		t.Fatalf("bits argv = %q, want original discrete value", run.commands[1].args[7])
	}
}

var _ CloudFoundry = Provider{}

var _ runner.Runner = (*recordingRunner)(nil)
