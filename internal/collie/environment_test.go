package collie

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentOverridesAmbientRuntimePaths(t *testing.T) {
	base := []string{
		"PATH=/bin", "HERDR_PLUGIN_CONFIG_DIR=/ambient/config", "HERDR_PLUGIN_STATE_DIR=/ambient/state",
		"COLLIE_STATE_DIR=/ambient/collie", "COLLIE_PLUGIN_ROOT=/ambient/root", "HERDR_SOCKET_PATH=/ambient/socket", "COLLIE_HOST=0.0.0.0", "COLLIE_PORT=9999", "COLLIE_PACK_TRANSPORT=pinned",
	}
	env := Environment(Runtime{PluginRoot: "/manager/collie", ConfigDir: "/manager/config", StateDir: "/manager/state", SocketPath: "/manager/herdr.sock", Host: "127.0.0.2", Port: 8787, PackTransport: "cf-identity"}, base)
	want := map[string]string{
		"PATH": "/bin", "HERDR_PLUGIN_CONFIG_DIR": "/manager/config", "HERDR_PLUGIN_STATE_DIR": "/manager/state",
		"COLLIE_STATE_DIR": "/manager/state", "COLLIE_PLUGIN_ROOT": "/manager/collie", "HERDR_SOCKET_PATH": "/manager/herdr.sock", "COLLIE_HOST": "127.0.0.2", "COLLIE_PORT": "8787", "COLLIE_PACK_TRANSPORT": "cf-identity",
	}
	if got := envMap(env); !reflect.DeepEqual(got, want) {
		t.Fatalf("Environment() = %#v, want %#v", got, want)
	}
	for _, key := range []string{"HERDR_PLUGIN_CONFIG_DIR", "HERDR_PLUGIN_STATE_DIR", "COLLIE_STATE_DIR", "COLLIE_PLUGIN_ROOT", "HERDR_SOCKET_PATH", "COLLIE_HOST", "COLLIE_PORT", "COLLIE_PACK_TRANSPORT"} {
		count := 0
		for _, entry := range env {
			if strings.HasPrefix(entry, key+"=") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s appears %d times in %v", key, count, env)
		}
	}
}

func TestEnvironmentRemovesAmbientPluginRootWhenUnconfigured(t *testing.T) {
	env := Environment(Runtime{ConfigDir: "/config", StateDir: "/state", Port: 8787}, []string{"COLLIE_PLUGIN_ROOT=/ambient/root"})
	if _, ok := envMap(env)["COLLIE_PLUGIN_ROOT"]; ok {
		t.Fatalf("ambient plugin root retained: %v", env)
	}
}

func TestEnvironmentRemovesAmbientPackTransportWhenUnconfigured(t *testing.T) {
	env := Environment(Runtime{ConfigDir: "/config", StateDir: "/state", Port: 8787}, []string{"COLLIE_PACK_TRANSPORT=cf-identity"})
	if _, ok := envMap(env)["COLLIE_PACK_TRANSPORT"]; ok {
		t.Fatalf("ambient Pack transport retained: %v", env)
	}
}

func TestEnvironmentRemovesAmbientSocketWhenUnconfigured(t *testing.T) {
	env := Environment(Runtime{ConfigDir: "/config", StateDir: "/state", Port: 8787}, []string{"HERDR_SOCKET_PATH=/ambient"})
	if _, ok := envMap(env)["HERDR_SOCKET_PATH"]; ok {
		t.Fatalf("ambient socket retained: %v", env)
	}
}

func envMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		result[key] = value
	}
	return result
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
