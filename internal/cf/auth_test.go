package cf

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type authCall struct {
	name string
	args []string
	env  []string
}

type authRunner struct {
	calls []authCall
	err   error
}

func (r *authRunner) Run(context.Context, string, ...string) ([]byte, error) {
	panic("authentication must use RunEnv")
}

func (r *authRunner) RunEnv(_ context.Context, environment []string, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, authCall{name: name, args: append([]string(nil), args...), env: append([]string(nil), environment...)})
	return nil, r.err
}

func TestAuthenticatorUsesIsolatedCFHomeAndExactCommands(t *testing.T) {
	runner := &authRunner{}
	home := filepath.Join(t.TempDir(), "cf-home")
	authenticator := Authenticator{Run: runner, Executable: "/opt/cf", API: "https://api.example", Username: "manager", Password: "in-memory-only", CFHome: home, SkipSSLValidation: true}

	if err := authenticator.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []authCall{
		{name: "/opt/cf", args: []string{"api", "https://api.example", "--skip-ssl-validation"}, env: []string{"CF_HOME=" + home}},
		{name: "/opt/cf", args: []string{"auth", "manager", "in-memory-only"}, env: []string{"CF_HOME=" + home}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestAuthenticatorReturnsGenericErrorWithoutSecret(t *testing.T) {
	secret := "in-memory-only"
	runner := &authRunner{err: os.ErrPermission}
	authenticator := Authenticator{Run: runner, Executable: "/opt/cf", API: "https://api.example", Username: "manager", Password: secret, CFHome: t.TempDir()}

	err := authenticator.Authenticate(context.Background())
	if err == nil || err.Error() != "CF CLI authentication failed" || strings.Contains(err.Error(), secret) {
		t.Fatalf("error = %v, want generic secret-free error", err)
	}
}
