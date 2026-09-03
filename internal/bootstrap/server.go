package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var memberPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var secretPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer\s+)?|\b(?:token|secret|password)\s*[=:]\s*)\S+`)

type Config struct{ Executable, TokenPath, ReadyPath, TrustStorePath, LeadAddress, MemberID string }
type Joiner interface {
	Join(context.Context, []string, io.Reader) error
}
type Server struct {
	config Config
	joiner Joiner
	log    io.Writer
	mu     sync.Mutex
}

// ServeHTTP intentionally has no browser authentication. The identity-aware
// route and manager-source policy are the authorization boundary.

func New(config Config, joiner Joiner, log io.Writer) (*Server, error) {
	if config.Executable == "" || config.TokenPath == "" || config.ReadyPath == "" || config.TrustStorePath == "" || joiner == nil {
		return nil, errors.New("bootstrap paths, executable, and joiner are required")
	}
	parsed, err := url.Parse(config.LeadAddress)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid Pack lead address")
	}
	if !memberPattern.MatchString(config.MemberID) {
		return nil, errors.New("invalid sandbox member ID")
	}
	if log == nil {
		log = io.Discard
	}
	return &Server{config: config, joiner: joiner, log: log}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/bootstrap/health":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	case "/bootstrap/join":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.join(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.config.ReadyPath); err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		s.fail(w, err)
		return
	}
	if complete, err := regularFile(s.config.TrustStorePath); err != nil {
		s.fail(w, err)
		return
	} else if complete {
		if err = writeMarker(s.config.ReadyPath); err != nil {
			s.fail(w, err)
			return
		}
		_ = os.Remove(s.config.TokenPath)
		writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
		return
	}
	token, err := os.Open(s.config.TokenPath)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer token.Close()
	args := []string{"pack", "join", s.config.LeadAddress, "-", "--label", s.config.MemberID}
	if err = s.joiner.Join(r.Context(), args, token); err != nil {
		s.fail(w, err)
		return
	}
	if err = writeMarker(s.config.ReadyPath); err != nil {
		s.fail(w, err)
		return
	}
	if err = os.Remove(s.config.TokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(s.log, sanitize(err.Error()))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
}
func regularFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("Pack trust store must be a regular file")
	}
	return true, nil
}
func writeMarker(path string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".bootstrap-ready-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.WriteString("ready\n")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (s *Server) fail(w http.ResponseWriter, err error) {
	message := sanitize(err.Error())
	fmt.Fprintln(s.log, message)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func sanitize(value string) string {
	value = secretPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	if len(value) > 1024 {
		return value[:1024]
	}
	return value
}
