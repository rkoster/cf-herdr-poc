package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cf-herdr-poc/internal/cf"
	"cf-herdr-poc/internal/config"
	"cf-herdr-poc/internal/httpapi"
	"cf-herdr-poc/internal/identity"
	"cf-herdr-poc/internal/pack"
	"cf-herdr-poc/internal/reconcile"
	"cf-herdr-poc/internal/runner"
	runtimebundle "cf-herdr-poc/internal/runtime"
	"cf-herdr-poc/internal/store"
	"cf-herdr-poc/internal/supervisor"
)

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("load manager config: %w", err)
	}
	if err := cfg.ValidateProduction(); err != nil {
		return fmt.Errorf("validate manager config: %w", err)
	}
	if err := canonicalizeManagerPaths(&cfg); err != nil {
		return err
	}
	dirs, err := ensureManagerDirs(cfg)
	if err != nil {
		return fmt.Errorf("initialize manager runtime directories: %w", err)
	}
	host, port, err := loopbackAddress(cfg.CollieAddress)
	if err != nil {
		return err
	}
	state := store.NewFile(cfg.StatePath)
	if err := state.Load(); err != nil {
		return fmt.Errorf("load sandbox state: %w", err)
	}

	commandRunner := runner.Exec{}
	configDir := dirs.collieConfig
	stateDir := dirs.collieState
	socketPath := filepath.Join(stateDir, "herdr.sock")
	collie := supervisor.New(supervisor.Config{Executable: cfg.BunExecutable, Dir: cfg.CollieDir, PluginRoot: cfg.CollieDir, ConfigDir: configDir, StateDir: stateDir, SocketPath: socketPath, Host: host, Port: port, PackTransport: "cf-identity"}, nil, nil)
	packManager := pack.New(commandRunner, collie, pack.Config{Executable: cfg.CollieExecutable, TempDir: dirs.token, PluginRoot: cfg.CollieDir, ConfigDir: configDir, StateDir: stateDir, SocketPath: socketPath, Host: host, Port: port, TokenLifetime: 10 * time.Minute})
	builder := runtimebundle.Builder{Run: commandRunner, RuntimeDir: cfg.RuntimeDir, WorkRoot: cfg.WorkRoot}
	cloud := cf.Provider{Run: commandRunner, Buildpacks: cfg.Buildpacks, WorkRoot: cfg.WorkRoot}
	probe := identity.New(identity.Config{CertPath: cfg.InstanceCert, KeyPath: cfg.InstanceKey, Timeout: 10 * time.Second, MaxBodyBytes: 64 << 10})
	reconciler := reconcile.New(reconcile.Config{WorkRoot: cfg.WorkRoot, IdentityDomain: cfg.IdentityDomain, ManagerRouteHost: cfg.ManagerRouteHost, ManagerPackHost: cfg.ManagerPackHost, ManagerAppGUID: cfg.ManagerAppGUID, PollAttempts: 30, PollInterval: time.Second, ScanInterval: cfg.ReconcileInterval}, state, reconcile.BundleRuntime{Builder: builder}, cloud, reconcile.ConcretePackManager{Manager: packManager}, probe, realClock{})
	collieURL, _ := url.Parse("http://" + cfg.CollieAddress)
	web := managerWeb(cfg.WebDir)
	handler, err := httpapi.New(httpapi.Config{Store: state, Reconciler: reconciler, Buildpacks: cfg.Buildpacks, CollieURL: collieURL, ManagerPackHost: cfg.ManagerPackHost, ManagerToken: cfg.APIToken, TrustForwardedProto: true, Healthy: collie.Healthy, ErrorSink: func(err error) { log.Printf("manager API reconciliation: %v", err) }, Web: web})
	if err != nil {
		return fmt.Errorf("build HTTP gateway: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := collie.Start(ctx); err != nil {
		return fmt.Errorf("start lead Collie: %w", err)
	}
	if err := collie.Ready(ctx); err != nil {
		_ = collie.Stop(context.Background())
		return fmt.Errorf("wait for lead Collie: %w", err)
	}
	reconciler.Start(ctx)
	server := managerHTTPServer(cfg.Address, handler)
	errorsChannel := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- err
		}
		close(errorsChannel)
	}()

	serveErr := waitForShutdown(ctx, errorsChannel, collie.Errors())
	if serveErr != nil {
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return errors.Join(serveErr, stopAll(shutdownCtx, server.Shutdown, handler.Close, reconciler.Stop, collie.Stop))
}

type managerDirs struct {
	collieConfig string
	collieState  string
	token        string
}

func ensureManagerDirs(cfg config.Config) (managerDirs, error) {
	base := filepath.Dir(cfg.StatePath)
	dirs := managerDirs{
		collieConfig: filepath.Join(base, "collie-config"),
		collieState:  filepath.Join(base, "collie-state"),
		token:        filepath.Join(base, "tokens"),
	}
	for name, path := range map[string]string{
		"state parent":  base,
		"work root":     cfg.WorkRoot,
		"Collie config": dirs.collieConfig,
		"Collie state":  dirs.collieState,
		"token":         dirs.token,
	} {
		if err := ensurePrivateDir(path); err != nil {
			return managerDirs{}, fmt.Errorf("ensure %s directory: %w", name, err)
		}
	}
	return dirs, nil
}

func ensurePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q is a symlink", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("%q is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	info, err = os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect created directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("created path %q is not a physical directory", path)
	}
	return nil
}

func canonicalizeManagerPaths(cfg *config.Config) error {
	paths := []struct {
		name  string
		value *string
	}{
		{name: "MANAGER_STATE_PATH", value: &cfg.StatePath},
		{name: "MANAGER_WEB_DIR", value: &cfg.WebDir},
		{name: "MANAGER_COLLIE_DIR", value: &cfg.CollieDir},
		{name: "MANAGER_WORK_ROOT", value: &cfg.WorkRoot},
		{name: "MANAGER_RUNTIME_DIR", value: &cfg.RuntimeDir},
		{name: "MANAGER_BUN_EXECUTABLE", value: &cfg.BunExecutable},
		{name: "MANAGER_COLLIE_EXECUTABLE", value: &cfg.CollieExecutable},
	}
	for _, path := range paths {
		if filepath.IsAbs(*path.value) {
			continue
		}
		absolute, err := filepath.Abs(*path.value)
		if err != nil {
			return fmt.Errorf("resolve %s %q: %w", path.name, *path.value, err)
		}
		*path.value = absolute
	}
	return nil
}

func managerWeb(dir string) http.Handler {
	files := http.StripPrefix("/manager/", http.FileServer(http.Dir(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/manager/")
		if path == "" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path))); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		if strings.Contains(filepath.Base(path), ".") {
			http.NotFound(w, r)
			return
		}
		request := r.Clone(r.Context())
		request.URL.Path = "/manager/"
		files.ServeHTTP(w, request)
	})
}

func waitForShutdown(ctx context.Context, httpErrors, supervisorErrors <-chan error) error {
	select {
	case <-ctx.Done():
		return nil
	case err := <-httpErrors:
		if err == nil {
			return nil
		}
		return fmt.Errorf("serve manager HTTP: %w", err)
	case err := <-supervisorErrors:
		return err
	}
}

func managerHTTPServer(address string, handler http.Handler) *http.Server {
	// A bounded WriteTimeout limits stuck clients while still allowing POC reverse-proxy streams.
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
}

func stopAll(ctx context.Context, stopHTTP, stopAPI func(context.Context) error, stopReconciler func(), stopCollie func(context.Context) error) error {
	httpErr := stopHTTP(ctx)
	apiErr := stopAPI(ctx)
	stopReconciler()
	return errors.Join(httpErr, apiErr, stopCollie(ctx))
}

func loopbackAddress(address string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("MANAGER_COLLIE_ADDRESS must be host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", 0, errors.New("MANAGER_COLLIE_ADDRESS must use a loopback IP")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("MANAGER_COLLIE_ADDRESS has an invalid port")
	}
	return host, port, nil
}
