package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type fakeJoiner struct {
	mu    sync.Mutex
	calls int
	args  []string
	input string
	err   error
}

func (j *fakeJoiner) Join(_ context.Context, args []string, input io.Reader) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls++
	j.args = append([]string(nil), args...)
	data, _ := io.ReadAll(input)
	j.input = string(data)
	return j.err
}

func TestHandlerRoutesAndMethods(t *testing.T) {
	server, _ := fixture(t, &fakeJoiner{})
	tests := []struct {
		method, path string
		status       int
	}{{"GET", "/bootstrap/health", 200}, {"POST", "/bootstrap/health", 405}, {"GET", "/bootstrap/join", 405}, {"POST", "/bootstrap/join", 200}, {"GET", "/other", 404}}
	for _, tt := range tests {
		request := httptest.NewRequest(tt.method, tt.path, nil)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != tt.status {
			t.Fatalf("%s %s=%d want %d", tt.method, tt.path, response.Code, tt.status)
		}
		if tt.path == "/bootstrap/health" && tt.method == "GET" && !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("content type=%q", response.Header().Get("Content-Type"))
		}
	}
}

func TestJoinUsesFixedArgvAndTokenOnStdin(t *testing.T) {
	joiner := &fakeJoiner{}
	server, config := fixture(t, joiner)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("POST", "/bootstrap/join", nil))
	want := []string{"pack", "join", "https://manager.identity.example", "-", "--label", "demo", "--address", "demo.apps.identity"}
	if !reflect.DeepEqual(joiner.args, want) || joiner.input != "super-secret\n" {
		t.Fatalf("args/input=(%#v,%q)", joiner.args, joiner.input)
	}
	if _, err := os.Stat(config.TokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token remains: %v", err)
	}
	info, err := os.Stat(config.ReadyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("marker=%v %v", info, err)
	}
}

func TestRepeatedAndConcurrentJoinIsIdempotent(t *testing.T) {
	joiner := &fakeJoiner{}
	server, _ := fixture(t, joiner)
	const requests = 12
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		go func() {
			defer wg.Done()
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest("POST", "/bootstrap/join", nil))
			if response.Code != http.StatusOK {
				t.Errorf("status=%d", response.Code)
			}
		}()
	}
	wg.Wait()
	if joiner.calls != 1 {
		t.Fatalf("join calls=%d", joiner.calls)
	}
}

func TestJoinFailureSanitizesResponseAndLog(t *testing.T) {
	joiner := &fakeJoiner{err: errors.New("Authorization: Bearer super-secret")}
	server, _ := fixture(t, joiner)
	var log bytes.Buffer
	server.log = &log
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("POST", "/bootstrap/join", nil))
	combined := response.Body.String() + log.String()
	if response.Code != 500 || strings.Contains(combined, "super-secret") || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("status/body/log=%d %q", response.Code, combined)
	}
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	base := Config{Executable: "collie", TokenPath: "/tmp/token", ReadyPath: "/tmp/ready", TrustStorePath: "/tmp/trust", LeadAddress: "https://manager.identity.example", MemberID: "demo", SelfAddress: "demo.apps.identity"}
	for _, mutate := range []func(*Config){func(c *Config) { c.MemberID = "bad id" }, func(c *Config) { c.LeadAddress = "http://manager" }, func(c *Config) { c.TokenPath = "" }, func(c *Config) { c.ReadyPath = "" }} {
		config := base
		mutate(&config)
		if _, err := New(config, &fakeJoiner{}, io.Discard); err == nil {
			t.Fatalf("accepted %#v", config)
		}
	}
}

func TestMarkerWriteFailureLeavesTokenForRecovery(t *testing.T) {
	joiner := &fakeJoiner{}
	server, config := fixture(t, joiner)
	config.ReadyPath = filepath.Join(t.TempDir(), "missing", "ready")
	server.config = config
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("POST", "/bootstrap/join", nil))
	if response.Code != 500 {
		t.Fatalf("status=%d", response.Code)
	}
	if _, err := os.Stat(config.TokenPath); err != nil {
		t.Fatalf("token removed: %v", err)
	}
}

func TestTrustStoreRecoversMarkerWithoutRejoining(t *testing.T) {
	joiner := &fakeJoiner{}
	server, config := fixture(t, joiner)
	if err := os.WriteFile(config.TrustStorePath, []byte("opaque\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("POST", "/bootstrap/join", nil))
	if response.Code != 200 || joiner.calls != 0 {
		t.Fatalf("status/calls=%d/%d", response.Code, joiner.calls)
	}
	if _, err := os.Stat(config.ReadyPath); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if _, err := os.Stat(config.TokenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token remains: %v", err)
	}
}

func TestTrustStoreSymlinkIsRejected(t *testing.T) {
	joiner := &fakeJoiner{}
	server, config := fixture(t, joiner)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, config.TrustStorePath); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("POST", "/bootstrap/join", nil))
	if response.Code != 500 || joiner.calls != 0 {
		t.Fatalf("status/calls=%d/%d", response.Code, joiner.calls)
	}
}

func fixture(t *testing.T, joiner *fakeJoiner) (*Server, Config) {
	t.Helper()
	root := t.TempDir()
	token := filepath.Join(root, "token")
	if err := os.WriteFile(token, []byte("super-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Executable: "collie", TokenPath: token, ReadyPath: filepath.Join(root, "ready"), TrustStorePath: filepath.Join(root, "pack-trust.json"), LeadAddress: "https://manager.identity.example", MemberID: "demo", SelfAddress: "demo.apps.identity"}
	server, err := New(config, joiner, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return server, config
}
