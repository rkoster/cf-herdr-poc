package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresIdentityDomain(t *testing.T) {
	_, err := Load(env(map[string]string{
		"SANDBOX_BUILDPACKS": "ruby_buildpack",
	}))
	if err == nil || !strings.Contains(err.Error(), "CF_IDENTITY_DOMAIN") {
		t.Fatalf("Load() error = %v, want error naming CF_IDENTITY_DOMAIN", err)
	}
}

func TestLoadDefaults(t *testing.T) {
	got, err := Load(env(map[string]string{
		"CF_IDENTITY_DOMAIN": " apps.identity ",
		"SANDBOX_BUILDPACKS": " ruby_buildpack ",
	}))
	if err != nil {
		t.Fatal(err)
	}

	want := Config{
		Address:           ":8080",
		StatePath:         "./data/sandboxes.json",
		WebDir:            "./web/dist",
		CollieDir:         "./collie",
		IdentityDomain:    "apps.identity",
		Buildpacks:        []string{"ruby_buildpack"},
		ReconcileInterval: 2 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestLoadBuildpacksTrimsAndSplits(t *testing.T) {
	got, err := Load(env(map[string]string{
		"CF_IDENTITY_DOMAIN": "apps.identity",
		"SANDBOX_BUILDPACKS": " ruby_buildpack, nodejs_buildpack ",
	}))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"ruby_buildpack", "nodejs_buildpack"}
	if !reflect.DeepEqual(got.Buildpacks, want) {
		t.Fatalf("Buildpacks = %#v, want %#v", got.Buildpacks, want)
	}
}

func TestLoadRejectsEmptyBuildpackList(t *testing.T) {
	for _, value := range []string{"", "  ", " , "} {
		t.Run(value, func(t *testing.T) {
			_, err := Load(env(map[string]string{
				"CF_IDENTITY_DOMAIN": "apps.identity",
				"SANDBOX_BUILDPACKS": value,
			}))
			if err == nil || !strings.Contains(err.Error(), "SANDBOX_BUILDPACKS") {
				t.Fatalf("Load() error = %v, want error naming SANDBOX_BUILDPACKS", err)
			}
		})
	}
}

func TestLoadAddressPrecedence(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "port", env: map[string]string{"PORT": "9090"}, want: ":9090"},
		{name: "manager address", env: map[string]string{"PORT": "9090", "MANAGER_ADDRESS": " 127.0.0.1:7070 "}, want: "127.0.0.1:7070"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env["CF_IDENTITY_DOMAIN"] = "apps.identity"
			tt.env["SANDBOX_BUILDPACKS"] = "ruby_buildpack"
			got, err := Load(env(tt.env))
			if err != nil {
				t.Fatal(err)
			}
			if got.Address != tt.want {
				t.Fatalf("Address = %q, want %q", got.Address, tt.want)
			}
		})
	}
}

func TestLoadOverrides(t *testing.T) {
	got, err := Load(env(map[string]string{
		"CF_IDENTITY_DOMAIN":         " apps.identity ",
		"SANDBOX_BUILDPACKS":         " ruby_buildpack ",
		"MANAGER_STATE_PATH":         " /tmp/state.json ",
		"MANAGER_WEB_DIR":            " /tmp/web ",
		"MANAGER_COLLIE_DIR":         " /tmp/collie ",
		"MANAGER_APP_NAME":           " manager ",
		"MANAGER_APP_GUID":           " app-guid ",
		"MANAGER_PACK_HOST":          " pack.apps.identity ",
		"MANAGER_RECONCILE_INTERVAL": " 5s ",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if got.StatePath != "/tmp/state.json" || got.WebDir != "/tmp/web" || got.CollieDir != "/tmp/collie" ||
		got.ManagerAppName != "manager" || got.ManagerAppGUID != "app-guid" || got.ManagerPackHost != "pack.apps.identity" ||
		got.ReconcileInterval != 5*time.Second {
		t.Fatalf("Load() overrides = %#v", got)
	}
}

func TestLoadRejectsMalformedReconcileInterval(t *testing.T) {
	_, err := Load(env(map[string]string{
		"CF_IDENTITY_DOMAIN":         "apps.identity",
		"SANDBOX_BUILDPACKS":         "ruby_buildpack",
		"MANAGER_RECONCILE_INTERVAL": "eventually",
	}))
	if err == nil || !strings.Contains(err.Error(), "MANAGER_RECONCILE_INTERVAL") {
		t.Fatalf("Load() error = %v, want error naming MANAGER_RECONCILE_INTERVAL", err)
	}
}

func TestLoadRejectsNonpositiveReconcileInterval(t *testing.T) {
	for _, value := range []string{"0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			_, err := Load(env(map[string]string{
				"CF_IDENTITY_DOMAIN":         "apps.identity",
				"SANDBOX_BUILDPACKS":         "ruby_buildpack",
				"MANAGER_RECONCILE_INTERVAL": value,
			}))
			if err == nil || !strings.Contains(err.Error(), "MANAGER_RECONCILE_INTERVAL") {
				t.Fatalf("Load() error = %v, want error naming MANAGER_RECONCILE_INTERVAL", err)
			}
		})
	}
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
