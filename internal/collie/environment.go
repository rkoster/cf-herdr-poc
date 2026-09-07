package collie

import (
	"strconv"
	"strings"
)

type Runtime struct {
	PluginRoot    string
	ConfigDir     string
	StateDir      string
	SocketPath    string
	Host          string
	Port          int
	PackTransport string
}

func Environment(runtime Runtime, base []string) []string {
	overrides := map[string]string{
		"HERDR_PLUGIN_CONFIG_DIR": runtime.ConfigDir,
		"HERDR_PLUGIN_STATE_DIR":  runtime.StateDir,
		"COLLIE_STATE_DIR":        runtime.StateDir,
		"COLLIE_HOST":             runtime.Host,
		"COLLIE_PORT":             strconv.Itoa(runtime.Port),
	}
	if runtime.PluginRoot != "" {
		overrides["COLLIE_PLUGIN_ROOT"] = runtime.PluginRoot
	}
	if runtime.PackTransport != "" {
		overrides["COLLIE_PACK_TRANSPORT"] = runtime.PackTransport
	}
	if runtime.SocketPath != "" {
		overrides["HERDR_SOCKET_PATH"] = runtime.SocketPath
	}
	managed := map[string]bool{
		"HERDR_PLUGIN_CONFIG_DIR": true, "HERDR_PLUGIN_STATE_DIR": true, "COLLIE_STATE_DIR": true,
		"COLLIE_PLUGIN_ROOT": true, "HERDR_SOCKET_PATH": true, "COLLIE_HOST": true, "COLLIE_PORT": true, "COLLIE_PACK_TRANSPORT": true,
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, found := strings.Cut(entry, "=")
		if found && managed[key] {
			continue
		}
		result = append(result, entry)
	}
	for _, key := range []string{"HERDR_PLUGIN_CONFIG_DIR", "HERDR_PLUGIN_STATE_DIR", "COLLIE_STATE_DIR", "COLLIE_PLUGIN_ROOT", "HERDR_SOCKET_PATH", "COLLIE_HOST", "COLLIE_PORT", "COLLIE_PACK_TRANSPORT"} {
		if value, ok := overrides[key]; ok {
			result = append(result, key+"="+value)
		}
	}
	return result
}
