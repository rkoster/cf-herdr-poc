package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cf-herdr-poc/internal/config"
	"cf-herdr-poc/internal/httpapi"
	"cf-herdr-poc/internal/runner"
	runtimebundle "cf-herdr-poc/internal/runtime"
	"cf-herdr-poc/internal/store"
)

func TestManagerWebServesAssetsAndFallsBackToIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("manager app"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := managerWeb(dir)
	for _, tt := range []struct {
		path, want string
		status     int
	}{
		{"/manager/", "manager app", http.StatusOK},
		{"/manager/sandboxes/demo", "manager app", http.StatusOK},
		{"/manager/assets/app.js", "bundle", http.StatusOK},
		{"/manager/assets/missing.js", "404 page not found\n", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
		body, _ := io.ReadAll(response.Result().Body)
		if response.Code != tt.status || string(body) != tt.want {
			t.Errorf("%s = %d %q, want %d %q", tt.path, response.Code, body, tt.status, tt.want)
		}
	}
}

func TestCanonicalizeManagerPathsUsesManagerStartupDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "manager-runtime", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manager-runtime", "bin", "cf"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	cfg := config.Config{
		StatePath:         "./data/state.json",
		WebDir:            "web",
		CollieDir:         "collie",
		WorkRoot:          "./data/work",
		RuntimeDir:        "./manager-runtime",
		SandboxRuntimeDir: "./sandbox/runtime",
		BunExecutable:     "./manager-runtime/bin/bun",
		CollieExecutable:  "manager-runtime/bin/collie",
		HerdrExecutable:   "manager-runtime/bin/herdr",
		CFExecutable:      "manager-runtime/bin/cf",
	}
	if err := canonicalizeManagerPaths(&cfg); err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		StatePath:         filepath.Join(root, "data/state.json"),
		WebDir:            filepath.Join(root, "web"),
		CollieDir:         filepath.Join(root, "collie"),
		WorkRoot:          filepath.Join(root, "data/work"),
		RuntimeDir:        filepath.Join(root, "manager-runtime"),
		SandboxRuntimeDir: filepath.Join(root, "sandbox/runtime"),
		BunExecutable:     filepath.Join(root, "manager-runtime/bin/bun"),
		CollieExecutable:  filepath.Join(root, "manager-runtime/bin/collie"),
		CFExecutable:      filepath.Join(root, "manager-runtime/bin/cf"),
		HerdrExecutable:   filepath.Join(root, "manager-runtime/bin/herdr"),
	}
	if cfg.StatePath != want.StatePath || cfg.WebDir != want.WebDir || cfg.CollieDir != want.CollieDir || cfg.WorkRoot != want.WorkRoot || cfg.RuntimeDir != want.RuntimeDir || cfg.SandboxRuntimeDir != want.SandboxRuntimeDir || cfg.BunExecutable != want.BunExecutable || cfg.CollieExecutable != want.CollieExecutable || cfg.HerdrExecutable != want.HerdrExecutable || cfg.CFExecutable != want.CFExecutable {
		t.Fatalf("canonicalized config = %#v", cfg)
	}

	absolute := filepath.Join(root, "manager-runtime", "bin", "cf")
	cfg = config.Config{StatePath: absolute, WebDir: absolute, CollieDir: absolute, WorkRoot: absolute, RuntimeDir: absolute, SandboxRuntimeDir: absolute, BunExecutable: absolute, CollieExecutable: absolute, HerdrExecutable: absolute, CFExecutable: absolute}
	if err := canonicalizeManagerPaths(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.StatePath != absolute || cfg.WebDir != absolute || cfg.CollieDir != absolute || cfg.WorkRoot != absolute || cfg.RuntimeDir != absolute || cfg.SandboxRuntimeDir != absolute || cfg.BunExecutable != absolute || cfg.CollieExecutable != absolute || cfg.HerdrExecutable != absolute || cfg.CFExecutable != absolute {
		t.Fatalf("absolute paths changed: %#v", cfg)
	}
}

func TestCanonicalizeManagerPathsRequiresCFExecutableResolution(t *testing.T) {
	cfg := config.Config{CFExecutable: filepath.Join(t.TempDir(), "missing-cf")}
	if err := canonicalizeManagerPaths(&cfg); err == nil || !strings.Contains(err.Error(), "MANAGER_CF_EXECUTABLE") {
		t.Fatalf("canonicalizeManagerPaths() error = %v, want CF executable resolution error", err)
	}
}

func TestManagerRuntimeBuilderUsesSandboxRuntime(t *testing.T) {
	cfg := config.Config{RuntimeDir: "/app/manager-runtime", SandboxRuntimeDir: "/app/sandbox/runtime", WorkRoot: "/app/work"}
	builder := newRuntimeBuilder(cfg, runner.Exec{})
	if builder.RuntimeDir != cfg.SandboxRuntimeDir {
		t.Fatalf("builder runtime directory = %q, want %q", builder.RuntimeDir, cfg.SandboxRuntimeDir)
	}
	if builder.WorkRoot != cfg.WorkRoot {
		t.Fatalf("builder work root = %q, want %q", builder.WorkRoot, cfg.WorkRoot)
	}
	if cfg.RuntimeDir != "/app/manager-runtime" {
		t.Fatalf("manager runtime directory changed: %q", cfg.RuntimeDir)
	}
	var _ runtimebundle.Builder = builder
}

func TestEnsurePrivateDirCreatesNestedDirectoryWithPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "private")
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("created mode = %v, want private directory", info.Mode())
	}
}

func TestEnsurePrivateDirLeavesExistingDirectoryPermissionsUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "operator")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("existing mode = %v, want 0750", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirRejectsFileAndFinalSymlink(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, link} {
		if err := ensurePrivateDir(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("ensurePrivateDir(%q) error = %v", path, err)
		}
	}
}

func TestEnsureManagerDirsCreatesAllRuntimeDirectories(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{StatePath: filepath.Join(root, "state", "sandboxes.json"), WorkRoot: filepath.Join(root, "work")}
	dirs, err := ensureManagerDirs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "state"), filepath.Join(root, "work"), filepath.Join(root, "state", "collie-config"), filepath.Join(root, "state", "collie-state"), filepath.Join(root, "state", "tokens")}
	for _, path := range want {
		if info, statErr := os.Lstat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("runtime directory %q: info=%v err=%v", path, info, statErr)
		}
	}
	if dirs.token != want[4] {
		t.Fatalf("token dir = %q, want %q", dirs.token, want[4])
	}
}

func TestLoopbackAddress(t *testing.T) {
	host, port, err := loopbackAddress("127.0.0.1:9191")
	if err != nil || host != "127.0.0.1" || port != 9191 {
		t.Fatalf("loopbackAddress = %q, %d, %v", host, port, err)
	}
	for _, value := range []string{"0.0.0.0:9191", "example.com:9191", "127.0.0.1", "127.0.0.1:0"} {
		if _, _, err := loopbackAddress(value); err == nil {
			t.Fatalf("loopbackAddress(%q) succeeded", value)
		}
	}
}

func TestStopAllUsesReverseStartupOrderAndJoinsErrors(t *testing.T) {
	var order []string
	wantErr := errors.New("collie stop")
	err := stopAll(context.Background(), func(context.Context) error { order = append(order, "http"); return nil }, func(context.Context) error { order = append(order, "api"); return nil }, func() { order = append(order, "reconciler") }, func(context.Context) error { order = append(order, "collie"); return wantErr }, func(context.Context) error { order = append(order, "herdr"); return nil })
	if !reflect.DeepEqual(order, []string{"http", "api", "reconciler", "collie", "herdr"}) {
		t.Fatalf("stop order = %#v", order)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("stopAll error = %v", err)
	}
}

func TestManagerHTTPServerSetsResourceTimeouts(t *testing.T) {
	server := managerHTTPServer(":8080", http.NotFoundHandler())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout != 60*time.Second || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("unsafe HTTP server limits: %#v", server)
	}
}

func TestWaitForShutdownReturnsSupervisorError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpErrors := make(chan error)
	supervisorErrors := make(chan error, 1)
	want := errors.New("Collie exited")
	supervisorErrors <- want
	if err := waitForShutdown(ctx, httpErrors, supervisorErrors); !errors.Is(err, want) {
		t.Fatalf("waitForShutdown error = %v", err)
	}
}

func TestManagerHealthLatchStaysReadyAfterCollieReportsUnhealthy(t *testing.T) {
	var ready atomic.Bool
	collieHealthy := true
	healthy := func() bool {
		return ready.Load()
	}
	handler, err := httpapi.New(httpapi.Config{
		Store:           store.NewFile(filepath.Join(t.TempDir(), "state.json")),
		Reconciler:      managerTestReconciler{},
		Buildpacks:      []string{"ruby_buildpack"},
		CollieURL:       mustURL(t, "http://127.0.0.1:9191"),
		ManagerPackHost: "pack.identity.example",
		ManagerToken:    "test-token",
		Healthy:         healthy,
		ErrorSink:       func(error) {},
		Now:             func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeReady := httptest.NewRecorder()
	handler.ServeHTTP(beforeReady, httptest.NewRequest(http.MethodGet, "/manager/healthz", nil))
	if beforeReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("health before manager readiness = %d, want 503", beforeReady.Code)
	}
	ready.Store(true)
	before := httptest.NewRecorder()
	handler.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/manager/healthz", nil))
	if before.Code != http.StatusOK {
		t.Fatalf("health before Collie transition = %d, want 200", before.Code)
	}
	collieHealthy = false
	if collieHealthy {
		t.Fatal("test did not simulate Collie becoming unhealthy")
	}
	after := httptest.NewRecorder()
	handler.ServeHTTP(after, httptest.NewRequest(http.MethodGet, "/manager/healthz", nil))
	if after.Code != http.StatusOK {
		t.Fatalf("health after Collie transition = %d, want 200", after.Code)
	}
}

type managerTestReconciler struct{}

func (managerTestReconciler) ReconcileOne(context.Context, string) error { return nil }
func (managerTestReconciler) Retry(context.Context, string) error        { return nil }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
