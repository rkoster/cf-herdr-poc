package config

import (
	"fmt"
	"strings"
	"time"
)

const defaultReconcileInterval = 2 * time.Second

type Config struct {
	Address           string
	StatePath         string
	WebDir            string
	CollieDir         string
	IdentityDomain    string
	ManagerAppName    string
	ManagerAppGUID    string
	ManagerPackHost   string
	ManagerRouteHost  string
	Buildpacks        []string
	ReconcileInterval time.Duration
	APIToken          string
	CollieAddress     string
	WorkRoot          string
	RuntimeDir        string
	BunExecutable     string
	CollieExecutable  string
	CFExecutable      string
	InstanceCert      string
	InstanceKey       string
}

func Load(getenv func(string) string) (Config, error) {
	value := func(key string) string { return strings.TrimSpace(getenv(key)) }
	valueOrDefault := func(key, defaultValue string) string {
		if result := value(key); result != "" {
			return result
		}
		return defaultValue
	}

	identityDomain := value("CF_IDENTITY_DOMAIN")
	if identityDomain == "" {
		return Config{}, fmt.Errorf("CF_IDENTITY_DOMAIN is required")
	}
	managerPackHost := value("MANAGER_PACK_HOST")
	managerRouteHost := ""
	if managerPackHost != "" {
		suffix := "." + identityDomain
		managerRouteHost = strings.TrimSuffix(managerPackHost, suffix)
		if managerRouteHost == managerPackHost || managerRouteHost == "" || strings.Contains(managerRouteHost, ".") || !validDNSName(managerPackHost) || managerRouteHost+suffix != managerPackHost {
			return Config{}, fmt.Errorf("MANAGER_PACK_HOST must be a direct child of CF_IDENTITY_DOMAIN")
		}
	}

	buildpackValue := value("SANDBOX_BUILDPACKS")
	if buildpackValue == "" {
		return Config{}, fmt.Errorf("SANDBOX_BUILDPACKS must contain at least one buildpack")
	}
	buildpacks := strings.Split(buildpackValue, ",")
	for i := range buildpacks {
		buildpacks[i] = strings.TrimSpace(buildpacks[i])
		if buildpacks[i] == "" {
			return Config{}, fmt.Errorf("SANDBOX_BUILDPACKS must not contain empty buildpacks")
		}
	}

	address := value("MANAGER_ADDRESS")
	if address == "" {
		if port := value("PORT"); port != "" {
			address = ":" + port
		} else {
			address = ":8080"
		}
	}

	reconcileInterval := defaultReconcileInterval
	if raw := value("MANAGER_RECONCILE_INTERVAL"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MANAGER_RECONCILE_INTERVAL must be a valid duration: %w", err)
		}
		if parsed <= 0 {
			return Config{}, fmt.Errorf("MANAGER_RECONCILE_INTERVAL must be positive")
		}
		reconcileInterval = parsed
	}

	runtimeDir := valueOrDefault("MANAGER_RUNTIME_DIR", "./sandbox/runtime")
	return Config{
		Address:           address,
		StatePath:         valueOrDefault("MANAGER_STATE_PATH", "./data/sandboxes.json"),
		WebDir:            valueOrDefault("MANAGER_WEB_DIR", "./web/dist"),
		CollieDir:         valueOrDefault("MANAGER_COLLIE_DIR", "./collie"),
		IdentityDomain:    identityDomain,
		ManagerAppName:    value("MANAGER_APP_NAME"),
		ManagerAppGUID:    value("MANAGER_APP_GUID"),
		ManagerPackHost:   managerPackHost,
		ManagerRouteHost:  managerRouteHost,
		Buildpacks:        buildpacks,
		ReconcileInterval: reconcileInterval,
		APIToken:          value("MANAGER_API_TOKEN"),
		CollieAddress:     valueOrDefault("MANAGER_COLLIE_ADDRESS", "127.0.0.1:9191"),
		WorkRoot:          valueOrDefault("MANAGER_WORK_ROOT", "./data/work"),
		RuntimeDir:        runtimeDir,
		BunExecutable:     valueOrDefault("MANAGER_BUN_EXECUTABLE", runtimeDir+"/bin/bun"),
		CollieExecutable:  valueOrDefault("MANAGER_COLLIE_EXECUTABLE", runtimeDir+"/bin/collie"),
		CFExecutable:      valueOrDefault("MANAGER_CF_EXECUTABLE", "./manager-runtime/bin/cf"),
		InstanceCert:      valueOrDefault("CF_INSTANCE_CERT", "/etc/cf-instance-credentials/instance.crt"),
		InstanceKey:       valueOrDefault("CF_INSTANCE_KEY", "/etc/cf-instance-credentials/instance.key"),
	}, nil
}

func validDNSName(value string) bool {
	if len(value) > 253 || value == "" {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func (c Config) ValidateProduction() error {
	for key, value := range map[string]string{"MANAGER_APP_NAME": c.ManagerAppName, "MANAGER_APP_GUID": c.ManagerAppGUID, "MANAGER_PACK_HOST": c.ManagerPackHost, "MANAGER_API_TOKEN": c.APIToken} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", key)
		}
	}
	return nil
}
