package cf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cf-herdr-poc/internal/model"
	"cf-herdr-poc/internal/runner"
)

const (
	maxOperationText         = 1024
	maxOperationSummaryBytes = 4 * 1024
)

var (
	namePattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	uuidPattern      = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ansiPattern      = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	bearerValue      = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?)([^"'\s,}]+)`)
	standaloneBearer = regexp.MustCompile(`(?i)(\bbearer\s+)([^"'\s,}]+)`)
	jsonSecret       = regexp.MustCompile(`(?i)("(?:[^"\\]*(?:token|secret|password|private_key|instance_key|instance_cert|authorization)[^"\\]*)"\s*:\s*)("(?:\\.|[^"\\])*"|[^,}\s]+)`)
	shellSecret      = regexp.MustCompile(`(?i)(\b(?:export\s+)?[a-z0-9_]*(?:token|secret|password|private_key|instance_key|instance_cert|authorization)[a-z0-9_]*\s*=\s*)("(?:\\.|[^"\\])*"|'[^']*'|[^\s,}]+)`)
	pemBlock         = regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`)
)

type PushRequest struct {
	Name            string
	ExpectedAppGUID string
	Buildpack       string
	BitsPath        string
}

type RouteRequest struct {
	AppName       string
	AppGUID       string
	Domain        string
	Host          string
	SourceAppGUID string
}

type RoutePolicyRequest struct {
	Domain        string
	Host          string
	SourceAppGUID string
}

type App struct {
	Name    string
	GUID    string
	State   string
	Running bool
	Ready   bool
}

type CloudFoundry interface {
	Stage(context.Context, PushRequest) (model.Operation, error)
	EnsureAppAbsent(context.Context, string) (model.Operation, error)
	AppGUID(context.Context, string) (string, model.Operation, error)
	ConfigureEnrollment(context.Context, string, string, string) (model.Operation, error)
	StartApp(context.Context, string) (model.Operation, error)
	InspectApp(context.Context, string) (App, error)
	SecureRoute(context.Context, RouteRequest) (model.Operation, error)
	AddRoutePolicy(context.Context, RoutePolicyRequest) (model.Operation, error)
	RemoveRoutePolicy(context.Context, RoutePolicyRequest) (model.Operation, error)
	RemoveRoute(context.Context, RouteRequest) (model.Operation, error)
	DeleteApp(context.Context, string, string) (model.Operation, error)
}

type Provider struct {
	Run         runner.Runner
	Executable  string
	Buildpacks  []string
	WorkRoot    string
	Environment []string
}

type Error struct {
	Operation string
	Kind      string
	Cause     error
}

func (e *Error) Error() string {
	if e.Operation == "stage" && e.Kind == "already_exists" {
		return "stage app name already exists"
	}
	if e.Operation == "stage" && e.Kind == "ownership_lost" {
		return "stage app ownership lost"
	}
	return e.Operation + " " + e.Kind
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) AlreadyExists() bool { return e.Kind == "already_exists" }
func (e *Error) Absent() bool        { return e.Kind == "not_found" }
func (e *Error) IdentityMismatch() bool {
	return e.Kind == "identity_mismatch" || e.Kind == "ownership_lost"
}

func (p Provider) Stage(ctx context.Context, request PushRequest) (model.Operation, error) {
	if err := validateName("app", request.Name); err != nil {
		return model.Operation{}, err
	}
	if !contains(p.Buildpacks, request.Buildpack) {
		return model.Operation{}, fmt.Errorf("buildpack is not allowed")
	}
	if err := validateBitsPath(p.WorkRoot, request.BitsPath); err != nil {
		return model.Operation{}, err
	}
	if request.ExpectedAppGUID != "" {
		if err := validateGUID(request.ExpectedAppGUID); err != nil {
			return model.Operation{}, err
		}
	}
	observation, output, err := p.execute(ctx, "stage-preflight", "app", request.Name, "--guid")
	if err != nil {
		if !isProviderAbsent(err) || request.ExpectedAppGUID == "" {
			if isProviderAbsent(err) {
				observation.Success = true
				observation.Error = ""
			} else {
				return observation, err
			}
		} else {
			observation.Name = "stage"
			observation.Error = "stage app ownership lost"
			return observation, &Error{Operation: "stage", Kind: "ownership_lost", Cause: err}
		}
	} else {
		guid := strings.TrimSpace(string(output))
		if err := validateGUID(guid); err != nil {
			observation.Success = false
			observation.Error = "cf app returned an invalid GUID"
			return observation, errors.New(observation.Error)
		}
		if request.ExpectedAppGUID == "" {
			observation.Success = false
			observation.Name = "stage"
			observation.Error = "stage app name already exists"
			return observation, &Error{Operation: "stage", Kind: "already_exists"}
		}
		if guid != request.ExpectedAppGUID {
			observation.Success = false
			observation.Name = "stage"
			observation.Error = "stage app identity mismatch"
			return observation, &Error{Operation: "stage", Kind: "identity_mismatch"}
		}
	}
	operation, _, err := p.execute(ctx, "stage", "push", request.Name, "--no-route", "--no-start", "-b", request.Buildpack, "-p", request.BitsPath, "-c", "./.sandbox/start.sh")
	return operation, err
}

func (p Provider) EnsureAppAbsent(ctx context.Context, name string) (model.Operation, error) {
	if err := validateName("app", name); err != nil {
		return model.Operation{}, err
	}
	operation, output, err := p.execute(ctx, "app-absence", "app", name, "--guid")
	if err != nil {
		if isProviderAbsent(err) {
			operation.Success = true
			operation.Error = ""
			return operation, nil
		}
		return operation, err
	}
	if err := validateGUID(strings.TrimSpace(string(output))); err != nil {
		operation.Success = false
		operation.Error = "cf app returned an invalid GUID"
		return operation, errors.New(operation.Error)
	}
	operation.Success = false
	operation.Error = "app name already exists"
	return operation, &Error{Operation: "app-absence", Kind: "already_exists"}
}

func (p Provider) ConfigureEnrollment(ctx context.Context, name, tokenAppPath, leadAddress string) (model.Operation, error) {
	if err := validateName("app", name); err != nil {
		return model.Operation{}, err
	}
	if tokenAppPath != "/home/vcap/app/.sandbox/join-token" || !validHTTPSAddress(leadAddress) {
		return model.Operation{}, fmt.Errorf("invalid enrollment configuration")
	}
	return p.executeMany(ctx, "configure-enrollment", [][]string{{"set-env", name, "COLLIE_JOIN_TOKEN_FILE", tokenAppPath}, {"set-env", name, "COLLIE_PACK_LEAD_ADDRESS", leadAddress}, {"set-env", name, "SANDBOX_MEMBER_ID", name}})
}

func (p Provider) StartApp(ctx context.Context, name string) (model.Operation, error) {
	if err := validateName("app", name); err != nil {
		return model.Operation{}, err
	}
	operation, _, err := p.execute(ctx, "start-app", "start", name)
	return operation, err
}

func (p Provider) AppGUID(ctx context.Context, name string) (string, model.Operation, error) {
	if err := validateName("app", name); err != nil {
		return "", model.Operation{}, err
	}
	operation, output, err := p.execute(ctx, "app-guid", "app", name, "--guid")
	if err != nil {
		return "", operation, err
	}
	guid := strings.TrimSpace(string(output))
	if err := validateGUID(guid); err != nil {
		operation.Success = false
		operation.Error = "cf app returned an invalid GUID"
		return "", operation, errors.New(operation.Error)
	}
	return guid, operation, nil
}

func (p Provider) SecureRoute(ctx context.Context, request RouteRequest) (model.Operation, error) {
	if err := validateRouteRequest(request); err != nil {
		return model.Operation{}, err
	}
	commands := [][]string{
		{"create-route", request.Domain, "--hostname", request.Host},
		{"map-route", request.AppName, request.Domain, "--hostname", request.Host},
		{"add-route-policy", request.Domain, "--hostname", request.Host, "--source", "cf:app:" + request.SourceAppGUID},
	}
	return p.executeMany(ctx, "secure-route", commands)
}

func (p Provider) AddRoutePolicy(ctx context.Context, request RoutePolicyRequest) (model.Operation, error) {
	if err := validateRoutePolicy(request); err != nil {
		return model.Operation{}, err
	}
	operation, _, err := p.execute(ctx, "add-route-policy", "add-route-policy", request.Domain, "--hostname", request.Host, "--source", "cf:app:"+request.SourceAppGUID)
	return operation, err
}

func (p Provider) RemoveRoutePolicy(ctx context.Context, request RoutePolicyRequest) (model.Operation, error) {
	if err := validateRoutePolicy(request); err != nil {
		return model.Operation{}, err
	}
	operation, _, err := p.execute(ctx, "remove-route-policy", "remove-route-policy", request.Domain, "--hostname", request.Host, "--source", "cf:app:"+request.SourceAppGUID)
	return operation, err
}

func (p Provider) RemoveRoute(ctx context.Context, request RouteRequest) (model.Operation, error) {
	if err := validateRouteRequest(request); err != nil {
		return model.Operation{}, err
	}
	result, err := p.executeMany(ctx, "remove-route", [][]string{{"remove-route-policy", request.Domain, "--hostname", request.Host, "--source", "cf:app:" + request.SourceAppGUID}})
	if err != nil {
		return result, err
	}
	if request.AppGUID == "" {
		cleanup, cleanupErr := p.executeMany(ctx, "remove-route", [][]string{{"delete-route", request.Domain, "--hostname", request.Host, "-f"}})
		return mergeOperations(result, cleanup), cleanupErr
	}
	identity, output, identityErr := p.execute(ctx, "remove-route", "app", request.AppName, "--guid")
	result = mergeOperations(result, identity)
	matched := identityErr == nil && strings.TrimSpace(string(output)) == request.AppGUID
	mismatch := identityErr == nil && !matched
	if identityErr != nil && !isProviderAbsent(identityErr) {
		return result, identityErr
	}
	if isProviderAbsent(identityErr) {
		result.Success = true
		result.Error = ""
	}
	commands := [][]string{}
	if matched {
		commands = append(commands, []string{"unmap-route", request.AppName, request.Domain, "--hostname", request.Host})
	}
	commands = append(commands, []string{"delete-route", request.Domain, "--hostname", request.Host, "-f"})
	cleanup, cleanupErr := p.executeMany(ctx, "remove-route", commands)
	result = mergeOperations(result, cleanup)
	if cleanupErr != nil {
		return result, cleanupErr
	}
	if mismatch {
		result.Success = false
		result.Error = "remove-route identity mismatch"
		return result, &Error{Operation: "remove-route", Kind: "identity_mismatch"}
	}
	return result, nil
}

func (p Provider) DeleteApp(ctx context.Context, name, expectedGUID string) (model.Operation, error) {
	if err := validateName("app", name); err != nil {
		return model.Operation{}, err
	}
	if err := validateGUID(expectedGUID); err != nil {
		return model.Operation{}, err
	}
	operation, output, err := p.execute(ctx, "delete-app", "app", name, "--guid")
	if err != nil {
		if isProviderAbsent(err) {
			operation.Success = true
			operation.Error = ""
			return operation, nil
		}
		return operation, err
	}
	if strings.TrimSpace(string(output)) != expectedGUID {
		operation.Success = false
		operation.Error = "delete-app identity mismatch"
		return operation, &Error{Operation: "delete-app", Kind: "identity_mismatch"}
	}
	deleted, _, err := p.execute(ctx, "delete-app", "delete", name, "-f")
	operation = mergeOperations(operation, deleted)
	return operation, err
}

func (p Provider) InspectApp(ctx context.Context, guid string) (App, error) {
	if err := validateGUID(guid); err != nil {
		return App{}, err
	}
	_, output, err := p.execute(ctx, "inspect-processes", "curl", fmt.Sprintf("/v3/apps/%s/processes", url.PathEscape(guid)))
	if err != nil {
		return App{}, err
	}
	var processes struct {
		Resources []struct {
			GUID      string `json:"guid"`
			Type      string `json:"type"`
			Instances *int   `json:"instances"`
		} `json:"resources"`
	}
	if err := decodeJSON(output, &processes); err != nil {
		return App{}, fmt.Errorf("decode processes JSON: %w", err)
	}
	app := App{GUID: guid, Running: true, Ready: true}
	webProcesses := 0
	for _, process := range processes.Resources {
		if process.Type != "web" {
			continue
		}
		webProcesses++
		if err := validateGUID(process.GUID); err != nil {
			return App{}, fmt.Errorf("decode processes JSON: invalid process GUID")
		}
		_, statsOutput, err := p.execute(ctx, "inspect-process-stats", "curl", fmt.Sprintf("/v3/processes/%s/stats", url.PathEscape(process.GUID)))
		if err != nil {
			return App{}, err
		}
		// CAPI GET /v3/processes/:guid/stats wraps instance stats in resources.
		var stats struct {
			Resources []struct {
				Type     string `json:"type"`
				Index    int    `json:"index"`
				State    string `json:"state"`
				Routable bool   `json:"routable"`
			} `json:"resources"`
		}
		if err := decodeJSON(statsOutput, &stats); err != nil {
			return App{}, fmt.Errorf("decode process stats JSON: %w", err)
		}
		if len(stats.Resources) == 0 {
			app.Running, app.Ready = false, false
		}
		if process.Instances != nil && len(stats.Resources) < *process.Instances {
			app.Ready = false
		}
		for _, instance := range stats.Resources {
			if instance.State != "RUNNING" {
				app.Running, app.Ready = false, false
			} else if !instance.Routable {
				app.Ready = false
			}
		}
	}
	if webProcesses == 0 {
		app.Running, app.Ready = false, false
	}
	return app, nil
}

func (p Provider) executeMany(ctx context.Context, name string, commands [][]string) (model.Operation, error) {
	started := time.Now().UTC()
	result := model.Operation{Name: name, StartedAt: started, Success: true}
	for _, command := range commands {
		operation, _, err := p.execute(ctx, name, command...)
		result.Command = bounded(strings.TrimSpace(result.Command + " ; " + operation.Command))
		result.Summary = appendSummary(result.Summary, operation.Summary)
		if err != nil {
			var providerErr *Error
			if errors.As(err, &providerErr) && ((name == "secure-route" && providerErr.AlreadyExists()) || (name == "remove-route" && providerErr.Absent())) {
				continue
			}
			result.Success = false
			result.Error = operation.Error
			result.Duration = time.Since(started)
			return result, err
		}
	}
	result.Duration = time.Since(started)
	return result, nil
}

func mergeOperations(first, second model.Operation) model.Operation {
	first.Command = bounded(strings.TrimSpace(first.Command + " ; " + second.Command))
	first.Summary = appendSummary(first.Summary, second.Summary)
	first.Duration += second.Duration
	first.Success = first.Success && second.Success
	if second.Error != "" {
		first.Error = second.Error
	}
	return first
}

func isProviderAbsent(err error) bool {
	var providerErr *Error
	return errors.As(err, &providerErr) && providerErr.Absent()
}

func isProviderAlreadyExists(err error) bool {
	var classified interface{ AlreadyExists() bool }
	return errors.As(err, &classified) && classified.AlreadyExists()
}

func (p Provider) execute(ctx context.Context, operationName string, args ...string) (model.Operation, []byte, error) {
	started := time.Now().UTC()
	executable := p.Executable
	if executable == "" {
		executable = "cf"
	}
	operation := model.Operation{Name: operationName, StartedAt: started, Command: commandDisplay(executable, args)}
	if p.Run == nil {
		operation.Duration = time.Since(started)
		operation.Error = "command runner is required"
		return operation, nil, errors.New(operation.Error)
	}
	var output []byte
	var err error
	if len(p.Environment) > 0 {
		if envRunner, ok := p.Run.(runner.EnvRunner); ok {
			output, err = envRunner.RunEnv(ctx, p.Environment, executable, args...)
		} else {
			operation.Duration = time.Since(started)
			operation.Error = "environment-capable command runner is required"
			return operation, nil, errors.New(operation.Error)
		}
	} else {
		output, err = p.Run.Run(ctx, executable, args...)
	}
	operation.Duration = time.Since(started)
	operation.Success = err == nil
	operation.Summary = sanitizeOutput(output)
	if err != nil {
		cause := err
		operation.Error = bounded(operationName + " failed")
		if ctxErr := ctx.Err(); ctxErr != nil {
			operation.Error = bounded(operationName + " canceled: " + ctxErr.Error())
			cause = ctxErr
		}
		return operation, output, &Error{Operation: operationName, Kind: classifyError(operationName, string(output)), Cause: cause}
	}
	return operation, output, nil
}

func classifyError(operation, output string) string {
	value := strings.ToLower(output)
	if operation == "secure-route" || operation == "add-route-policy" {
		if strings.Contains(value, "already exists") {
			return "already_exists"
		}
	}
	if operation == "delete-app" || operation == "remove-route" || operation == "remove-route-policy" || operation == "app-guid" || operation == "app-absence" || operation == "stage-preflight" {
		if strings.Contains(value, "not found") || strings.Contains(value, "does not exist") {
			return "not_found"
		}
	}
	return "failed"
}

func validHTTPSAddress(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func commandDisplay(name string, args []string) string {
	return bounded(strings.Join(append([]string{name}, args...), " "))
}

func bounded(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	if len(value) > maxOperationText {
		return value[:maxOperationText]
	}
	return value
}

func sanitizeOutput(output []byte) string {
	// This is known-pattern sanitization for CF command output, not permission
	// to pass arbitrary environment dumps into operation records.
	value := ansiPattern.ReplaceAllString(string(output), "")
	value = pemBlock.ReplaceAllString(value, "[REDACTED]")
	value = strings.Map(func(character rune) rune {
		switch character {
		case '\n', '\t':
			return character
		case '\r':
			return '\n'
		}
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, value)
	value = bearerValue.ReplaceAllString(value, "${1}[REDACTED]")
	value = standaloneBearer.ReplaceAllString(value, "${1}[REDACTED]")
	value = jsonSecret.ReplaceAllStringFunc(value, redactAssignment)
	value = shellSecret.ReplaceAllStringFunc(value, redactAssignment)
	value = strings.TrimSpace(value)
	return capSummary(value)
}

func appendSummary(existing, next string) string {
	return capSummary(strings.TrimSpace(existing + "\n" + next))
}

func redactAssignment(value string) string {
	separator := strings.IndexAny(value, "=:")
	if separator < 0 {
		return value
	}
	prefix := value[:separator+1]
	remainder := strings.TrimSpace(value[separator+1:])
	if len(remainder) >= 2 && (remainder[0] == '"' || remainder[0] == '\'') {
		return prefix + string(remainder[0]) + "[REDACTED]" + string(remainder[0])
	}
	return prefix + "[REDACTED]"
}

func capSummary(value string) string {
	if len(value) > maxOperationSummaryBytes {
		const marker = "[older output truncated]\n"
		value = marker + value[len(value)-(maxOperationSummaryBytes-len(marker)):]
	}
	return value
}

func validateRouteRequest(request RouteRequest) error {
	if err := validateName("app", request.AppName); err != nil {
		return err
	}
	if request.AppGUID != "" {
		if err := validateGUID(request.AppGUID); err != nil {
			return err
		}
	}
	return validateRoutePolicy(RoutePolicyRequest{Domain: request.Domain, Host: request.Host, SourceAppGUID: request.SourceAppGUID})
}

func validateRoutePolicy(request RoutePolicyRequest) error {
	if err := validateDomain(request.Domain); err != nil {
		return err
	}
	if err := validateName("host", request.Host); err != nil {
		return err
	}
	return validateGUID(request.SourceAppGUID)
}

func validateName(kind, value string) error {
	if !namePattern.MatchString(value) {
		return fmt.Errorf("invalid %s name", kind)
	}
	return nil
}

func validateGUID(value string) error {
	if !uuidPattern.MatchString(value) {
		return fmt.Errorf("invalid GUID")
	}
	return nil
}

func validateDomain(value string) error {
	if len(value) == 0 || len(value) > 253 || strings.HasSuffix(value, ".") || net.ParseIP(value) != nil {
		return fmt.Errorf("invalid domain")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return fmt.Errorf("invalid domain")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid domain")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' {
				return fmt.Errorf("invalid domain")
			}
		}
	}
	return nil
}

func validateBitsPath(workRoot, bitsPath string) error {
	if !filepath.IsAbs(bitsPath) {
		return fmt.Errorf("bits path must be absolute")
	}
	cleanBits := filepath.Clean(bitsPath)
	if workRoot != "" {
		if !filepath.IsAbs(workRoot) {
			return fmt.Errorf("work root must be absolute")
		}
		cleanRoot := filepath.Clean(workRoot)
		// This prevents accidental or user-supplied path escape. The manager owns
		// this staging tree; malicious same-UID mutation is outside the POC boundary.
		if err := requireChild(cleanRoot, cleanBits); err != nil {
			return err
		}
		physicalRoot, err := filepath.EvalSymlinks(cleanRoot)
		if err != nil {
			return fmt.Errorf("resolve physical work root: %w", err)
		}
		ancestor, err := nearestExistingAncestor(cleanBits)
		if err != nil {
			return err
		}
		physicalAncestor, err := filepath.EvalSymlinks(ancestor)
		if err != nil {
			return fmt.Errorf("resolve physical bits ancestor: %w", err)
		}
		remainder, err := filepath.Rel(ancestor, cleanBits)
		if err != nil {
			return fmt.Errorf("resolve bits path remainder: %w", err)
		}
		if err := requireChild(physicalRoot, filepath.Join(physicalAncestor, remainder)); err != nil {
			return err
		}
	}
	info, err := os.Stat(cleanBits)
	if err != nil {
		return fmt.Errorf("inspect bits path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("bits path must be a directory")
	}
	return nil
}

func nearestExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		_, err := os.Lstat(current)
		if err == nil {
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect bits path ancestor: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("bits path has no existing ancestor")
		}
	}
}

func requireChild(root, path string) error {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("bits path must be confined to work root")
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func decodeJSON(value []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
