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
	host, port, err := loopbackAddress(cfg.CollieAddress)
	if err != nil {
		return err
	}
	state := store.NewFile(cfg.StatePath)
	if err := state.Load(); err != nil {
		return fmt.Errorf("load sandbox state: %w", err)
	}

	commandRunner := runner.Exec{}
	configDir := filepath.Join(filepath.Dir(cfg.StatePath), "collie-config")
	stateDir := filepath.Join(filepath.Dir(cfg.StatePath), "collie-state")
	socketPath := filepath.Join(stateDir, "herdr.sock")
	collie := supervisor.New(supervisor.Config{Dir: cfg.CollieDir, ConfigDir: configDir, StateDir: stateDir, SocketPath: socketPath, Host: host, Port: port}, nil, nil)
	packManager := pack.New(commandRunner, collie, pack.Config{Executable: "bun", TempDir: filepath.Join(filepath.Dir(cfg.StatePath), "tokens"), ConfigDir: configDir, StateDir: stateDir, SocketPath: socketPath, Host: host, Port: port, TokenLifetime: 10 * time.Minute})
	builder := runtimebundle.Builder{Run: commandRunner, RuntimeDir: cfg.RuntimeDir, WorkRoot: cfg.WorkRoot}
	cloud := cf.Provider{Run: commandRunner, Buildpacks: cfg.Buildpacks, WorkRoot: cfg.WorkRoot}
	probe := identity.New(identity.Config{CertPath: cfg.InstanceCert, KeyPath: cfg.InstanceKey, Timeout: 10 * time.Second, MaxBodyBytes: 64 << 10})
	reconciler := reconcile.New(reconcile.Config{WorkRoot: cfg.WorkRoot, IdentityDomain: cfg.IdentityDomain, ManagerRouteHost: cfg.ManagerPackHost, ManagerPackHost: cfg.ManagerPackHost, ManagerAppGUID: cfg.ManagerAppGUID, PollAttempts: 30, PollInterval: time.Second, ScanInterval: cfg.ReconcileInterval}, state, reconcile.BundleRuntime{Builder: builder}, cloud, reconcile.ConcretePackManager{Manager: packManager}, probe, realClock{})
	collieURL, _ := url.Parse("http://" + cfg.CollieAddress)
	web := http.StripPrefix("/manager/", http.FileServer(http.Dir(cfg.WebDir)))
	handler, err := httpapi.New(httpapi.Config{Store: state, Reconciler: reconciler, Buildpacks: cfg.Buildpacks, CollieURL: collieURL, ManagerPackHost: cfg.ManagerPackHost, Authorize: httpapi.BearerAuthorizer(cfg.APIToken), Web: web})
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
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errorsChannel := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- err
		}
		close(errorsChannel)
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		if err != nil {
			cancel()
			serveErr = fmt.Errorf("serve manager HTTP: %w", err)
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return errors.Join(serveErr, stopAll(shutdownCtx, server.Shutdown, reconciler.Stop, collie.Stop))
}

func stopAll(ctx context.Context, stopHTTP func(context.Context) error, stopReconciler func(), stopCollie func(context.Context) error) error {
	httpErr := stopHTTP(ctx)
	stopReconciler()
	return errors.Join(httpErr, stopCollie(ctx))
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
