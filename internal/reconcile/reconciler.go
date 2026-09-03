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
	Push(context.Context, cf.PushRequest) (cf.App, model.Operation, error)
	AppGUID(context.Context, string) (string, model.Operation, error)
	InspectApp(context.Context, string) (cf.App, error)
	SecureRoute(context.Context, cf.RouteRequest) (model.Operation, error)
	AddRoutePolicy(context.Context, cf.RoutePolicyRequest) (model.Operation, error)
	RemoveRoutePolicy(context.Context, cf.RoutePolicyRequest) (model.Operation, error)
	RemoveRoute(context.Context, cf.RouteRequest) (model.Operation, error)
	DeleteApp(context.Context, string) (model.Operation, error)
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

type Reconciler struct {
	config  Config
	store   Store
	runtime Runtime
	cf      CFProvider
	pack    PackManager
	probe   Probe
	clock   Clock

	worker       sync.Mutex
	stateMu      sync.Mutex
	inFlight     map[string]bool
	enrollments  map[string]Enrollment
	prepared     map[string]Prepared
	stop         chan struct{}
	done         chan struct{}
	started      bool
	active       int
	maxActive    int
	beforeEffect func(string)
}

func New(config Config, store Store, runtime Runtime, cloud CFProvider, packManager PackManager, probe Probe, clock Clock) *Reconciler {
	if config.PollAttempts < 1 {
		config.PollAttempts = 1
	}
	if config.ScanInterval <= 0 {
		config.ScanInterval = 2 * time.Second
	}
	return &Reconciler{config: config, store: store, runtime: runtime, cf: cloud, pack: packManager, probe: probe, clock: clock, inFlight: map[string]bool{}, enrollments: map[string]Enrollment{}, prepared: map[string]Prepared{}}
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
			err = r.ensureEnrollment(ctx, sandbox)
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseStaging, nil)
			}
		case model.PhaseStaging:
			err = r.ensurePrepared(ctx, sandbox)
			if err == nil {
				err = r.ensureEnrollment(ctx, sandbox)
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseDiscoveringApp, nil)
			}
			if err == nil {
				prepared := r.prepared[sandbox.Name]
				err = r.runtime.InstallEnrollment(prepared.Path, r.enrollments[sandbox.Name].Path())
				if err != nil {
					break
				}
				app, op, pushErr := r.effectPush(ctx, sandbox, prepared.Path)
				err = r.appendOperation(sandbox.Name, op)
				if err == nil {
					err = pushErr
				}
				if err == nil && app.GUID != "" {
					err = r.store.Update(sandbox.Name, func(s *model.Sandbox) error {
						s.AppGUID = app.GUID
						s.Phase = model.PhaseSecuringRoute
						s.UpdatedAt = r.clock.Now()
						return nil
					})
				}
			}
		case model.PhaseDiscoveringApp:
			var guid string
			var op model.Operation
			guid, op, err = r.effectGUID(ctx, sandbox.Name)
			if persistErr := r.appendOperation(sandbox.Name, op); persistErr != nil {
				err = persistErr
			}
			if err == nil {
				err = r.store.Update(sandbox.Name, func(s *model.Sandbox) error { s.AppGUID = guid; return nil })
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseSecuringRoute, nil)
			}
		case model.PhaseSecuringRoute:
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
			op, e := r.effectAddPolicy(ctx, cf.RoutePolicyRequest{Domain: r.config.IdentityDomain, Host: r.config.ManagerRouteHost, SourceAppGUID: sandbox.AppGUID})
			err = r.appendOperation(sandbox.Name, op)
			if err == nil && e != nil && !isAlreadyExists(e) {
				err = e
			}
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseWaitingForApp, nil)
			}
		case model.PhaseWaitingForApp:
			err = r.poll(ctx, "inspect-app", func() (bool, error) {
				app, e := r.cf.InspectApp(ctx, sandbox.AppGUID)
				return app.Running && app.Ready, e
			})
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseWaitingForRoute, nil)
			}
		case model.PhaseWaitingForRoute:
			err = r.poll(ctx, "probe-route", func() (bool, error) { return r.probe.Reachable(ctx, sandbox.InternalHost) })
			if err == nil {
				err = r.persistPhase(sandbox.Name, model.PhaseJoiningPack, nil)
			}
		case model.PhaseJoiningPack:
			var present bool
			present, err = r.effectMemberPresent(ctx, sandbox.Name)
			if err == nil && !present {
				err = errors.New("Collie Pack member did not enroll")
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

func (r *Reconciler) reconcileDelete(ctx context.Context, sandbox model.Sandbox) error {
	if err := r.persistPhase(sandbox.Name, model.PhaseDeleting, nil); err != nil {
		return err
	}
	if sandbox.PackMemberID != "" {
		present, err := r.effectMemberPresent(ctx, sandbox.PackMemberID)
		if err != nil {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
		if present {
			if err = r.effectRemoveMember(ctx, sandbox.PackMemberID); err != nil && !isAbsent(err) {
				return r.fail(sandbox.Name, model.PhaseDeleting, err)
			}
		}
	}
	if sandbox.AppGUID != "" {
		op, err := r.effectRemovePolicy(ctx, cf.RoutePolicyRequest{Domain: r.config.IdentityDomain, Host: r.config.ManagerRouteHost, SourceAppGUID: sandbox.AppGUID})
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil && !isAbsent(err) {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	if sandbox.InternalHost != "" {
		op, err := r.effectRemoveRoute(ctx, cf.RouteRequest{AppName: sandbox.Name, AppGUID: sandbox.AppGUID, Domain: r.config.IdentityDomain, Host: sandbox.Name, SourceAppGUID: r.config.ManagerAppGUID})
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil && !isAbsent(err) {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	if sandbox.AppGUID != "" {
		op, err := r.effectDeleteApp(ctx, sandbox.Name)
		if save := r.appendOperation(sandbox.Name, op); save != nil {
			return save
		}
		if err != nil && !isAbsent(err) {
			return r.fail(sandbox.Name, model.PhaseDeleting, err)
		}
	}
	if err := r.cleanupTransient(sandbox.Name); err != nil {
		return r.fail(sandbox.Name, model.PhaseDeleting, err)
	}
	if err := r.runtime.Cleanup(filepath.Join(r.config.WorkRoot, sandbox.Name)); err != nil && !isAbsent(err) {
		return r.fail(sandbox.Name, model.PhaseDeleting, err)
	}
	delete(r.prepared, sandbox.Name)
	return r.store.Delete(sandbox.Name)
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
	return r.store.Update(name, func(s *model.Sandbox) error {
		s.Operations = append(s.Operations, op)
		s.UpdatedAt = r.clock.Now()
		return nil
	})
}
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
func (r *Reconciler) effectPush(ctx context.Context, s model.Sandbox, path string) (cf.App, model.Operation, error) {
	r.effect("push")
	return r.cf.Push(ctx, cf.PushRequest{Name: s.Name, Buildpack: s.Buildpack, BitsPath: path, JoinTokenPath: "/home/vcap/app/.sandbox/join-token", PackLeadAddress: "https://" + r.config.ManagerPackHost})
}
func (r *Reconciler) effectGUID(ctx context.Context, name string) (string, model.Operation, error) {
	r.effect("discover-guid")
	return r.cf.AppGUID(ctx, name)
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
func (r *Reconciler) effectDeleteApp(ctx context.Context, name string) (model.Operation, error) {
	r.effect("delete-app")
	return r.cf.DeleteApp(ctx, name)
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
	r.stateMu.Lock()
	if r.started {
		r.stateMu.Unlock()
		return
	}
	r.started = true
	r.stop = make(chan struct{})
	r.done = make(chan struct{})
	r.stateMu.Unlock()
	go func() {
		defer close(r.done)
		r.scan(ctx)
		ticker := time.NewTicker(r.config.ScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stop:
				return
			case <-ticker.C:
				r.scan(ctx)
			}
		}
	}()
}
func (r *Reconciler) scan(ctx context.Context) {
	for _, sandbox := range r.store.List() {
		_ = r.ReconcileOne(ctx, sandbox.Name)
	}
}
func (r *Reconciler) Stop() {
	r.stateMu.Lock()
	if !r.started {
		r.stateMu.Unlock()
		return
	}
	stop, done := r.stop, r.done
	r.started = false
	close(stop)
	r.stateMu.Unlock()
	<-done
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
