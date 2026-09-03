package httpapi

import (
	"context"
	"crypto/subtle"
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
	"time"

	"cf-herdr-poc/internal/model"
)

const maxRequestBody = 64 << 10

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
	Store           Store
	Reconciler      Reconciler
	Buildpacks      []string
	CollieURL       *url.URL
	ManagerPackHost string
	Authorize       func(*http.Request) bool
	Web             http.Handler
	Now             func() time.Time
}

type server struct {
	config Config
	collie *httputil.ReverseProxy
}

func New(config Config) (http.Handler, error) {
	if config.Store == nil || config.Reconciler == nil || config.CollieURL == nil || config.Authorize == nil {
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
	return &server{config: config, collie: httputil.NewSingleHostReverseProxy(config.CollieURL)}, nil
}

func BearerAuthorizer(token string) func(*http.Request) bool {
	want := []byte("Bearer " + token)
	return func(request *http.Request) bool {
		got := []byte(request.Header.Get("Authorization"))
		return len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, ok := canonicalHost(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if strings.EqualFold(host, s.config.ManagerPackHost) {
		if strings.HasPrefix(r.URL.Path, "/pack/v1/") {
			s.collie.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	switch {
	case r.URL.Path == "/":
		http.Redirect(w, r, "/manager/", http.StatusTemporaryRedirect)
	case r.URL.Path == "/manager/healthz" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/manager/api/sandboxes" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.authorized(w, r, s.sandboxes)
	case strings.HasPrefix(r.URL.Path, "/manager/api/sandboxes/"):
		s.authorized(w, r, s.sandbox)
	case strings.HasPrefix(r.URL.Path, "/collie/"):
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/collie")
		s.collie.ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/pack/v1/"):
		http.NotFound(w, r)
	case strings.HasPrefix(r.URL.Path, "/manager/") && s.config.Web != nil:
		s.config.Web.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) authorized(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if !s.config.Authorize(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="manager"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	next(w, r)
}

func (s *server) sandboxes(w http.ResponseWriter, r *http.Request) {
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
	go func() { _ = s.config.Reconciler.ReconcileOne(context.Background(), input.Name) }()
	writeJSON(w, http.StatusAccepted, PublicSandbox(sandbox))
}

func (s *server) sandbox(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/manager/api/sandboxes/")
	name := remainder
	retry := false
	if strings.HasSuffix(remainder, "/retry") {
		name = strings.TrimSuffix(remainder, "/retry")
		retry = true
	}
	if !validName(name) || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.config.Store.Get(name); !ok {
		http.NotFound(w, r)
		return
	}
	if retry && r.Method == http.MethodPost {
		go func() { _ = s.config.Reconciler.Retry(context.Background(), name) }()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !retry && r.Method == http.MethodDelete {
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
		go func() { _ = s.config.Reconciler.ReconcileOne(context.Background(), name) }()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Allow", "POST, DELETE")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "ssh") && parsed.Host != "" && parsed.User == nil || err == nil && parsed.Scheme == "ssh" && parsed.Host != ""
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
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() || strings.EqualFold(host, "localhost")
}
