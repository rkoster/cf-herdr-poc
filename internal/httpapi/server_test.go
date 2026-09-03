package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"cf-herdr-poc/internal/model"
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

func newTestHandler(t *testing.T, store *memoryStore, reconciler *fakeReconciler, collie *httptest.Server) http.Handler {
	return newTestHandlerWithHealth(t, store, reconciler, collie, func() bool { return true })
}

func newTestHandlerWithHealth(t *testing.T, store *memoryStore, reconciler *fakeReconciler, collie *httptest.Server, healthy func() bool) http.Handler {
	t.Helper()
	u, err := url.Parse(collie.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Store: store, Reconciler: reconciler, Buildpacks: []string{"ruby_buildpack"}, CollieURL: u, ManagerPackHost: "pack.identity.example", Authorize: BearerAuthorizer("test-token"), Healthy: healthy, Now: func() time.Time { return time.Unix(100, 0).UTC() }})
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

	w := request(t, h, http.MethodPost, "/collie/assets/app.js?v=1", "body", "public.example", false)
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
		{"pack.identity.example:443", "/other", http.StatusNotFound},
		{"public.example", "/pack/v1/hello", http.StatusNotFound},
		{"public.example", "/collie/pack/v1/hello", http.StatusCreated},
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
