package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cf-herdr-poc/internal/model"
)

const (
	maxRequestBody = 64 << 10
	sessionCookie  = "manager_session"
	sessionContext = "cf-herdr-poc manager browser session v1"
)

type Store interface {
	List() []model.Sandbox
	Get(string) (model.Sandbox, bool)
	Create(model.Sandbox) error
	Update(string, func(*model.Sandbox) error) error
}

type Reconciler interface {
	ReconcileOne(context.Context, string) error
	Retry(context.Context, string) error
}

type Config struct {
	Store               Store
	Reconciler          Reconciler
	Buildpacks          []string
	CollieURL           *url.URL
	ManagerPackHost     string
	ManagerToken        string
	TrustForwardedProto bool
	Healthy             func() bool
	ErrorSink           func(error)
	Web                 http.Handler
	Now                 func() time.Time
}

type Handler struct {
	config       Config
	collie       *httputil.ReverseProxy
	pack         *httputil.ReverseProxy
	sessionValue string
	ctx          context.Context
	cancel       context.CancelFunc
	jobsMu       sync.Mutex
	jobs         sync.WaitGroup
	closed       bool
}

func New(config Config) (*Handler, error) {
	if config.Store == nil || config.Reconciler == nil || config.CollieURL == nil || config.ManagerToken == "" || config.Healthy == nil || config.ErrorSink == nil {
		return nil, errors.New("HTTP API dependencies are required")
	}
	if config.CollieURL.Scheme != "http" || !isLoopback(config.CollieURL.Hostname()) {
		return nil, errors.New("Collie URL must use HTTP on loopback")
	}
	if !validHostname(config.ManagerPackHost) {
		return nil, errors.New("manager Pack host is invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{config: config, collie: newCollieProxy(config.CollieURL, true), pack: newCollieProxy(config.CollieURL, false), sessionValue: deriveSession(config.ManagerToken), ctx: ctx, cancel: cancel}, nil
}

func newCollieProxy(target *url.URL, stripAuthorization bool) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	direct := proxy.Director
	proxy.Director = func(request *http.Request) {
		direct(request)
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		if stripAuthorization {
			request.Header.Del("Authorization")
		}
		for name := range request.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-manager-") && (strings.Contains(lower, "auth") || strings.Contains(lower, "token") || strings.Contains(lower, "session") || strings.Contains(lower, "cookie") || strings.Contains(lower, "credential")) {
				request.Header.Del(name)
			}
		}
	}
	return proxy
}

func BearerAuthorizer(token string) func(*http.Request) bool {
	want := []byte("Bearer " + token)
	return func(request *http.Request) bool {
		got := []byte(request.Header.Get("Authorization"))
		return len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
	}
}

func deriveSession(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(sessionContext))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, ok := canonicalHost(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if strings.EqualFold(host, s.config.ManagerPackHost) {
		if strings.HasPrefix(r.URL.Path, "/pack/v1/") {
			s.pack.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/manager/api" || strings.HasPrefix(r.URL.Path, "/manager/api/") {
		if r.URL.Path == "/manager/api/session" {
			s.session(w, r)
			return
		}
		s.authorized(w, r, s.api)
		return
	}
	switch {
	case r.URL.Path == "/":
		http.Redirect(w, r, "/manager/", http.StatusTemporaryRedirect)
	case r.URL.Path == "/manager/healthz" && r.Method == http.MethodGet:
		if !s.config.Healthy() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case strings.HasPrefix(r.URL.Path, "/collie/"):
		s.authorized(w, r, func(w http.ResponseWriter, r *http.Request) {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/collie")
			s.collie.ServeHTTP(w, r)
		})
	case strings.HasPrefix(r.URL.Path, "/pack/v1/"):
		http.NotFound(w, r)
	case strings.HasPrefix(r.URL.Path, "/manager/") && s.config.Web != nil:
		s.config.Web.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Handler) authorized(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if !s.authorize(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="manager"`)
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	next(w, r)
}

func (s *Handler) authorize(r *http.Request) bool {
	if BearerAuthorizer(s.config.ManagerToken)(r) {
		return true
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(cookie.Value), []byte(s.sessionValue))
}

func (s *Handler) session(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, DELETE")
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !hasJSONContentType(r) {
		writeAPIError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var input struct {
		Token string `json:"token"`
	}
	if err := decoder.Decode(&input); err != nil || requireEOF(decoder) != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	want, got := []byte(s.config.ManagerToken), []byte(input.Token)
	if len(want) != len(got) || subtle.ConstantTimeCompare(want, got) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.TLS == nil && !(s.config.TrustForwardedProto && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		writeAPIError(w, http.StatusBadRequest, "HTTPS required")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.sessionValue, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Handler) enqueue(run func(context.Context) error) bool {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.closed {
		return false
	}
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		if err := run(s.ctx); err != nil {
			s.config.ErrorSink(err)
		}
	}()
	return true
}

func (s *Handler) acceptingJobs() bool {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	return !s.closed
}

func (s *Handler) Close(ctx context.Context) error {
	s.jobsMu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	s.jobsMu.Unlock()
	done := make(chan struct{})
	go func() { s.jobs.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Handler) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/manager/api/sandboxes":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.sandboxes(w, r)
	case strings.HasPrefix(r.URL.Path, "/manager/api/sandboxes/"):
		s.sandbox(w, r)
	default:
		writeAPIError(w, http.StatusNotFound, "not found")
	}
}

func (s *Handler) sandboxes(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		items := s.config.Store.List()
		sort.Slice(items, func(i, j int) bool {
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].Name < items[j].Name
			}
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		})
		views := make([]SandboxView, len(items))
		for i := range items {
			views[i] = PublicSandbox(items[i])
		}
		writeJSON(w, http.StatusOK, views)
		return
	}
	if !hasJSONContentType(r) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if !s.acceptingJobs() {
		writeAPIError(w, http.StatusServiceUnavailable, "shutting down")
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var input struct {
		Name       string `json:"name"`
		Repository string `json:"repository"`
		Buildpack  string `json:"buildpack"`
	}
	if err := decoder.Decode(&input); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
		}
		return
	}
	if err := requireEOF(decoder); err != nil {
		http.Error(w, "invalid trailing JSON", http.StatusBadRequest)
		return
	}
	if err := validateCreate(input.Name, input.Repository, input.Buildpack, s.config.Buildpacks); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, exists := s.config.Store.Get(input.Name); exists {
		http.Error(w, "sandbox already exists", http.StatusConflict)
		return
	}
	now := s.config.Now()
	sandbox := model.Sandbox{Name: input.Name, Repository: input.Repository, Buildpack: input.Buildpack, Desired: model.DesiredPresent, Phase: model.PhaseCreating, CreatedAt: now, UpdatedAt: now}
	if err := s.config.Store.Create(sandbox); err != nil {
		if _, exists := s.config.Store.Get(input.Name); exists {
			http.Error(w, "sandbox already exists", http.StatusConflict)
		} else {
			http.Error(w, "persist sandbox", http.StatusInternalServerError)
		}
		return
	}
	if !s.enqueue(func(ctx context.Context) error { return s.config.Reconciler.ReconcileOne(ctx, input.Name) }) {
		writeAPIError(w, http.StatusServiceUnavailable, "shutting down")
		return
	}
	writeJSON(w, http.StatusAccepted, PublicSandbox(sandbox))
}

func (s *Handler) sandbox(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/manager/api/sandboxes/")
	name := remainder
	retry := false
	if strings.HasSuffix(remainder, "/retry") {
		name = strings.TrimSuffix(remainder, "/retry")
		retry = true
	}
	if !validName(name) || strings.Contains(name, "/") {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if _, ok := s.config.Store.Get(name); !ok {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if retry && r.Method == http.MethodPost {
		if !s.acceptingJobs() {
			writeAPIError(w, http.StatusServiceUnavailable, "shutting down")
			return
		}
		if !s.enqueue(func(ctx context.Context) error { return s.config.Reconciler.Retry(ctx, name) }) {
			writeAPIError(w, http.StatusServiceUnavailable, "shutting down")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !retry && r.Method == http.MethodDelete {
		if !s.acceptingJobs() {
			writeAPIError(w, http.StatusServiceUnavailable, "shutting down")
			return
		}
		now := s.config.Now()
		if err := s.config.Store.Update(name, func(value *model.Sandbox) error {
			value.Desired = model.DesiredDeleted
			value.Phase = model.PhaseDeleting
			value.UpdatedAt = now
			return nil
		}); err != nil {
			http.Error(w, "persist deletion", http.StatusInternalServerError)
			return
		}
		if !s.enqueue(func(ctx context.Context) error { return s.config.Reconciler.ReconcileOne(ctx, name) }) {
			writeAPIError(w, http.StatusServiceUnavailable, "shutting down")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Allow", "POST, DELETE")
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func validName(value string) bool { return namePattern.MatchString(value) }
func validateCreate(name, repository, buildpack string, buildpacks []string) error {
	if !validName(name) {
		return errors.New("invalid sandbox name")
	}
	if !validRepository(repository) {
		return errors.New("invalid repository")
	}
	for _, allowed := range buildpacks {
		if buildpack == allowed {
			return nil
		}
	}
	return errors.New("buildpack is not allowed")
}
func validRepository(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	if strings.HasPrefix(value, "git@") {
		parts := strings.SplitN(value, ":", 2)
		return len(parts) == 2 && validHostname(strings.TrimPrefix(parts[0], "git@")) && parts[1] != ""
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "ssh") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}
func validHostname(value string) bool {
	if value == "" || strings.ContainsAny(value, "/@?#") {
		return false
	}
	host, ok := canonicalHost(value)
	return ok && host == value && net.ParseIP(host) == nil
}
func canonicalHost(value string) (string, bool) {
	if strings.Contains(value, " ") || value == "" {
		return "", false
	}
	host := value
	if strings.HasPrefix(value, "[") || strings.Count(value, ":") == 1 {
		if parsed, _, err := net.SplitHostPort(value); err == nil {
			host = parsed
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host, host != "" && !strings.ContainsAny(host, "/@?#")
}
func hasJSONContentType(r *http.Request) bool {
	value, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && value == "application/json"
}
func requireEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON")
	}
	return err
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() || strings.EqualFold(host, "localhost")
}
