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

func TestPushUsesExactArgvAndDiscoversGUID(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{nil, []byte(appGUID + "\n")}}
	provider := Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}

	app, operation, err := provider.Push(context.Background(), PushRequest{
		Name: "demo", Buildpack: "ruby_buildpack", BitsPath: "/tmp/work/demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []command{
		{name: "cf", args: []string{"push", "demo", "--no-route", "-b", "ruby_buildpack", "-p", "/tmp/work/demo", "-c", "./.sandbox/start.sh"}},
		{name: "cf", args: []string{"app", "demo", "--guid"}},
	}
	if !reflect.DeepEqual(run.commands, want) {
		t.Fatalf("commands = %#v, want %#v", run.commands, want)
	}
	if app.Name != "demo" || app.GUID != appGUID || !operation.Success || operation.Name == "" {
		t.Fatalf("Push() = (%#v, %#v), want discovered app and successful operation", app, operation)
	}
}

func TestRouteOperationsUseExactArgv(t *testing.T) {
	run := &recordingRunner{}
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
	run := &recordingRunner{}
	operation, err := (Provider{Run: run}).DeleteApp(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	want := []command{{name: "cf", args: []string{"delete", "demo", "-f"}}}
	if !reflect.DeepEqual(run.commands, want) || !operation.Success {
		t.Fatalf("DeleteApp commands/operation = (%#v, %#v)", run.commands, operation)
	}
}

func TestValidationHappensBeforeRunnerCalls(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		run      func(Provider) error
	}{
		{name: "malicious app", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, _, err := p.Push(context.Background(), PushRequest{Name: "demo;rm-rf", Buildpack: "ruby_buildpack", BitsPath: "/tmp/work/demo"})
			return err
		}},
		{name: "unknown buildpack", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, _, err := p.Push(context.Background(), PushRequest{Name: "demo", Buildpack: "evil", BitsPath: "/tmp/work/demo"})
			return err
		}},
		{name: "relative bits", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, _, err := p.Push(context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: "demo"})
			return err
		}},
		{name: "escaped bits", provider: Provider{Buildpacks: []string{"ruby_buildpack"}, WorkRoot: "/tmp/work"}, run: func(p Provider) error {
			_, _, err := p.Push(context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: "/tmp/elsewhere/demo"})
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
	_, _, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: workRoot}).Push(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: link},
	)
	if err == nil {
		t.Fatal("Push succeeded through symlink outside work root")
	}
	if len(run.commands) != 0 {
		t.Fatalf("Push ran commands before rejecting symlink: %#v", run.commands)
	}
}

func TestInspectAppUsesTypedV3JSON(t *testing.T) {
	processGUID := "123e4567-e89b-12d3-a456-426614174002"
	run := &recordingRunner{outputs: [][]byte{
		[]byte(`{"resources":[{"guid":"` + processGUID + `","type":"web","state":"STARTED"}]}`),
		[]byte(`{"resources":[{"state":"RUNNING"}]}`),
	}}

	app, err := (Provider{Run: run}).InspectApp(context.Background(), appGUID)
	if err != nil {
		t.Fatal(err)
	}
	want := []command{
		{name: "cf", args: []string{"curl", "/v3/apps/" + appGUID + "/processes"}},
		{name: "cf", args: []string{"curl", "/v3/processes/" + processGUID + "/stats"}},
	}
	if !reflect.DeepEqual(run.commands, want) || !app.Running || !app.Ready {
		t.Fatalf("InspectApp = (%#v, %#v), commands %#v", app, err, run.commands)
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
	operation, err := (Provider{Run: run}).DeleteApp(context.Background(), "demo")
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

func TestCommandDisplayIsBoundedAndSanitized(t *testing.T) {
	run := &recordingRunner{outputs: [][]byte{nil, []byte(appGUID)}}
	bitsPath := "/tmp/" + strings.Repeat("x", 2000) + "\nforged"
	_, operation, err := (Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}}).Push(
		context.Background(), PushRequest{Name: "demo", Buildpack: "ruby_buildpack", BitsPath: bitsPath},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(operation.Command) > 1024 || strings.ContainsAny(operation.Command, "\r\n") {
		t.Fatalf("unsafe command display length/content: %q", operation.Command)
	}
	if run.commands[0].args[6] != bitsPath {
		t.Fatalf("bits argv = %q, want original discrete value", run.commands[0].args[6])
	}
}

var _ CloudFoundry = Provider{}

var _ runner.Runner = (*recordingRunner)(nil)
