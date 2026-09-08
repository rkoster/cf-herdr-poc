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
		CollieAddress:     "127.0.0.1:9191",
		WorkRoot:          "./data/work",
		RuntimeDir:        "./manager-runtime",
		SandboxRuntimeDir: "./sandbox/runtime",
		BunExecutable:     "./manager-runtime/bin/bun",
		CollieExecutable:  "./manager-runtime/bin/collie",
		HerdrExecutable:   "./manager-runtime/bin/herdr",
		CFExecutable:      "./manager-runtime/bin/cf",
		InstanceCert:      "/etc/cf-instance-credentials/instance.crt",
		InstanceKey:       "/etc/cf-instance-credentials/instance.key",
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
		"CF_IDENTITY_DOMAIN":          " apps.identity ",
		"SANDBOX_BUILDPACKS":          " ruby_buildpack ",
		"MANAGER_STATE_PATH":          " /tmp/state.json ",
		"MANAGER_WEB_DIR":             " /tmp/web ",
		"MANAGER_COLLIE_DIR":          " /tmp/collie ",
		"MANAGER_APP_NAME":            " manager ",
		"MANAGER_APP_GUID":            " app-guid ",
		"MANAGER_PACK_HOST":           " pack.apps.identity ",
		"MANAGER_RECONCILE_INTERVAL":  " 5s ",
		"MANAGER_API_TOKEN":           " operator-secret ",
		"MANAGER_COLLIE_ADDRESS":      " 127.0.0.1:9191 ",
		"MANAGER_WORK_ROOT":           " /tmp/work ",
		"MANAGER_RUNTIME_DIR":         " /tmp/runtime ",
		"MANAGER_SANDBOX_RUNTIME_DIR": " /tmp/sandbox-runtime ",
		"MANAGER_BUN_EXECUTABLE":      " /tmp/bun ",
		"MANAGER_COLLIE_EXECUTABLE":   " /tmp/collie-bin ",
		"MANAGER_HERDR_EXECUTABLE":    " /tmp/herdr-bin ",
		"MANAGER_CF_EXECUTABLE":       " /tmp/cf ",
		"CF_INSTANCE_CERT":            " /tmp/cert ",
		"CF_INSTANCE_KEY":             " /tmp/key ",
		"CF_API":                      " https://api.example ",
		"CF_USERNAME":                 " manager ",
		"CF_PASSWORD":                 " in-memory-only ",
		"CF_ORG":                      " poc ",
		"CF_SPACE":                    " demo ",
		"CF_SKIP_SSL_VALIDATION":      " true ",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if got.StatePath != "/tmp/state.json" || got.WebDir != "/tmp/web" || got.CollieDir != "/tmp/collie" ||
		got.ManagerAppName != "manager" || got.ManagerAppGUID != "app-guid" || got.ManagerPackHost != "pack.apps.identity" || got.ManagerRouteHost != "pack" ||
		got.ReconcileInterval != 5*time.Second || got.APIToken != "operator-secret" || got.CollieAddress != "127.0.0.1:9191" ||
		got.WorkRoot != "/tmp/work" || got.RuntimeDir != "/tmp/runtime" || got.BunExecutable != "/tmp/bun" || got.CollieExecutable != "/tmp/collie-bin" || got.HerdrExecutable != "/tmp/herdr-bin" || got.CFExecutable != "/tmp/cf" || got.InstanceCert != "/tmp/cert" || got.InstanceKey != "/tmp/key" || got.CFAPI != "https://api.example" || got.CFUsername != "manager" || got.CFPassword != "in-memory-only" || got.CFOrg != "poc" || got.CFSpace != "demo" || !got.CFSkipSSLValidation {
		t.Fatalf("Load() overrides = %#v", got)
	}
}

func TestLoadRejectsInvalidCFSkipSSLValidation(t *testing.T) {
	_, err := Load(env(map[string]string{
		"CF_IDENTITY_DOMAIN":     "apps.identity",
		"SANDBOX_BUILDPACKS":     "ruby_buildpack",
		"CF_SKIP_SSL_VALIDATION": "sometimes",
	}))
	if err == nil || !strings.Contains(err.Error(), "CF_SKIP_SSL_VALIDATION") {
		t.Fatalf("Load() error = %v, want error naming CF_SKIP_SSL_VALIDATION", err)
	}
}

func TestLoadRejectsManagerPackHostOutsideIdentityDomain(t *testing.T) {
	for _, host := range []string{"pack", "pack.example.com", "nested.pack.apps.identity", "pack.apps.identity.apps.identity", "PACK.apps.identity", ".apps.identity"} {
		t.Run(host, func(t *testing.T) {
			_, err := Load(env(map[string]string{
				"CF_IDENTITY_DOMAIN": "apps.identity",
				"SANDBOX_BUILDPACKS": "ruby_buildpack",
				"MANAGER_PACK_HOST":  host,
			}))
			if err == nil || !strings.Contains(err.Error(), "MANAGER_PACK_HOST must be a direct child of CF_IDENTITY_DOMAIN") {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestLoadRequiresProductionAssemblySettings(t *testing.T) {
	base := Config{ManagerAppName: "manager", ManagerAppGUID: "guid", ManagerPackHost: "pack.apps.identity", APIToken: "secret", CFAPI: "https://api.example", CFUsername: "manager", CFPassword: "in-memory-only", CFOrg: "poc", CFSpace: "demo"}
	for _, tt := range []struct {
		name  string
		clear func(*Config)
	}{
		{"MANAGER_APP_NAME", func(c *Config) { c.ManagerAppName = "" }}, {"MANAGER_APP_GUID", func(c *Config) { c.ManagerAppGUID = "" }},
		{"MANAGER_PACK_HOST", func(c *Config) { c.ManagerPackHost = "" }}, {"MANAGER_API_TOKEN", func(c *Config) { c.APIToken = "" }},
		{"CF_API", func(c *Config) { c.CFAPI = "" }}, {"CF_USERNAME", func(c *Config) { c.CFUsername = "" }}, {"CF_PASSWORD", func(c *Config) { c.CFPassword = "" }}, {"CF_ORG", func(c *Config) { c.CFOrg = "" }}, {"CF_SPACE", func(c *Config) { c.CFSpace = "" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := base
			tt.clear(&value)
			if err := value.ValidateProduction(); err == nil || !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("error = %v", err)
			}
		})
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
