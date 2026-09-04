package reconcile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"cf-herdr-poc/internal/cf"
	"cf-herdr-poc/internal/model"
	"cf-herdr-poc/internal/pack"
	runtimebundle "cf-herdr-poc/internal/runtime"
)

type Store interface {
	Get(string) (model.Sandbox, bool)
	List() []model.Sandbox
	Update(string, func(*model.Sandbox) error) error
	Delete(string) error
}

type Prepared struct {
	Path     string
	Revision string
}

type Runtime interface {
	Prepare(context.Context, string, string) (Prepared, error)
	Prepared(string) (bool, error)
	InstallEnrollment(string, string) error
	Cleanup(string) error
}

type CFProvider interface {
	Stage(context.Context, cf.PushRequest) (model.Operation, error)
	EnsureAppAbsent(context.Context, string) (model.Operation, error)
	AppGUID(context.Context, string) (string, model.Operation, error)
	ConfigureEnrollment(context.Context, string, string, string) (model.Operation, error)
	StartApp(context.Context, string) (model.Operation, error)
	InspectApp(context.Context, string) (cf.App, error)
	SecureRoute(context.Context, cf.RouteRequest) (model.Operation, error)
	AddRoutePolicy(context.Context, cf.RoutePolicyRequest) (model.Operation, error)
	RemoveRoutePolicy(context.Context, cf.RoutePolicyRequest) (model.Operation, error)
	RemoveRoute(context.Context, cf.RouteRequest) (model.Operation, error)
	DeleteApp(context.Context, string, string) (model.Operation, error)
}

type Enrollment interface {
	Path() string
	ExpiresAt() time.Time
	Cleanup() error
}

type PackManager interface {
	PrepareEnrollment(context.Context, string, string) (Enrollment, error)
	MemberPresent(context.Context, string) (bool, error)
	RemoveMember(context.Context, string) error
}

type Probe interface {
	Reachable(context.Context, string) (bool, error)
	TriggerEnrollment(context.Context, string) (model.Operation, error)
}

type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type Config struct {
	WorkRoot         string
	IdentityDomain   string
	ManagerRouteHost string
	ManagerPackHost  string
	ManagerAppGUID   string
	PollAttempts     int
	PollInterval     time.Duration
	ScanInterval     time.Duration
}

type lifecycleState uint8

const (
	lifecycleStopped lifecycleState = iota
	lifecycleRunning
	lifecycleStopping
)

type workerGeneration struct {
	stop chan struct{}
	done chan struct{}
}

type Reconciler struct {
	config  Config
	store   Store
	runtime Runtime
	cf      CFProvider
	pack    PackManager
	probe   Probe
	clock   Clock

	worker              sync.Mutex
	stateMu             sync.Mutex
	inFlight            map[string]bool
	enrollments         map[string]Enrollment
	prepared            map[string]Prepared
	recoveryPhase       map[string]model.Phase
	lifecycle           lifecycleState
	generation          *workerGeneration
	active              int
	maxActive           int
	beforeEffect        func(string)
	beforeLifecycleDone func()
}

func New(config Config, store Store, runtime Runtime, cloud CFProvider, packManager PackManager, probe Probe, clock Clock) *Reconciler {
	if config.PollAttempts < 1 {
		config.PollAttempts = 1
	}
	if config.ScanInterval <= 0 {
		config.ScanInterval = 2 * time.Second
	}
	return &Reconciler{config: config, store: store, runtime: runtime, cf: cloud, pack: packManager, probe: probe, clock: clock, inFlight: map[string]bool{}, enrollments: map[string]Enrollment{}, prepared: map[string]Prepared{}, recoveryPhase: map[string]model.Phase{}}
}

func (r *Reconciler) ReconcileOne(ctx context.Context, name string) error {
	r.stateMu.Lock()
	if r.inFlight[name] {
		r.stateMu.Unlock()
		return nil
	}
	r.inFlight[name] = true
	r.stateMu.Unlock()
	defer func() { r.stateMu.Lock(); delete(r.inFlight, name); r.stateMu.Unlock() }()

	r.worker.Lock()
	r.stateMu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.stateMu.Unlock()
	defer func() { r.stateMu.Lock(); r.active--; r.stateMu.Unlock(); r.worker.Unlock() }()

	sandbox, ok := r.store.Get(name)
	if !ok {
		return nil
	}
	if sandbox.Desired == model.DesiredDeleted {
		return r.reconcileDelete(ctx, sandbox)
	}
	if sandbox.Phase == model.PhaseFailed {
		return nil
	}
	return r.reconcileCreate(ctx, sandbox)
}

func (r *Reconciler) Retry(ctx context.Context, name string) error {
	sandbox, ok := r.store.Get(name)
	if !ok {
		return fmt.Errorf("sandbox does not exist")
	}
	if sandbox.Phase == model.PhaseFailed {
		if err := r.store.Update(name, func(s *model.Sandbox) error {
			s.Phase = s.ResumePhase
			s.ResumePhase = ""
			s.LastError = ""
			s.UpdatedAt = r.clock.Now()
			return nil
		}); err != nil {
			return err
		}
	}
	return r.ReconcileOne(ctx, name)
}

func (r *Reconciler) AllowsActions(name string) bool {
	sandbox, ok := r.store.Get(name)
	return ok && sandbox.Desired != model.DesiredDeleted
}

func (r *Reconciler) reconcileCreate(ctx context.Context, sandbox model.Sandbox) error {
	for {
		current, ok := r.store.Get(sandbox.Name)
		if !ok {
			return nil
		}
		sandbox = current
		if sandbox.Desired == model.DesiredDeleted {
			return r.reconcileDelete(ctx, sandbox)
		}
		if needsEnrollment(sandbox.Phase) {
			enrollment, ok := r.enrollments[sandbox.Name]
			if !ok || (!enrollment.ExpiresAt().IsZero() && !enrollment.ExpiresAt().After(r.clock.Now())) {
				if ok {
					if err := enrollment.Cleanup(); err != nil {
						return r.fail(sandbox.Name, sandbox.Phase, err)
					}
					delete(r.enrollments, sandbox.Name)
				}
				r.recoveryPhase[sandbox.Name] = sandbox.Phase
				if err := r.persistPhase(sandbox.Name, model.PhasePreparingInvite, nil); err != nil {
					return err
				}
				continue
			}
		}
		var err error
		switch sandbox.Phase {
		case model.PhaseCreating:
			if err = r.persistPhase(sandbox.Name, model.PhaseCreating, nil); err == nil {
				var result Prepared
				result, err = r.effectPrepare(ctx, sandbox)
				if err == nil {
					r.prepared[sandbox.Name] = result
					err = r.store.Update(sandbox.Name, func(s *model.Sandbox) error { s.Revision = result.Revision; s.UpdatedAt = r.clock.Now(); return nil })
				}
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhasePreparingInvite, nil)
			}
		case model.PhasePreparingInvite:
			err = r.persistPhase(sandbox.Name, model.PhasePreparingInvite, nil)
			if err == nil {
				err = r.ensureEnrollment(ctx, sandbox)
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseStaging, nil)
			}
		case model.PhaseStaging:
			err = r.persistPhase(sandbox.Name, model.PhaseStaging, nil)
			if err == nil {
				err = r.ensurePrepared(ctx, sandbox)
			}
			if err == nil {
				err = r.ensureEnrollment(ctx, sandbox)
			}
			if err == nil {
				prepared := r.prepared[sandbox.Name]
				err = r.persistPhase(sandbox.Name, model.PhaseStaging, nil)
				if err != nil {
					break
				}
				err = r.runtime.InstallEnrollment(prepared.Path, r.enrollments[sandbox.Name].Path())
				if err != nil {
					break
				}
				err = r.persistPhase(sandbox.Name, model.PhaseStaging, nil)
				if err != nil {
					break
				}
				op, stageErr := r.effectStage(ctx, sandbox, prepared.Path)
				err = r.appendOperation(sandbox.Name, op)
				if err == nil {
					err = stageErr
				}
				if err == nil {
					err = r.persistPhase(sandbox.Name, model.PhaseDiscoveringApp, nil)
				}
			}
		case model.PhaseDiscoveringApp:
			err = r.persistPhase(sandbox.Name, model.PhaseDiscoveringApp, nil)
			if err != nil {
				break
			}
			var guid string
			var op model.Operation
			guid, op, err = r.effectGUID(ctx, sandbox.Name)
			if persistErr := r.appendOperation(sandbox.Name, op); persistErr != nil {
				err = persistErr
			}
			if err == nil && sandbox.AppGUID != "" && guid != sandbox.AppGUID {
				err = fmt.Errorf("app identity mismatch: persisted GUID does not match current app")
			}
			if err == nil {
				next := model.PhaseSecuringRoute
				if recovery, ok := r.recoveryPhase[sandbox.Name]; ok {
					next = recovery
					delete(r.recoveryPhase, sandbox.Name)
				}
				err = r.store.Update(sandbox.Name, func(s *model.Sandbox) error {
					s.AppGUID = guid
					s.Phase = next
					s.UpdatedAt = r.clock.Now()
					return nil
				})
			}
		case model.PhaseSecuringRoute:
			err = r.persistPhase(sandbox.Name, model.PhaseSecuringRoute, nil)
			if err != nil {
				break
			}
			host := sandbox.Name + "." + r.config.IdentityDomain
			op, e := r.effectSecureRoute(ctx, cf.RouteRequest{AppName: sandbox.Name, AppGUID: sandbox.AppGUID, Domain: r.config.IdentityDomain, Host: sandbox.Name, SourceAppGUID: r.config.ManagerAppGUID})
			err = r.appendOperation(sandbox.Name, op)
			if err == nil && e != nil && !isAlreadyExists(e) {
				err = e
			}
			if err == nil {
				err = r.store.Update(sandbox.Name, func(s *model.Sandbox) error { s.InternalHost = host; return nil })
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseSecuringManagerRoute, nil)
			}
		case model.PhaseSecuringManagerRoute:
			err = r.persistPhase(sandbox.Name, model.PhaseSecuringManagerRoute, nil)
			if err != nil {
				break
			}
			op, e := r.effectAddPolicy(ctx, cf.RoutePolicyRequest{Domain: r.config.IdentityDomain, Host: r.config.ManagerRouteHost, SourceAppGUID: sandbox.AppGUID})
			err = r.appendOperation(sandbox.Name, op)
			if err == nil && e != nil && !isAlreadyExists(e) {
				err = e
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseConfiguringEnrollment, nil)
			}
		case model.PhaseConfiguringEnrollment:
			err = r.persistPhase(sandbox.Name, model.PhaseConfiguringEnrollment, nil)
			if err == nil {
				op, e := r.effectConfigureEnrollment(ctx, sandbox.Name)
				err = r.appendOperation(sandbox.Name, op)
				if err == nil {
					err = e
				}
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseStarting, nil)
			}
		case model.PhaseStarting:
			err = r.persistPhase(sandbox.Name, model.PhaseStarting, nil)
			if err == nil {
				op, e := r.effectStartApp(ctx, sandbox.Name)
				err = r.appendOperation(sandbox.Name, op)
				if err == nil {
					err = e
				}
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseWaitingForApp, nil)
			}
		case model.PhaseWaitingForApp:
			err = r.persistPhase(sandbox.Name, model.PhaseWaitingForApp, nil)
			if err != nil {
				break
			}
			err = r.poll(ctx, "inspect-app", func() (bool, error) {
				app, e := r.cf.InspectApp(ctx, sandbox.AppGUID)
				return app.Running && app.Ready, e
			})
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseWaitingForRoute, nil)
			}
		case model.PhaseWaitingForRoute:
			err = r.persistPhase(sandbox.Name, model.PhaseWaitingForRoute, nil)
			if err != nil {
				break
			}
			err = r.poll(ctx, "probe-route", func() (bool, error) { return r.probe.Reachable(ctx, sandbox.InternalHost) })
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseTriggeringEnrollment, nil)
			}
		case model.PhaseTriggeringEnrollment:
			err = r.persistPhase(sandbox.Name, model.PhaseTriggeringEnrollment, nil)
			if err == nil {
				var present bool
				present, err = r.pack.MemberPresent(ctx, sandbox.Name)
				if err == nil && !present {
					err = r.persistPhase(sandbox.Name, model.PhaseTriggeringEnrollment, nil)
					if err != nil {
						break
					}
					op, e := r.effectTriggerEnrollment(ctx, sandbox.InternalHost)
					err = r.appendOperation(sandbox.Name, op)
					if err == nil && e != nil {
						present, err = r.pack.MemberPresent(ctx, sandbox.Name)
						if err == nil && !present {
							err = e
						}
					}
				}
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseJoiningPack, nil)
			}
		case model.PhaseJoiningPack:
			err = r.persistPhase(sandbox.Name, model.PhaseJoiningPack, nil)
			if err == nil {
				err = r.poll(ctx, "observe-member", func() (bool, error) { return r.pack.MemberPresent(ctx, sandbox.Name) })
			}
			if err == nil {
				err = r.cleanupTransient(sandbox.Name)
			}
			if err == nil {
				err = r.store.Update(sandbox.Name, func(s *model.Sandbox) error { s.PackMemberID = s.Name; return nil })
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseReady, nil)
			}
		case model.PhaseReady:
			return nil
		default:
			err = fmt.Errorf("unknown reconciliation phase %q", sandbox.Phase)
		}
		if err != nil {
			current, ok := r.store.Get(sandbox.Name)
			if ok {
				sandbox = current
			}
			return r.fail(sandbox.Name, sandbox.Phase, err)
		}
	}
}

func needsEnrollment(phase model.Phase) bool {
	switch phase {
	case model.PhaseDiscoveringApp, model.PhaseSecuringRoute, model.PhaseSecuringManagerRoute, model.PhaseConfiguringEnrollment, model.PhaseStarting:
		return true
	}
	return false
}

func (r *Reconciler) reconcileDelete(ctx context.Context, sandbox model.Sandbox) error {
	if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
		return err
	}
	memberID := sandbox.Name
	{
		if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
			return err
		}
		present, err := r.effectMemberPresent(ctx, memberID)
		if err != nil {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
		if present {
			if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
				return err
			}
			if err = r.effectRemoveMember(ctx, memberID); err != nil && !isAbsent(err) {
				return r.fail(sandbox.Name, model.PhaseDeleting, err)
			}
		}
	}
	appGUID := sandbox.AppGUID
	appExists := appGUID != ""
	if appGUID == "" {
		if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
			return err
		}
		op, err := r.effectAppAbsent(ctx, sandbox.Name)
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil {
			if isAlreadyExists(err) {
				return r.fail(sandbox.Name, model.PhaseDeleting, errors.New("app ownership unknown: persisted GUID is missing and app name already exists"))
			}
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	if appExists {
		if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
			return err
		}
		op, err := r.effectRemovePolicy(ctx, cf.RoutePolicyRequest{Domain: r.config.IdentityDomain, Host: r.config.ManagerRouteHost, SourceAppGUID: appGUID})
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil && !isAbsent(err) {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	{
		if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
			return err
		}
		op, err := r.effectRemoveRoute(ctx, cf.RouteRequest{AppName: sandbox.Name, AppGUID: appGUID, Domain: r.config.IdentityDomain, Host: sandbox.Name, SourceAppGUID: r.config.ManagerAppGUID})
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil && !isAbsent(err) {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	if appExists {
		if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
			return err
		}
		op, err := r.effectDeleteApp(ctx, sandbox.Name, appGUID)
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil && !isAbsent(err) {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
		return err
	}
	if err := r.cleanupTransient(sandbox.Name); err != nil {
		return r.fail(sandbox.Name, model.PhaseDeleting, err)
	}
	if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
		return err
	}
	if err := r.runtime.Cleanup(filepath.Join(r.config.WorkRoot, sandbox.Name)); err != nil && !isAbsent(err) {
		return r.fail(sandbox.Name, model.PhaseDeleting, err)
	}
	delete(r.prepared, sandbox.Name)
	if err := r.store.Delete(sandbox.Name); err != nil {
		return r.fail(sandbox.Name, model.PhaseDeleting, err)
	}
	return nil
}

func (r *Reconciler) ensurePrepared(ctx context.Context, s model.Sandbox) error {
	if _, ok := r.prepared[s.Name]; ok {
		return nil
	}
	path := filepath.Join(r.config.WorkRoot, s.Name)
	if s.Revision != "" {
		present, err := r.runtime.Prepared(path)
		if err != nil {
			return err
		}
		if present {
			r.prepared[s.Name] = Prepared{Path: path, Revision: s.Revision}
			return nil
		}
	}
	result, err := r.effectPrepare(ctx, s)
	if err == nil {
		r.prepared[s.Name] = result
	}
	return err
}
func (r *Reconciler) ensureEnrollment(ctx context.Context, s model.Sandbox) error {
	if old, ok := r.enrollments[s.Name]; ok {
		if old.ExpiresAt().IsZero() || old.ExpiresAt().After(r.clock.Now()) {
			return nil
		}
		_ = old.Cleanup()
		delete(r.enrollments, s.Name)
	}
	enrollment, err := r.effectPrepareEnrollment(ctx, s.Name)
	if err == nil {
		r.enrollments[s.Name] = enrollment
	}
	return err
}
func (r *Reconciler) cleanupTransient(name string) error {
	if e, ok := r.enrollments[name]; ok {
		if err := e.Cleanup(); err != nil {
			return err
		}
		delete(r.enrollments, name)
	}
	if _, ok := r.prepared[name]; ok {
		if err := r.runtime.Cleanup(r.prepared[name].Path); err != nil && !isAbsent(err) {
			return err
		}
		delete(r.prepared, name)
	}
	return nil
}

func (r *Reconciler) persistPhase(name string, phase model.Phase, op *model.Operation) error {
	return r.store.Update(name, func(s *model.Sandbox) error {
		s.Phase = phase
		s.ResumePhase = ""
		s.LastError = ""
		s.UpdatedAt = r.clock.Now()
		if op != nil {
			s.Operations = append(s.Operations, *op)
		}
		return nil
	})
}
func (r *Reconciler) appendOperation(name string, op model.Operation) error {
	if op.Name == "" {
		return nil
	}
	op.Name = sanitizeOperationField(op.Name)
	op.Command = sanitizeOperationField(op.Command)
	op.Summary = sanitizeOperationField(op.Summary)
	op.Error = sanitizeOperationField(op.Error)
	return r.store.Update(name, func(s *model.Sandbox) error {
		s.Operations = append(s.Operations, op)
		s.UpdatedAt = r.clock.Now()
		return nil
	})
}

func sanitizeOperationField(value string) string { return sanitize(value) }
func (r *Reconciler) fail(name string, resume model.Phase, cause error) error {
	message := sanitize(cause.Error())
	persistErr := r.store.Update(name, func(s *model.Sandbox) error {
		s.Phase = model.PhaseFailed
		s.ResumePhase = resume
		s.LastError = message
		s.UpdatedAt = r.clock.Now()
		return nil
	})
	return errors.Join(cause, persistErr)
}

func (r *Reconciler) poll(ctx context.Context, name string, condition func() (bool, error)) error {
	for attempt := 0; attempt < r.config.PollAttempts; attempt++ {
		r.effect(name)
		ok, err := condition()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if attempt+1 < r.config.PollAttempts {
			if err := r.clock.Wait(ctx, r.config.PollInterval); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("%s timed out", name)
}
func (r *Reconciler) effect(name string) {
	if r.beforeEffect != nil {
		r.beforeEffect(name)
	}
}
func (r *Reconciler) effectPrepare(ctx context.Context, s model.Sandbox) (Prepared, error) {
	r.effect("prepare-bits")
	return r.runtime.Prepare(ctx, s.Repository, filepath.Join(r.config.WorkRoot, s.Name))
}
func (r *Reconciler) effectPrepareEnrollment(ctx context.Context, name string) (Enrollment, error) {
	r.effect("prepare-invite")
	return r.pack.PrepareEnrollment(ctx, r.config.ManagerPackHost, name)
}
func (r *Reconciler) effectStage(ctx context.Context, s model.Sandbox, path string) (model.Operation, error) {
	r.effect("stage")
	return r.cf.Stage(ctx, cf.PushRequest{Name: s.Name, Buildpack: s.Buildpack, BitsPath: path})
}
func (r *Reconciler) effectConfigureEnrollment(ctx context.Context, name string) (model.Operation, error) {
	r.effect("configure-enrollment")
	return r.cf.ConfigureEnrollment(ctx, name, "/home/vcap/app/.sandbox/join-token", "https://"+r.config.ManagerPackHost)
}
func (r *Reconciler) effectStartApp(ctx context.Context, name string) (model.Operation, error) {
	r.effect("start-app")
	return r.cf.StartApp(ctx, name)
}
func (r *Reconciler) effectTriggerEnrollment(ctx context.Context, host string) (model.Operation, error) {
	r.effect("trigger-enrollment")
	return r.probe.TriggerEnrollment(ctx, host)
}
func (r *Reconciler) effectGUID(ctx context.Context, name string) (string, model.Operation, error) {
	r.effect("discover-guid")
	return r.cf.AppGUID(ctx, name)
}

func (r *Reconciler) effectAppAbsent(ctx context.Context, name string) (model.Operation, error) {
	r.effect("observe-app-absence")
	return r.cf.EnsureAppAbsent(ctx, name)
}
func (r *Reconciler) effectSecureRoute(ctx context.Context, q cf.RouteRequest) (model.Operation, error) {
	r.effect("secure-route")
	return r.cf.SecureRoute(ctx, q)
}
func (r *Reconciler) effectAddPolicy(ctx context.Context, q cf.RoutePolicyRequest) (model.Operation, error) {
	r.effect("secure-manager-route")
	return r.cf.AddRoutePolicy(ctx, q)
}
func (r *Reconciler) effectMemberPresent(ctx context.Context, id string) (bool, error) {
	r.effect("observe-member")
	return r.pack.MemberPresent(ctx, id)
}
func (r *Reconciler) effectRemoveMember(ctx context.Context, id string) error {
	r.effect("remove-member")
	return r.pack.RemoveMember(ctx, id)
}
func (r *Reconciler) effectRemovePolicy(ctx context.Context, q cf.RoutePolicyRequest) (model.Operation, error) {
	r.effect("remove-manager-policy")
	return r.cf.RemoveRoutePolicy(ctx, q)
}
func (r *Reconciler) effectRemoveRoute(ctx context.Context, q cf.RouteRequest) (model.Operation, error) {
	r.effect("remove-route")
	return r.cf.RemoveRoute(ctx, q)
}
func (r *Reconciler) effectDeleteApp(ctx context.Context, name, expectedGUID string) (model.Operation, error) {
	r.effect("delete-app")
	return r.cf.DeleteApp(ctx, name, expectedGUID)
}

type absent interface{ Absent() bool }

func isAbsent(err error) bool {
	var target absent
	return errors.Is(err, errors.ErrUnsupported) || errors.As(err, &target) && target.Absent()
}

type alreadyExists interface{ AlreadyExists() bool }

func isAlreadyExists(err error) bool {
	var target alreadyExists
	return errors.As(err, &target) && target.AlreadyExists()
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
		return value[:1024]
	}
	return value
}

func (r *Reconciler) Start(ctx context.Context) {
	for {
		r.stateMu.Lock()
		if r.lifecycle == lifecycleRunning {
			r.stateMu.Unlock()
			return
		}
		if r.lifecycle == lifecycleStopping {
			done := r.generation.done
			r.stateMu.Unlock()
			<-done
			continue
		}
		generation := &workerGeneration{stop: make(chan struct{}), done: make(chan struct{})}
		r.lifecycle = lifecycleRunning
		r.generation = generation
		r.stateMu.Unlock()
		go func(generation *workerGeneration) {
			defer func() {
				r.stateMu.Lock()
				if r.beforeLifecycleDone != nil {
					r.beforeLifecycleDone()
				}
				if r.generation == generation {
					r.lifecycle = lifecycleStopped
					r.generation = nil
				}
				close(generation.done)
				r.stateMu.Unlock()
			}()
			r.scan(ctx)
			ticker := time.NewTicker(r.config.ScanInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-generation.stop:
					return
				case <-ticker.C:
					r.scan(ctx)
				}
			}
		}(generation)
		return
	}
}
func (r *Reconciler) scan(ctx context.Context) {
	for _, sandbox := range r.store.List() {
		_ = r.ReconcileOne(ctx, sandbox.Name)
	}
}
func (r *Reconciler) Stop() {
	r.stateMu.Lock()
	if r.lifecycle == lifecycleStopped {
		r.stateMu.Unlock()
		return
	}
	generation := r.generation
	if r.lifecycle == lifecycleRunning {
		r.lifecycle = lifecycleStopping
		close(generation.stop)
	}
	r.stateMu.Unlock()
	<-generation.done
}
func (r *Reconciler) MaxConcurrent() int {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.maxActive
}

// BundleRuntime adapts the concrete runtime builder without leaking it into tests.
type BundleRuntime struct{ Builder runtimebundle.Builder }

func (a BundleRuntime) Prepare(ctx context.Context, repository, destination string) (Prepared, error) {
	result, err := a.Builder.Prepare(ctx, repository, destination)
	return Prepared{Path: destination, Revision: result.Revision}, err
}
func (a BundleRuntime) Prepared(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
func (a BundleRuntime) InstallEnrollment(destination, source string) error {
	return a.Builder.InstallEnrollment(destination, source)
}
func (a BundleRuntime) Cleanup(path string) error { return os.RemoveAll(path) }

type packEnrollment struct{ value *pack.Enrollment }

func (e packEnrollment) Path() string         { return e.value.Path }
func (e packEnrollment) ExpiresAt() time.Time { return e.value.ExpiresAt }
func (e packEnrollment) Cleanup() error       { return e.value.Cleanup() }

type ConcretePackManager struct{ Manager *pack.Manager }

func (a ConcretePackManager) PrepareEnrollment(ctx context.Context, host, name string) (Enrollment, error) {
	value, err := a.Manager.PrepareEnrollment(ctx, host, name)
	if err != nil {
		return nil, err
	}
	return packEnrollment{value}, nil
}
func (a ConcretePackManager) MemberPresent(ctx context.Context, id string) (bool, error) {
	return a.Manager.MemberPresent(ctx, id)
}
func (a ConcretePackManager) RemoveMember(ctx context.Context, id string) error {
	return a.Manager.RemoveMember(ctx, id)
}
