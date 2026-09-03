package cf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cf-herdr-poc/internal/model"
	"cf-herdr-poc/internal/runner"
)

const maxOperationText = 1024

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type PushRequest struct {
	Name      string
	Buildpack string
	BitsPath  string
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
	Push(context.Context, PushRequest) (App, model.Operation, error)
	AppGUID(context.Context, string) (string, model.Operation, error)
	InspectApp(context.Context, string) (App, error)
	SecureRoute(context.Context, RouteRequest) (model.Operation, error)
	AddRoutePolicy(context.Context, RoutePolicyRequest) (model.Operation, error)
	RemoveRoutePolicy(context.Context, RoutePolicyRequest) (model.Operation, error)
	RemoveRoute(context.Context, RouteRequest) (model.Operation, error)
	DeleteApp(context.Context, string) (model.Operation, error)
}

type Provider struct {
	Run        runner.Runner
	Buildpacks []string
	WorkRoot   string
}

type Error struct {
	Operation string
	Kind      string
	Cause     error
}

func (e *Error) Error() string {
	return e.Operation + " " + e.Kind
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (p Provider) Push(ctx context.Context, request PushRequest) (App, model.Operation, error) {
	if err := validateName("app", request.Name); err != nil {
		return App{}, model.Operation{}, err
	}
	if !contains(p.Buildpacks, request.Buildpack) {
		return App{}, model.Operation{}, fmt.Errorf("buildpack is not allowed")
	}
	if err := validateBitsPath(p.WorkRoot, request.BitsPath); err != nil {
		return App{}, model.Operation{}, err
	}
	args := []string{"push", request.Name, "--no-route", "-b", request.Buildpack, "-p", request.BitsPath, "-c", "./.sandbox/start.sh"}
	operation, _, err := p.execute(ctx, "push", args...)
	if err != nil {
		return App{}, operation, err
	}
	guid, guidOperation, err := p.AppGUID(ctx, request.Name)
	operation.Duration += guidOperation.Duration
	operation.Command = bounded(operation.Command + " ; " + guidOperation.Command)
	if err != nil {
		operation.Success = false
		operation.Error = guidOperation.Error
		return App{}, operation, err
	}
	return App{Name: request.Name, GUID: guid}, operation, nil
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
	commands := [][]string{
		{"remove-route-policy", request.Domain, "--hostname", request.Host, "--source", "cf:app:" + request.SourceAppGUID},
		{"unmap-route", request.AppName, request.Domain, "--hostname", request.Host},
		{"delete-route", request.Domain, "--hostname", request.Host, "-f"},
	}
	return p.executeMany(ctx, "remove-route", commands)
}

func (p Provider) DeleteApp(ctx context.Context, name string) (model.Operation, error) {
	if err := validateName("app", name); err != nil {
		return model.Operation{}, err
	}
	operation, _, err := p.execute(ctx, "delete-app", "delete", name, "-f")
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
			GUID  string `json:"guid"`
			Type  string `json:"type"`
			State string `json:"state"`
		} `json:"resources"`
	}
	if err := decodeJSON(output, &processes); err != nil {
		return App{}, fmt.Errorf("decode processes JSON: %w", err)
	}
	app := App{GUID: guid, Ready: len(processes.Resources) > 0, Running: len(processes.Resources) > 0}
	for _, process := range processes.Resources {
		if err := validateGUID(process.GUID); err != nil {
			return App{}, fmt.Errorf("decode processes JSON: invalid process GUID")
		}
		app.State = process.State
		if process.State != "STARTED" {
			app.Running, app.Ready = false, false
		}
		_, statsOutput, err := p.execute(ctx, "inspect-process-stats", "curl", fmt.Sprintf("/v3/processes/%s/stats", url.PathEscape(process.GUID)))
		if err != nil {
			return App{}, err
		}
		var stats struct {
			Resources []struct {
				State string `json:"state"`
			} `json:"resources"`
		}
		if err := decodeJSON(statsOutput, &stats); err != nil {
			return App{}, fmt.Errorf("decode process stats JSON: %w", err)
		}
		if len(stats.Resources) == 0 {
			app.Running, app.Ready = false, false
		}
		for _, instance := range stats.Resources {
			if instance.State != "RUNNING" {
				app.Running, app.Ready = false, false
			}
		}
	}
	return app, nil
}

func (p Provider) executeMany(ctx context.Context, name string, commands [][]string) (model.Operation, error) {
	started := time.Now().UTC()
	result := model.Operation{Name: name, StartedAt: started, Success: true}
	for _, command := range commands {
		operation, _, err := p.execute(ctx, name, command...)
		result.Command = bounded(strings.TrimSpace(result.Command + " ; " + operation.Command))
		if err != nil {
			result.Success = false
			result.Error = operation.Error
			result.Duration = time.Since(started)
			return result, err
		}
	}
	result.Duration = time.Since(started)
	return result, nil
}

func (p Provider) execute(ctx context.Context, operationName string, args ...string) (model.Operation, []byte, error) {
	started := time.Now().UTC()
	operation := model.Operation{Name: operationName, StartedAt: started, Command: commandDisplay("cf", args)}
	if p.Run == nil {
		operation.Duration = time.Since(started)
		operation.Error = "command runner is required"
		return operation, nil, errors.New(operation.Error)
	}
	output, err := p.Run.Run(ctx, "cf", args...)
	operation.Duration = time.Since(started)
	operation.Success = err == nil
	if err != nil {
		cause := err
		operation.Error = bounded(operationName + " failed")
		if ctxErr := ctx.Err(); ctxErr != nil {
			operation.Error = bounded(operationName + " canceled: " + ctxErr.Error())
			cause = ctxErr
		}
		return operation, output, &Error{Operation: operationName, Kind: "failed", Cause: cause}
	}
	return operation, output, nil
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
	if workRoot == "" {
		return nil
	}
	if !filepath.IsAbs(workRoot) {
		return fmt.Errorf("work root must be absolute")
	}
	cleanRoot, cleanBits := filepath.Clean(workRoot), filepath.Clean(bitsPath)
	relative, err := filepath.Rel(cleanRoot, cleanBits)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("bits path must be confined to work root")
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(cleanRoot)
	resolvedBits, bitsErr := filepath.EvalSymlinks(cleanBits)
	if rootErr == nil && bitsErr == nil {
		relative, err = filepath.Rel(resolvedRoot, resolvedBits)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("bits path must be physically confined to work root")
		}
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
