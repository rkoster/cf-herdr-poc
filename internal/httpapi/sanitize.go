package httpapi

import (
	"regexp"
	"strings"
	"time"

	"cf-herdr-poc/internal/model"
)

type OperationView struct {
	Name      string        `json:"name"`
	Summary   string        `json:"summary,omitempty"`
	StartedAt time.Time     `json:"startedAt"`
	Duration  time.Duration `json:"duration"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

type SandboxView struct {
	Name         string          `json:"name"`
	Repository   string          `json:"repository"`
	Revision     string          `json:"revision,omitempty"`
	Buildpack    string          `json:"buildpack"`
	Desired      model.Desired   `json:"desired"`
	Phase        model.Phase     `json:"phase"`
	ResumePhase  model.Phase     `json:"resumePhase,omitempty"`
	PackMemberID string          `json:"packMemberId,omitempty"`
	LastError    string          `json:"lastError,omitempty"`
	Operations   []OperationView `json:"operations,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

func PublicSandbox(sandbox model.Sandbox) SandboxView {
	view := SandboxView{Name: sandbox.Name, Repository: sandbox.Repository, Revision: sandbox.Revision, Buildpack: sandbox.Buildpack, Desired: sandbox.Desired, Phase: sandbox.Phase, ResumePhase: sandbox.ResumePhase, PackMemberID: sandbox.PackMemberID, LastError: sanitize(sandbox.LastError), CreatedAt: sandbox.CreatedAt, UpdatedAt: sandbox.UpdatedAt}
	for _, operation := range sandbox.Operations {
		view.Operations = append(view.Operations, OperationView{Name: sanitize(operation.Name), Summary: sanitize(operation.Summary), StartedAt: operation.StartedAt, Duration: operation.Duration, Success: operation.Success, Error: sanitize(operation.Error)})
	}
	return view
}

var secretPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+)?|\b(?:token|secret|password)\s*[=:]\s*)\S+`)

func sanitize(value string) string {
	value = secretPattern.ReplaceAllString(value, "${1}[REDACTED]")
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}
