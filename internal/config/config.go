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
	Buildpacks        []string
	ReconcileInterval time.Duration
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
		reconcileInterval = parsed
	}

	return Config{
		Address:           address,
		StatePath:         valueOrDefault("MANAGER_STATE_PATH", "./data/sandboxes.json"),
		WebDir:            valueOrDefault("MANAGER_WEB_DIR", "./web/dist"),
		CollieDir:         valueOrDefault("MANAGER_COLLIE_DIR", "./collie"),
		IdentityDomain:    identityDomain,
		ManagerAppName:    value("MANAGER_APP_NAME"),
		ManagerAppGUID:    value("MANAGER_APP_GUID"),
		ManagerPackHost:   value("MANAGER_PACK_HOST"),
		Buildpacks:        buildpacks,
		ReconcileInterval: reconcileInterval,
	}, nil
}
