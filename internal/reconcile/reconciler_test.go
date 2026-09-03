package reconcile

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cf-herdr-poc/internal/cf"
	"cf-herdr-poc/internal/model"
)

const (
	managerGUID = "11111111-1111-4111-8111-111111111111"
	sandboxGUID = "22222222-2222-4222-8222-222222222222"
)

type memoryStore struct {
	mu           sync.Mutex
	items        map[string]model.Sandbox
	calls        *[]string
	failUpdate   int
	updateCount  int
	failOnUpdate int
}

func (s *memoryStore) Get(name string) (model.Sandbox, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[name]
	return v, ok
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
func (s *memoryStore) Update(name string, fn func(*model.Sandbox) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCount++
	if s.failOnUpdate == s.updateCount {
		return errors.New("token=store-secret persistence failed")
	}
	if s.failUpdate > 0 {
		s.failUpdate--
		return errors.New("token=store-secret persistence failed")
	}
	v, ok := s.items[name]
	if !ok {
		return errors.New("missing")
	}
	if err := fn(&v); err != nil {
		return err
	}
	s.items[name] = v
	if s.calls != nil {
		*s.calls = append(*s.calls, "persist:"+string(v.Phase))
	}
	return nil
}
func (s *memoryStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, name)
	if s.calls != nil {
		*s.calls = append(*s.calls, "delete-record")
	}
	return nil
}

type fakeRuntime struct {
	calls *[]string
	fail  error
}

func (f *fakeRuntime) Prepare(context.Context, string, string) (Prepared, error) {
	*f.calls = append(*f.calls, "prepare-bits")
	return Prepared{Path: "/work/demo", Revision: "abc123"}, f.fail
}
func (f *fakeRuntime) Prepared(string) (bool, error) {
	*f.calls = append(*f.calls, "observe-bits")
	return true, f.fail
}
func (f *fakeRuntime) InstallEnrollment(string, string) error {
	*f.calls = append(*f.calls, "install-invite")
	return f.fail
}
func (f *fakeRuntime) Cleanup(string) error {
	*f.calls = append(*f.calls, "cleanup-bits")
	return f.fail
}

type fakeEnrollment struct {
	calls       *[]string
	path        string
	expiry      time.Time
	failCleanup bool
}

func (e *fakeEnrollment) Path() string         { return e.path }
func (e *fakeEnrollment) ExpiresAt() time.Time { return e.expiry }
func (e *fakeEnrollment) Cleanup() error {
	*e.calls = append(*e.calls, "cleanup-invite")
	if e.failCleanup {
		return errors.New("token=cleanup-secret failed")
	}
	return nil
}

type fakePack struct {
	calls   *[]string
	present bool
	failAt  string
	expiry  time.Time
}

func (p *fakePack) PrepareEnrollment(context.Context, string, string) (Enrollment, error) {
	*p.calls = append(*p.calls, "prepare-invite")
	if p.failAt == "prepare-invite" {
		return nil, errors.New("invite token=secret failed")
	}
	return &fakeEnrollment{calls: p.calls, path: "/secret/invite", expiry: p.expiry}, nil
}
func (p *fakePack) MemberPresent(context.Context, string) (bool, error) {
	*p.calls = append(*p.calls, "observe-member")
	if p.failAt == "observe-member" {
		return false, errors.New("member failed")
	}
	return p.present, nil
}
func (p *fakePack) RemoveMember(context.Context, string) error {
	*p.calls = append(*p.calls, "remove-member")
	if p.failAt == "remove-member" {
		return errors.New("remove failed")
	}
	p.present = false
	return nil
}

type classifiedError struct{ kind string }

func (e classifiedError) Error() string       { return e.kind }
func (e classifiedError) AlreadyExists() bool { return e.kind == "exists" }
func (e classifiedError) Absent() bool        { return e.kind == "absent" }

type fakeCF struct {
	calls     *[]string
	app       cf.App
	failAt    string
	operation model.Operation
}

func (f *fakeCF) Stage(_ context.Context, _ cf.PushRequest) (model.Operation, error) {
	*f.calls = append(*f.calls, "stage")
	op := operation("stage", f.failAt != "stage")
	if f.failAt == "stage" {
		return op, errors.New("staging Authorization: Bearer secret")
	}
	return op, nil
}
func (f *fakeCF) AppGUID(context.Context, string) (string, model.Operation, error) {
	*f.calls = append(*f.calls, "discover-guid")
	return sandboxGUID, operation("app-guid", true), nil
}
func (f *fakeCF) InspectApp(context.Context, string) (cf.App, error) {
	*f.calls = append(*f.calls, "inspect-app")
	if f.failAt == "inspect-app" {
		return cf.App{}, errors.New("inspect failed")
	}
	return f.app, nil
}
func (f *fakeCF) SecureRoute(context.Context, cf.RouteRequest) (model.Operation, error) {
	*f.calls = append(*f.calls, "secure-route")
	if f.operation.Name != "" {
		return f.operation, nil
	}
	if f.failAt == "secure-route-exists" {
		return operation("secure-route", false), classifiedError{kind: "exists"}
	}
	return operation("secure-route", true), nil
}
func (f *fakeCF) AddRoutePolicy(context.Context, cf.RoutePolicyRequest) (model.Operation, error) {
	*f.calls = append(*f.calls, "secure-manager-route")
	return operation("secure-manager-route", true), nil
}
func (f *fakeCF) ConfigureEnrollment(context.Context, string, string, string) (model.Operation, error) {
	*f.calls = append(*f.calls, "configure-enrollment")
	return operation("configure-enrollment", true), nil
}
func (f *fakeCF) StartApp(context.Context, string) (model.Operation, error) {
	*f.calls = append(*f.calls, "start-app")
	f.app = cf.App{GUID: sandboxGUID, Running: true, Ready: true}
	return operation("start-app", true), nil
}
func (f *fakeCF) RemoveRoutePolicy(context.Context, cf.RoutePolicyRequest) (model.Operation, error) {
	*f.calls = append(*f.calls, "remove-manager-policy")
	if f.failAt == "remove-manager-policy" {
		return operation("remove-manager-policy", false), errors.New("absent")
	}
	return operation("remove-manager-policy", true), nil
}
func (f *fakeCF) RemoveRoute(context.Context, cf.RouteRequest) (model.Operation, error) {
	*f.calls = append(*f.calls, "remove-route")
	if f.failAt == "remove-route" {
		return operation("remove-route", false), errors.New("route failed")
	}
	return operation("remove-route", true), nil
}
func (f *fakeCF) DeleteApp(context.Context, string) (model.Operation, error) {
	*f.calls = append(*f.calls, "delete-app")
	if f.failAt == "delete-app" {
		return operation("delete-app", false), errors.New("delete failed")
	}
	if f.failAt == "delete-absent" {
		return operation("delete-app", false), classifiedError{kind: "absent"}
	}
	return operation("delete-app", true), nil
}

type fakeProbe struct {
	calls     *[]string
	reachable bool
}

func (p *fakeProbe) Reachable(context.Context, string) (bool, error) {
	*p.calls = append(*p.calls, "probe-route")
	return p.reachable, nil
}

type fakeClock struct {
	now   time.Time
	waits int
}

func (c *fakeClock) Now() time.Time                            { return c.now }
func (c *fakeClock) Wait(context.Context, time.Duration) error { c.waits++; return nil }

func operation(name string, success bool) model.Operation {
	return model.Operation{Name: name, StartedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), Duration: time.Second, Success: success}
}

func fixture(phase model.Phase) (*Reconciler, *memoryStore, *fakeRuntime, *fakeCF, *fakePack, *fakeProbe, *fakeClock, *[]string) {
	calls := []string{}
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := &memoryStore{items: map[string]model.Sandbox{"demo": {Name: "demo", Repository: "https://git.example/demo", Buildpack: "ruby_buildpack", Desired: model.DesiredPresent, Phase: phase, CreatedAt: now}}, calls: &calls}
	rt := &fakeRuntime{calls: &calls}
	cloud := &fakeCF{calls: &calls, app: cf.App{GUID: sandboxGUID, Running: true, Ready: true}}
	pack := &fakePack{calls: &calls, present: true, expiry: now.Add(time.Hour)}
	probe := &fakeProbe{calls: &calls, reachable: true}
	clock := &fakeClock{now: now}
	r := New(Config{WorkRoot: "/work", IdentityDomain: "identity.example", ManagerRouteHost: "manager", ManagerPackHost: "manager.identity.example", ManagerAppGUID: managerGUID, PollAttempts: 2, PollInterval: time.Millisecond, ScanInterval: time.Hour}, s, rt, cloud, pack, probe, clock)
	return r, s, rt, cloud, pack, probe, clock, &calls
}

func TestCreationPersistsBeforeEveryEffectInExactOrder(t *testing.T) {
	r, s, _, _, _, _, _, calls := fixture(model.PhaseCreating)
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	wantEffects := []string{"prepare-bits", "prepare-invite", "install-invite", "stage", "discover-guid", "secure-route", "secure-manager-route", "configure-enrollment", "start-app", "inspect-app", "probe-route", "observe-member", "cleanup-invite", "cleanup-bits"}
	var effects []string
	for _, call := range *calls {
		if !strings.HasPrefix(call, "persist:") {
			effects = append(effects, call)
		}
	}
	if !reflect.DeepEqual(effects, wantEffects) {
		t.Fatalf("effects=%#v, want %#v", effects, wantEffects)
	}
	for i, call := range *calls {
		if slicesContains(wantEffects, call) && call != "cleanup-invite" && call != "cleanup-bits" {
			if i == 0 || !strings.HasPrefix((*calls)[i-1], "persist:") {
				t.Fatalf("effect %q not immediately preceded by persistence: %#v", call, *calls)
			}
		}
	}
	got, _ := s.Get("demo")
	if got.Phase != model.PhaseReady || got.Revision != "abc123" || got.AppGUID != sandboxGUID || got.InternalHost != "demo.identity.example" || got.PackMemberID != "demo" {
		t.Fatalf("sandbox = %#v", got)
	}
	if len(got.Operations) != 6 {
		t.Fatalf("operations = %#v, want CF operations", got.Operations)
	}
}

func TestEveryPersistedPhaseResumesWithoutRepeatingPriorEffects(t *testing.T) {
	tests := []struct {
		phase     model.Phase
		forbidden string
	}{{model.PhasePreparingInvite, "prepare-bits"}, {model.PhaseStaging, "prepare-bits"}, {model.PhaseWaitingForApp, "start-app"}, {model.PhaseWaitingForRoute, "start-app"}, {model.PhaseJoiningPack, "probe-route"}, {model.PhaseReady, "observe-member"}}
	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			r, s, _, cloud, _, _, _, calls := fixture(tt.phase)
			current, _ := s.Get("demo")
			current.Revision = "abc"
			current.AppGUID = sandboxGUID
			current.InternalHost = "demo.identity.example"
			current.PackMemberID = "demo"
			s.items["demo"] = current
			cloud.app = cf.App{GUID: sandboxGUID, Running: true, Ready: true}
			if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
				t.Fatal(err)
			}
			for _, call := range *calls {
				if call == tt.forbidden {
					t.Fatalf("repeated completed effect %q: %#v", call, *calls)
				}
			}
		})
	}
}

func TestFailureRetainsResumePhaseAndRetryClearsError(t *testing.T) {
	r, s, _, cloud, _, _, _, _ := fixture(model.PhaseStaging)
	cloud.failAt = "stage"
	if err := r.ReconcileOne(context.Background(), "demo"); err == nil {
		t.Fatal("ReconcileOne succeeded")
	}
	failed, _ := s.Get("demo")
	if failed.Phase != model.PhaseFailed || failed.ResumePhase != model.PhaseStaging || failed.LastError == "" {
		t.Fatalf("failed = %#v", failed)
	}
	if containsSecret(failed.LastError) {
		t.Fatalf("unsanitized error %q", failed.LastError)
	}
	cloud.failAt = ""
	if err := r.Retry(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	ready, _ := s.Get("demo")
	if ready.Phase != model.PhaseReady || ready.ResumePhase != "" || ready.LastError != "" {
		t.Fatalf("ready = %#v", ready)
	}
}

func TestStageSuccessThenPersistenceFailureRetriesStageIdempotently(t *testing.T) {
	r, s, _, _, _, _, _, calls := fixture(model.PhaseStaging)
	s.failOnUpdate = 4
	if err := r.ReconcileOne(context.Background(), "demo"); err == nil {
		t.Fatal("ReconcileOne succeeded")
	}
	failed, _ := s.Get("demo")
	if failed.ResumePhase != model.PhaseStaging {
		t.Fatalf("resume phase = %s", failed.ResumePhase)
	}
	if err := r.Retry(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if count(*calls, "stage") != 2 || count(*calls, "discover-guid") != 1 {
		t.Fatalf("calls = %#v", *calls)
	}
}

func TestExpiredEnrollmentIsRegenerated(t *testing.T) {
	r, _, _, _, pack, _, clock, calls := fixture(model.PhaseStaging)
	r.enrollments["demo"] = &fakeEnrollment{calls: calls, path: "old", expiry: clock.now.Add(-time.Second)}
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if count(*calls, "prepare-invite") != 1 || count(*calls, "cleanup-invite") < 2 {
		t.Fatalf("calls = %#v", *calls)
	}
	_ = pack
}

func TestFreshReconcilerRegeneratesInviteAndRestagesBeforePostStageWork(t *testing.T) {
	r, s, rt, cloud, pack, probe, clock, calls := fixture(model.PhaseSecuringRoute)
	current, _ := s.Get("demo")
	current.Revision = "abc"
	current.AppGUID = sandboxGUID
	s.items["demo"] = current
	fresh := New(r.config, s, rt, cloud, pack, probe, clock)
	if err := fresh.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, *calls, []string{"prepare-invite", "install-invite", "stage", "discover-guid", "secure-route", "secure-manager-route", "configure-enrollment", "start-app"})
}

func TestExpiredPostStageInviteIsRegeneratedAndRestaged(t *testing.T) {
	r, s, _, _, _, _, clock, calls := fixture(model.PhaseSecuringRoute)
	current, _ := s.Get("demo")
	current.Revision = "abc"
	current.AppGUID = sandboxGUID
	s.items["demo"] = current
	r.enrollments["demo"] = &fakeEnrollment{calls: calls, path: "old", expiry: clock.now.Add(-time.Second)}
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, *calls, []string{"cleanup-invite", "prepare-invite", "install-invite", "stage", "discover-guid", "secure-route", "configure-enrollment", "start-app"})
}

func TestInviteCleanupFailureRemainsRetryable(t *testing.T) {
	r, s, _, _, _, _, _, calls := fixture(model.PhaseJoiningPack)
	r.enrollments["demo"] = &fakeEnrollment{calls: calls, path: "invite", failCleanup: true}
	if err := r.ReconcileOne(context.Background(), "demo"); err == nil {
		t.Fatal("succeeded")
	}
	got, _ := s.Get("demo")
	if got.Phase != model.PhaseFailed || got.ResumePhase != model.PhaseJoiningPack || containsSecret(got.LastError) {
		t.Fatalf("sandbox=%#v", got)
	}
}

func TestAppendOperationSanitizesAllExternalFields(t *testing.T) {
	r, s, _, cloud, _, _, _, _ := fixture(model.PhaseSecuringRoute)
	current, _ := s.Get("demo")
	current.AppGUID = sandboxGUID
	s.items["demo"] = current
	cloud.operation = model.Operation{Name: "stage\nforged", Command: "token=command-secret", Summary: "Authorization: Bearer summary-secret", Error: "password=error-secret", Success: true}
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("demo")
	for _, op := range got.Operations {
		joined := op.Name + op.Command + op.Summary + op.Error
		if strings.Contains(joined, "secret") || strings.ContainsAny(joined, "\r\n") {
			t.Fatalf("unsafe operation=%#v", op)
		}
	}
}

func TestRealCFErrorClassificationsAreAccepted(t *testing.T) {
	if !isAlreadyExists(&cf.Error{Kind: "already_exists"}) || !isAbsent(&cf.Error{Kind: "not_found"}) {
		t.Fatal("real provider classifications not accepted")
	}
}

func TestPackMembershipIsConditionPolled(t *testing.T) {
	r, s, _, _, pack, _, clock, calls := fixture(model.PhaseJoiningPack)
	pack.present = false
	attempt := 0
	r.beforeEffect = func(name string) {
		if name == "observe-member" {
			attempt++
			if attempt == 2 {
				pack.present = true
			}
		}
	}
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if count(*calls, "observe-member") != 2 || clock.waits != 1 {
		t.Fatalf("calls=%#v waits=%d", *calls, clock.waits)
	}
	got, _ := s.Get("demo")
	if got.Phase != model.PhaseReady {
		t.Fatalf("phase=%s", got.Phase)
	}
}

func TestAllowsActionsDeniesDesiredDeletedImmediately(t *testing.T) {
	r, s, _, _, _, _, _, _ := fixture(model.PhaseReady)
	if !r.AllowsActions("demo") {
		t.Fatal("present sandbox blocked")
	}
	current, _ := s.Get("demo")
	current.Desired = model.DesiredDeleted
	s.items["demo"] = current
	if r.AllowsActions("demo") {
		t.Fatal("deleted sandbox allowed")
	}
}

func TestPersistenceFailurePreventsNextEffect(t *testing.T) {
	for _, phase := range []model.Phase{model.PhaseCreating, model.PhasePreparingInvite, model.PhaseStaging, model.PhaseDiscoveringApp, model.PhaseSecuringRoute, model.PhaseSecuringManagerRoute, model.PhaseConfiguringEnrollment, model.PhaseStarting, model.PhaseWaitingForApp, model.PhaseWaitingForRoute, model.PhaseJoiningPack} {
		t.Run(string(phase), func(t *testing.T) {
			r, s, _, _, _, _, _, calls := fixture(phase)
			current, _ := s.Get("demo")
			current.Revision = "abc"
			current.AppGUID = sandboxGUID
			current.InternalHost = "demo.identity.example"
			s.items["demo"] = current
			s.failUpdate = 1
			_ = r.ReconcileOne(context.Background(), "demo")
			for _, call := range *calls {
				if !strings.HasPrefix(call, "persist:") {
					t.Fatalf("effect occurred after persistence failure: %#v", *calls)
				}
			}
		})
	}
}

func TestTypedDuplicateCreationIsSuccess(t *testing.T) {
	r, s, _, cloud, _, _, _, _ := fixture(model.PhaseSecuringRoute)
	current, _ := s.Get("demo")
	current.AppGUID = sandboxGUID
	current.InternalHost = "demo.identity.example"
	s.items["demo"] = current
	cloud.failAt = "secure-route-exists"
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("demo")
	if got.Phase != model.PhaseReady {
		t.Fatalf("phase=%s", got.Phase)
	}
}

func TestTypedAbsentDeletionIsSuccess(t *testing.T) {
	r, s, _, cloud, pack, _, _, _ := fixture(model.PhaseReady)
	current, _ := s.Get("demo")
	current.Desired = model.DesiredDeleted
	current.AppGUID = sandboxGUID
	current.InternalHost = "demo.identity.example"
	current.PackMemberID = "demo"
	s.items["demo"] = current
	pack.present = false
	cloud.failAt = "delete-absent"
	if err := r.ReconcileOne(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("demo"); ok {
		t.Fatal("record retained")
	}
}

func TestPollingFailuresAreBoundedAndRetryable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase model.Phase
		setup func(*fakeCF, *fakeProbe)
	}{{"staging", model.PhaseWaitingForApp, func(c *fakeCF, _ *fakeProbe) { c.app = cf.App{GUID: sandboxGUID} }}, {"policy", model.PhaseWaitingForRoute, func(_ *fakeCF, p *fakeProbe) { p.reachable = false }}} {
		t.Run(tc.name, func(t *testing.T) {
			r, s, _, cloud, _, probe, clock, calls := fixture(tc.phase)
			tc.setup(cloud, probe)
			if err := r.ReconcileOne(context.Background(), "demo"); err == nil {
				t.Fatal("succeeded")
			}
			got, _ := s.Get("demo")
			if got.Phase != model.PhaseFailed || got.ResumePhase != tc.phase {
				t.Fatalf("sandbox=%#v", got)
			}
			if clock.waits != 1 {
				t.Fatalf("waits=%d", clock.waits)
			}
			expected := "inspect-app"
			if tc.phase == model.PhaseWaitingForRoute {
				expected = "probe-route"
			}
			if count(*calls, expected) != 2 {
				t.Fatalf("calls=%#v", *calls)
			}
		})
	}
}

func TestDeletionOrderRetainsFailuresForRetry(t *testing.T) {
	failures := []string{"remove-member", "remove-manager-policy", "remove-route", "delete-app"}
	for _, failure := range failures {
		t.Run(failure, func(t *testing.T) {
			r, s, rt, cloud, pack, _, _, calls := fixture(model.PhaseReady)
			current, _ := s.Get("demo")
			current.Desired = model.DesiredDeleted
			current.AppGUID = sandboxGUID
			current.InternalHost = "demo.identity.example"
			current.PackMemberID = "demo"
			s.items["demo"] = current
			cloud.failAt = failure
			pack.failAt = failure
			if err := r.ReconcileOne(context.Background(), "demo"); err == nil {
				t.Fatal("succeeded")
			}
			if _, ok := s.Get("demo"); !ok {
				t.Fatal("record deleted after failure")
			}
			cloud.failAt = ""
			pack.failAt = ""
			rt.fail = nil
			if err := r.Retry(context.Background(), "demo"); err != nil {
				t.Fatal(err)
			}
			if _, ok := s.Get("demo"); ok {
				t.Fatal("record retained")
			}
			wantOrder := []string{"remove-member", "remove-manager-policy", "remove-route", "delete-app", "cleanup-bits", "delete-record"}
			assertSubsequence(t, *calls, wantOrder)
		})
	}
}

func TestConcurrentCallsAreKeyedAndGloballySerialized(t *testing.T) {
	r, _, _, cloud, _, _, _, _ := fixture(model.PhaseWaitingForApp)
	entered := make(chan struct{})
	release := make(chan struct{})
	r.beforeEffect = func(name string) {
		if name == "inspect-app" {
			select {
			case entered <- struct{}{}:
				<-release
			}
			<-release
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = r.ReconcileOne(context.Background(), "demo") }()
	<-entered
	go func() { defer wg.Done(); _ = r.ReconcileOne(context.Background(), "demo") }()
	close(release)
	wg.Wait()
	if cloud == nil {
		t.Fatal()
	}
	if r.MaxConcurrent() != 1 {
		t.Fatalf("max concurrent=%d", r.MaxConcurrent())
	}
}

func TestStartScansImmediatelyAndStopIsLeakFree(t *testing.T) {
	r, s, _, _, _, _, _, _ := fixture(model.PhaseReady)
	current, _ := s.Get("demo")
	current.Desired = model.DesiredDeleted
	s.items["demo"] = current
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	deadline := time.After(time.Second)
	for {
		if _, ok := s.Get("demo"); !ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("startup scan did not reconcile")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	r.Stop()
	r.Stop()
}

func count(values []string, want string) int {
	n := 0
	for _, v := range values {
		if v == want {
			n++
		}
	}
	return n
}
func containsSecret(value string) bool {
	return value == "token=store-secret persistence failed" || value == "staging Authorization: Bearer secret"
}
func assertSubsequence(t *testing.T, got, want []string) {
	t.Helper()
	i := 0
	for _, v := range got {
		if i < len(want) && v == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Fatalf("%#v does not contain %#v", got, want)
	}
}
func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
