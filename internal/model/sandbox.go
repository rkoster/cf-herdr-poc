package model

import "time"

type Phase string

const (
	PhaseCreating      Phase = "creating"
	PhaseStaging       Phase = "staging"
	PhaseStarting      Phase = "starting"
	PhaseSecuringRoute Phase = "securing-route"
	PhaseJoiningPack   Phase = "joining-pack"
	PhaseReady         Phase = "ready"
	PhaseDeleting      Phase = "deleting"
	PhaseFailed        Phase = "failed"
)

type Desired string

const (
	DesiredPresent Desired = "present"
	DesiredDeleted Desired = "deleted"
)

type Operation struct {
	Name      string        `json:"name"`
	StartedAt time.Time     `json:"startedAt"`
	Duration  time.Duration `json:"duration"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
}

type Sandbox struct {
	Name         string      `json:"name"`
	AppGUID      string      `json:"appGuid,omitempty"`
	Repository   string      `json:"repository"`
	Revision     string      `json:"revision,omitempty"`
	Buildpack    string      `json:"buildpack"`
	Desired      Desired     `json:"desired"`
	Phase        Phase       `json:"phase"`
	InternalHost string      `json:"internalHost,omitempty"`
	PackMemberID string      `json:"packMemberId,omitempty"`
	LastError    string      `json:"lastError,omitempty"`
	Operations   []Operation `json:"operations,omitempty"`
	CreatedAt    time.Time   `json:"createdAt"`
	UpdatedAt    time.Time   `json:"updatedAt"`
}
