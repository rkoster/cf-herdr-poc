package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cf-herdr-poc/internal/cf"
	"cf-herdr-poc/internal/model"
	"cf-herdr-poc/internal/reconcile"
)

type memoryStore struct {
	mu    sync.Mutex
	items map[string]model.Sandbox
}

func (s *memoryStore) List() []model.Sandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Sandbox, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out
}
func (s *memoryStore) Get(name string) (model.Sandbox, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[name]
	return v, ok
}
func (s *memoryStore) Create(v model.Sandbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[v.Name]; ok {
		return errDuplicate
	}
	s.items[v.Name] = v
	return nil
}
func (s *memoryStore) Update(name string, fn func(*model.Sandbox) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[name]
	if !ok {
		return errMissing
	}
	if err := fn(&v); err != nil {
		return err
	}
	s.items[name] = v
	return nil
}
func (s *memoryStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, name)
	return nil
}

var errDuplicate = &storeError{"duplicate"}
var errMissing = &storeError{"missing"}

type storeError struct{ message string }

func (e *storeError) Error() string { return e.message }

type fakeReconciler struct {
	reconciled chan string
	retried    chan string
}

func (f *fakeReconciler) ReconcileOne(_ context.Context, name string) error {
	f.reconciled <- name
	return nil
}
func (f *fakeReconciler) Retry(_ context.Context, name string) error { f.retried <- name; return nil }

type blockingReconciler struct {
	reconcile func(context.Context, string) error
}

func (f *blockingReconciler) ReconcileOne(ctx context.Context, name string) error {
	return f.reconcile(ctx, name)
}
func (f *blockingReconciler) Retry(ctx context.Context, name string) error {
	return f.reconcile(ctx, name)
}

func newTestHandler(t *testing.T, store *memoryStore, reconciler Reconciler, collie *httptest.Server) *Handler {
	return newTestHandlerWithHealth(t, store, reconciler, collie, func() bool { return true })
}

func newTestHandlerWithHealth(t *testing.T, store *memoryStore, reconciler Reconciler, collie *httptest.Server, healthy func() bool) *Handler {
	t.Helper()
	u, err := url.Parse(collie.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Store: store, Reconciler: reconciler, Buildpacks: []string{"ruby_buildpack"}, CollieURL: u, ManagerPackHost: "pack.identity.example", ManagerToken: "test-token", Healthy: healthy, ErrorSink: func(error) {}, Now: func() time.Time { return time.Unix(100, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(t *testing.T, handler http.Handler, method, target, body, host string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = host
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if auth {
		r.Header.Set("Authorization", "Bearer test-token")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestManagerAPIRequiresAuthorization(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)
	if got := request(t, h, http.MethodGet, "/manager/api/sandboxes", "", "public.example", false).Code; got != http.StatusUnauthorized {
		t.Fatalf("status = %d", got)
	}
	if got := request(t, h, http.MethodGet, "/manager/healthz", "", "public.example", false).Code; got != http.StatusOK {
		t.Fatalf("health status = %d", got)
	}
}

func TestConfigReturnsProtectedBuildpackAllowlist(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)

	if got := request(t, h, http.MethodGet, "/manager/api/config", "", "public.example", false).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized config status = %d", got)
	}
	response := request(t, h, http.MethodGet, "/manager/api/config", "", "public.example", true)
	if response.Code != http.StatusOK || response.Body.String() != "{\"buildpacks\":[\"ruby_buildpack\"]}\n" {
		t.Fatalf("config = %d %q", response.Code, response.Body.String())
	}
	if got := request(t, h, http.MethodPost, "/manager/api/config", "{}", "public.example", true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("POST config status = %d", got)
	}
}

func TestManagerWebFallsBackToIndexForClientRoutes(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	store := &memoryStore{items: map[string]model.Sandbox{}}
	h := newTestHandler(t, store, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)
	h.config.Web = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manager/sandboxes/demo" {
			t.Fatalf("web path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("manager app"))
	})

	response := request(t, h, http.MethodGet, "/manager/sandboxes/demo", "", "public.example", false)
	if response.Code != http.StatusOK || response.Body.String() != "manager app" {
		t.Fatalf("web response = %d %q", response.Code, response.Body.String())
	}
}

func TestSessionCookieAuthorizesAPIAndCollie(t *testing.T) {
	collie := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)
	login := httptest.NewRequest(http.MethodPost, "/manager/api/session", strings.NewReader(`{"token":"test-token"}`))
	login.Host = "public.example"
	login.Header.Set("Content-Type", "application/json")
	login.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	cookies := w.Result().Cookies()
	if w.Code != http.StatusNoContent || len(cookies) != 1 {
		t.Fatalf("login = %d, cookies %#v", w.Code, cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || strings.Contains(cookie.Value, "test-token") {
		t.Fatalf("unsafe session cookie: %#v", cookie)
	}
	for _, path := range []string{"/manager/api/sandboxes", "/collie/assets/app.js"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "public.example"
		r.AddCookie(cookie)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		if response.Code == http.StatusUnauthorized {
			t.Fatalf("cookie rejected for %s", path)
		}
	}
	forged := *cookie
	forged.Value += "x"
	r := httptest.NewRequest(http.MethodGet, "/collie/assets/app.js", nil)
	r.Host = "public.example"
	r.AddCookie(&forged)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie status = %d", response.Code)
	}
}

func TestSessionForwardedProtoTrustOnlyPermitsLoginTransport(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	u, _ := url.Parse(collie.URL)
	config := Config{Store: &memoryStore{items: map[string]model.Sandbox{}}, Reconciler: &fakeReconciler{make(chan string, 1), make(chan string, 1)}, Buildpacks: []string{"ruby_buildpack"}, CollieURL: u, ManagerPackHost: "pack.identity.example", ManagerToken: "test-token", Healthy: func() bool { return true }, ErrorSink: func(error) {}}
	untrusted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	login := func(h http.Handler, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/manager/api/session", strings.NewReader(`{"token":"`+token+`"}`))
		r.Host = "public.example"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := login(untrusted, "test-token").Code; got != http.StatusBadRequest {
		t.Fatalf("untrusted header login status = %d", got)
	}
	config.TrustForwardedProto = true
	trusted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := login(trusted, "wrong").Code; got != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", got)
	}
	if got := login(trusted, "test-token"); got.Code != http.StatusNoContent || len(got.Result().Cookies()) != 1 {
		t.Fatalf("trusted login = %d", got.Code)
	}
	probe := httptest.NewRequest(http.MethodGet, "/manager/api/sandboxes", nil)
	probe.Host = "public.example"
	probe.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	trusted.ServeHTTP(w, probe)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("forwarded proto weakened auth: %d", w.Code)
	}
}

func TestSessionLogoutClearsCookie(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)
	r := httptest.NewRequest(http.MethodDelete, "/manager/api/session", nil)
	r.Host = "public.example"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout = %d %#v", w.Code, w.Result().Cookies())
	}
}

func TestManagerAPINamespaceAuthenticatesBeforeDispatch(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)
	for _, tt := range []struct {
		name, method, path string
		auth               bool
		want               int
	}{
		{"unauthorized method", http.MethodPut, "/manager/api/sandboxes", false, http.StatusUnauthorized},
		{"authorized method", http.MethodPut, "/manager/api/sandboxes", true, http.StatusMethodNotAllowed},
		{"unauthorized unknown", http.MethodGet, "/manager/api/unknown", false, http.StatusUnauthorized},
		{"authorized unknown", http.MethodGet, "/manager/api/unknown", true, http.StatusNotFound},
		{"namespace root", http.MethodGet, "/manager/api", true, http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := request(t, h, tt.method, tt.path, "", "public.example", tt.auth)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tt.want, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Fatalf("content type = %q", got)
			}
		})
	}
}

func TestHealthReflectsDependencyReadiness(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	healthy := false
	h := newTestHandlerWithHealth(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie, func() bool { return healthy })
	if got := request(t, h, http.MethodGet, "/manager/healthz", "", "public.example", false).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d", got)
	}
	healthy = true
	if got := request(t, h, http.MethodGet, "/manager/healthz", "", "public.example", false).Code; got != http.StatusOK {
		t.Fatalf("healthy status = %d", got)
	}
}

func TestSandboxLifecycleAPI(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	store := &memoryStore{items: map[string]model.Sandbox{}}
	reconciler := &fakeReconciler{make(chan string, 4), make(chan string, 4)}
	h := newTestHandler(t, store, reconciler, collie)

	created := request(t, h, http.MethodPost, "/manager/api/sandboxes", `{"name":"demo","repository":"git@example.com:team/demo.git","buildpack":"ruby_buildpack"}`, "public.example", true)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	stored, ok := store.Get("demo")
	if !ok || stored.Desired != model.DesiredPresent || stored.Phase != model.PhaseCreating || stored.CreatedAt.IsZero() {
		t.Fatalf("stored = %#v, %v", stored, ok)
	}
	select {
	case name := <-reconciler.reconciled:
		if name != "demo" {
			t.Fatal(name)
		}
	case <-time.After(time.Second):
		t.Fatal("reconcile not signaled")
	}
	if got := request(t, h, http.MethodPost, "/manager/api/sandboxes", `{"name":"demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack"}`, "public.example", true).Code; got != http.StatusConflict {
		t.Fatalf("duplicate status = %d", got)
	}

	retry := request(t, h, http.MethodPost, "/manager/api/sandboxes/demo/retry", "", "public.example", true)
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d", retry.Code)
	}
	select {
	case <-reconciler.retried:
	case <-time.After(time.Second):
		t.Fatal("retry not invoked")
	}

	deleted := request(t, h, http.MethodDelete, "/manager/api/sandboxes/demo", "", "public.example", true)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d", deleted.Code)
	}
	stored, _ = store.Get("demo")
	if stored.Desired != model.DesiredDeleted || stored.Phase != model.PhaseDeleting {
		t.Fatalf("delete was not persisted first: %#v", stored)
	}
}

type ownershipRunner struct {
	mu       sync.Mutex
	commands [][]string
}

func (r *ownershipRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, append([]string{name}, args...))
	if len(args) == 3 && args[0] == "app" && args[2] == "--guid" {
		return []byte("22222222-2222-4222-8222-222222222222"), nil
	}
	return nil, errors.New("unexpected CF mutation")
}

type ownershipRuntime struct{ path string }

func (r ownershipRuntime) Prepare(context.Context, string, string) (reconcile.Prepared, error) {
	return reconcile.Prepared{Path: r.path, Revision: "revision"}, nil
}
func (ownershipRuntime) Prepared(string) (bool, error)          { return true, nil }
func (ownershipRuntime) InstallEnrollment(string, string) error { return nil }
func (ownershipRuntime) Cleanup(string) error                   { return nil }

type ownershipEnrollment struct{ path string }

func (e ownershipEnrollment) Path() string       { return e.path }
func (ownershipEnrollment) ExpiresAt() time.Time { return time.Time{} }
func (ownershipEnrollment) Cleanup() error       { return nil }

type ownershipPack struct{ enrollment ownershipEnrollment }

func (p ownershipPack) PrepareEnrollment(context.Context, string, string) (reconcile.Enrollment, error) {
	return p.enrollment, nil
}
func (ownershipPack) MemberPresent(context.Context, string) (bool, error) { return false, nil }
func (ownershipPack) RemoveMember(context.Context, string) error          { return nil }

type ownershipProbe struct{}

func (ownershipProbe) Reachable(context.Context, string) (bool, error) { return false, nil }
func (ownershipProbe) TriggerEnrollment(context.Context, string) (model.Operation, error) {
	return model.Operation{}, nil
}

type ownershipClock struct{}

func (ownershipClock) Now() time.Time                            { return time.Unix(100, 0).UTC() }
func (ownershipClock) Wait(context.Context, time.Duration) error { return nil }

func TestExistingCFAppBlocksCreatePushAndSurvivesDelete(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	workRoot := t.TempDir()
	bitsPath := filepath.Join(workRoot, "demo")
	if err := os.Mkdir(bitsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	invitePath := filepath.Join(workRoot, "invite")
	if err := os.WriteFile(invitePath, []byte("invite"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{items: map[string]model.Sandbox{}}
	run := &ownershipRunner{}
	provider := cf.Provider{Run: run, Buildpacks: []string{"ruby_buildpack"}, WorkRoot: workRoot}
	r := reconcile.New(
		reconcile.Config{WorkRoot: workRoot, IdentityDomain: "identity.example", ManagerRouteHost: "manager", ManagerPackHost: "manager.identity.example", ManagerAppGUID: "11111111-1111-4111-8111-111111111111"},
		store, ownershipRuntime{path: bitsPath}, provider, ownershipPack{ownershipEnrollment{path: invitePath}}, ownershipProbe{}, ownershipClock{},
	)
	h := newTestHandler(t, store, r, collie)
	defer func() { _ = h.Close(context.Background()) }()

	created := request(t, h, http.MethodPost, "/manager/api/sandboxes", `{"name":"demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack"}`, "public.example", true)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	waitForSandbox(t, store, "demo", func(s model.Sandbox) bool { return s.Phase == model.PhaseFailed })

	deleted := request(t, h, http.MethodDelete, "/manager/api/sandboxes/demo", "", "public.example", true)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d", deleted.Code)
	}
	got := waitForSandbox(t, store, "demo", func(s model.Sandbox) bool {
		return s.Phase == model.PhaseFailed && strings.Contains(s.LastError, "ownership unknown")
	})
	if got.AppGUID != "" {
		t.Fatalf("preexisting app GUID was adopted: %#v", got)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	for _, command := range run.commands {
		if len(command) > 1 && (command[1] == "push" || command[1] == "delete" || command[1] == "unmap-route") {
			t.Fatalf("preexisting app was mutated: %#v", run.commands)
		}
	}
}

func waitForSandbox(t *testing.T, store *memoryStore, name string, ready func(model.Sandbox) bool) model.Sandbox {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if value, ok := store.Get(name); ok && ready(value) {
			return value
		}
		time.Sleep(time.Millisecond)
	}
	value, _ := store.Get(name)
	t.Fatalf("sandbox did not reach expected state: %#v", value)
	return model.Sandbox{}
}

func TestAPIValidationAndSortedGET(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	store := &memoryStore{items: map[string]model.Sandbox{
		"z": {Name: "z", CreatedAt: time.Unix(2, 0)}, "a": {Name: "a", CreatedAt: time.Unix(1, 0)},
	}}
	h := newTestHandler(t, store, &fakeReconciler{make(chan string, 8), make(chan string, 8)}, collie)
	got := request(t, h, http.MethodGet, "/manager/api/sandboxes", "", "public.example", true)
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("GET = %d %q", got.Code, got.Header().Get("Content-Type"))
	}
	var views []SandboxView
	if err := json.Unmarshal(got.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Name != "a" {
		t.Fatalf("views = %#v", views)
	}

	tests := []struct {
		name, body  string
		contentType bool
	}{
		{"bad name", `{"name":"Demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack"}`, true},
		{"bad repository", `{"name":"demo","repository":"file:///etc/passwd","buildpack":"ruby_buildpack"}`, true},
		{"HTTP repository credentials", `{"name":"demo","repository":"https://user:password@git.example/demo.git","buildpack":"ruby_buildpack"}`, true},
		{"SSH repository userinfo", `{"name":"demo","repository":"ssh://git@git.example/demo.git","buildpack":"ruby_buildpack"}`, true},
		{"repository query", `{"name":"demo","repository":"https://git.example/demo.git?token=secret","buildpack":"ruby_buildpack"}`, true},
		{"repository fragment", `{"name":"demo","repository":"ssh://git.example/demo.git#secret","buildpack":"ruby_buildpack"}`, true},
		{"repository option", `{"name":"demo","repository":"--upload-pack=x","buildpack":"ruby_buildpack"}`, true},
		{"bad buildpack", `{"name":"demo","repository":"ssh://git@git.example/demo.git","buildpack":"evil"}`, true},
		{"unknown field", `{"name":"demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack","token":"x"}`, true},
		{"trailing JSON", `{"name":"demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack"}{}`, true},
		{"wrong content type", `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/manager/api/sandboxes", strings.NewReader(tt.body))
			r.Host = "public.example"
			r.Header.Set("Authorization", "Bearer test-token")
			if tt.contentType {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest && w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
		})
	}

	large := `{"name":"demo","repository":"https://git.example/` + strings.Repeat("x", 70<<10) + `.git","buildpack":"ruby_buildpack"}`
	if got := request(t, h, http.MethodPost, "/manager/api/sandboxes", large, "public.example", true).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status = %d", got)
	}
}

func TestCollieAndPackProxyRouting(t *testing.T) {
	collie := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Collie", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.Copy(w, r.Body)
		_, _ = w.Write([]byte("|" + r.URL.Path + "?" + r.URL.RawQuery))
	}))
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)

	if got := request(t, h, http.MethodGet, "/collie/assets/app.js", "", "public.example", false).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized Collie status = %d", got)
	}
	w := request(t, h, http.MethodPost, "/collie/assets/app.js?v=1", "body", "public.example", true)
	if w.Code != http.StatusCreated || w.Header().Get("X-Collie") != "yes" || w.Body.String() != "body|/assets/app.js?v=1" {
		t.Fatalf("collie proxy = %d %q", w.Code, w.Body.String())
	}
	if got := request(t, h, http.MethodGet, "/manager/missing", "", "public.example", false).Header().Get("X-Collie"); got != "" {
		t.Fatal("manager path was proxied")
	}
	root := request(t, h, http.MethodGet, "/", "", "public.example", false)
	if root.Code != http.StatusTemporaryRedirect || root.Header().Get("Location") != "/manager/" {
		t.Fatalf("root = %d %q", root.Code, root.Header().Get("Location"))
	}

	combos := []struct {
		host, path string
		want       int
	}{
		{"pack.identity.example", "/pack/v1/hello", http.StatusCreated},
		{"PACK.IDENTITY.EXAMPLE.", "/pack/v1/hello", http.StatusCreated},
		{"pack.identity.example.identity.example", "/pack/v1/hello", http.StatusNotFound},
		{"pack.identity.example:443", "/other", http.StatusNotFound},
		{"public.example", "/pack/v1/hello", http.StatusNotFound},
		{"public.example", "/collie/pack/v1/hello", http.StatusUnauthorized},
	}
	for _, tt := range combos {
		w := request(t, h, http.MethodGet, tt.path, "", tt.host, false)
		if w.Code != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.host, tt.path, w.Code, tt.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/pack/v1/hello", nil)
	r.Host = "public.example"
	r.Header.Set("X-Forwarded-Host", "pack.identity.example")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("spoofed forwarded host status = %d", w2.Code)
	}
}

func TestProxyStripsGatewayCredentialsWithoutRemovingPackAuthorization(t *testing.T) {
	received := make(chan http.Header, 2)
	collie := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer collie.Close()
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, &fakeReconciler{make(chan string, 1), make(chan string, 1)}, collie)

	browser := httptest.NewRequest(http.MethodGet, "/collie/assets/app.js", nil)
	browser.Host = "public.example"
	browser.Header.Set("Authorization", "Bearer test-token")
	browser.Header.Set("Cookie", "manager_session=secret; preference=compact")
	browser.Header.Set("Proxy-Authorization", "Basic proxy-secret")
	browser.Header.Set("X-Manager-Authorization", "manager-secret")
	browser.Header.Set("X-Safe-Preference", "compact")
	h.ServeHTTP(httptest.NewRecorder(), browser)
	browserHeaders := <-received
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Manager-Authorization"} {
		if got := browserHeaders.Get(name); got != "" {
			t.Errorf("Collie received %s = %q", name, got)
		}
	}
	if got := browserHeaders.Get("X-Safe-Preference"); got != "compact" {
		t.Errorf("safe browser header = %q", got)
	}

	pack := httptest.NewRequest(http.MethodPost, "/pack/v1/enroll", nil)
	pack.Host = "pack.identity.example"
	pack.Header.Set("Authorization", "Pack signature-value")
	pack.Header.Set("Cookie", "manager_session=secret")
	pack.Header.Set("Proxy-Authorization", "Basic proxy-secret")
	pack.Header.Set("X-Manager-Authorization", "manager-secret")
	pack.Header.Set("X-Pack-Protocol", "v1")
	h.ServeHTTP(httptest.NewRecorder(), pack)
	packHeaders := <-received
	if got := packHeaders.Get("Authorization"); got != "Pack signature-value" {
		t.Errorf("Pack authorization = %q", got)
	}
	for _, name := range []string{"Cookie", "Proxy-Authorization", "X-Manager-Authorization"} {
		if got := packHeaders.Get(name); got != "" {
			t.Errorf("Pack received %s = %q", name, got)
		}
	}
	if got := packHeaders.Get("X-Pack-Protocol"); got != "v1" {
		t.Errorf("Pack protocol header = %q", got)
	}
}

func TestBearerAuthorizer(t *testing.T) {
	authorize := BearerAuthorizer("secret")
	for _, header := range []string{"", "Basic secret", "Bearer wrong", "bearer secret"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", header)
		if authorize(r) {
			t.Fatalf("authorized %q", header)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/", bytes.NewReader(nil))
	r.Header.Set("Authorization", "Bearer secret")
	if !authorize(r) {
		t.Fatal("valid bearer token rejected")
	}
}

func TestCloseCancelsAndWaitsForTrackedReconcile(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	started := make(chan struct{})
	finished := make(chan struct{})
	reconciler := &blockingReconciler{reconcile: func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}}
	errorsSeen := make(chan error, 1)
	u, _ := url.Parse(collie.URL)
	h, err := New(Config{Store: &memoryStore{items: map[string]model.Sandbox{}}, Reconciler: reconciler, Buildpacks: []string{"ruby_buildpack"}, CollieURL: u, ManagerPackHost: "pack.identity.example", ManagerToken: "test-token", Healthy: func() bool { return true }, ErrorSink: func(err error) { errorsSeen <- err }})
	if err != nil {
		t.Fatal(err)
	}
	created := request(t, h, http.MethodPost, "/manager/api/sandboxes", `{"name":"demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack"}`, "public.example", true)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d", created.Code)
	}
	<-started
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Close returned before reconcile finished")
	}
	select {
	case err := <-errorsSeen:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("reported error = %v", err)
		}
	default:
		t.Fatal("reconcile error was discarded")
	}

	closed := request(t, h, http.MethodPost, "/manager/api/sandboxes", `{"name":"other","repository":"https://git.example/other.git","buildpack":"ruby_buildpack"}`, "public.example", true)
	if closed.Code != http.StatusServiceUnavailable {
		t.Fatalf("enqueue after Close status = %d", closed.Code)
	}
}

func TestCloseHonorsContextWhileWaiting(t *testing.T) {
	collie := httptest.NewServer(http.NotFoundHandler())
	defer collie.Close()
	block := make(chan struct{})
	started := make(chan struct{})
	reconciler := &blockingReconciler{reconcile: func(context.Context, string) error { close(started); <-block; return nil }}
	h := newTestHandler(t, &memoryStore{items: map[string]model.Sandbox{}}, reconciler, collie)
	_ = request(t, h, http.MethodPost, "/manager/api/sandboxes", `{"name":"demo","repository":"https://git.example/demo.git","buildpack":"ruby_buildpack"}`, "public.example", true)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v", err)
	}
	close(block)
}
