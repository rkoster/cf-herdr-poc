package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cf-herdr-poc/internal/config"
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

func TestCanonicalizeColliePathsUsesManagerStartupDirectory(t *testing.T) {
	root := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDir) })

	cfg := config.Config{CollieDir: "collie", BunExecutable: "./bin/bun", CollieExecutable: "bin/collie"}
	if err := canonicalizeColliePaths(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.CollieDir != filepath.Join(root, "collie") || cfg.BunExecutable != filepath.Join(root, "bin/bun") || cfg.CollieExecutable != filepath.Join(root, "bin/collie") {
		t.Fatalf("canonicalized config = %#v", cfg)
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
	err := stopAll(context.Background(), func(context.Context) error { order = append(order, "http"); return nil }, func(context.Context) error { order = append(order, "api"); return nil }, func() { order = append(order, "reconciler") }, func(context.Context) error { order = append(order, "collie"); return wantErr })
	if !reflect.DeepEqual(order, []string{"http", "api", "reconciler", "collie"}) {
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
