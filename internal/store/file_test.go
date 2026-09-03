package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cf-herdr-poc/internal/model"
)

func TestFileRoundTripPreservesSandboxState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandboxes.json")
	created := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	want := model.Sandbox{
		Name:         "demo",
		AppGUID:      "app-guid",
		Repository:   "https://git.example/demo.git",
		Revision:     "abc123",
		Buildpack:    "ruby_buildpack",
		Desired:      model.DesiredPresent,
		Phase:        model.PhaseReady,
		InternalHost: "demo.apps.internal",
		PackMemberID: "member-id",
		Operations: []model.Operation{{
			Name:      "push",
			StartedAt: created,
			Duration:  2 * time.Second,
			Success:   true,
		}},
		CreatedAt: created,
		UpdatedAt: created.Add(time.Minute),
	}

	first := NewFile(path)
	if err := first.Load(); err != nil {
		t.Fatal(err)
	}
	if err := first.Create(want); err != nil {
		t.Fatal(err)
	}

	second := NewFile(path)
	if err := second.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := second.Get("demo")
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Get(demo) = (%#v, %v), want (%#v, true)", got, ok, want)
	}
}

func TestLoadMissingFileStartsEmpty(t *testing.T) {
	store := NewFile(filepath.Join(t.TempDir(), "missing.json"))
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("List() = %#v, want empty", got)
	}
}

func TestMalformedLoadFailsWithoutChangingFileOrState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandboxes.json")
	malformed := []byte("{not-json\n")
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFile(path)

	err := store.Load()
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("Load() error = %v, want explicit decode error", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !reflect.DeepEqual(got, malformed) {
		t.Fatalf("file = %q, want unchanged %q", got, malformed)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("List() after failed load = %#v, want empty", got)
	}
}

func TestFailedSandboxIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandboxes.json")
	store := NewFile(path)
	want := sandbox("failed", time.Now().UTC())
	want.Phase = model.PhaseFailed
	want.LastError = "staging failed"
	if err := store.Create(want); err != nil {
		t.Fatal(err)
	}

	reloaded := NewFile(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(want.Name)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Get(%q) = (%#v, %v), want failed sandbox", want.Name, got, ok)
	}
}

func TestUpdateCallbackErrorDoesNotMutateOrPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandboxes.json")
	store := NewFile(path)
	want := sandbox("demo", time.Now().UTC())
	want.Operations = []model.Operation{{Name: "create"}}
	if err := store.Create(want); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	callbackErr := errors.New("stop")
	err = store.Update("demo", func(s *model.Sandbox) error {
		s.Phase = model.PhaseReady
		s.Operations[0].Name = "mutated"
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Update() error = %v, want %v", err, callbackErr)
	}
	got, _ := store.Get("demo")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Get(demo) = %#v, want unchanged %#v", got, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("callback error changed persisted file")
	}
}

func TestFailedWriteDoesNotMutateMemory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "state")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFile(filepath.Join(parent, "sandboxes.json"))
	want := sandbox("demo", time.Now().UTC())

	if err := store.Create(want); err == nil {
		t.Fatal("Create() succeeded, want persistence error")
	}
	if _, ok := store.Get("demo"); ok {
		t.Fatal("failed Create mutated in-memory state")
	}
}

func TestListSortsByCreatedAtThenName(t *testing.T) {
	store := NewFile(filepath.Join(t.TempDir(), "sandboxes.json"))
	base := time.Now().UTC()
	for _, item := range []model.Sandbox{
		sandbox("z-last", base.Add(time.Minute)),
		sandbox("b-second", base),
		sandbox("a-first", base),
	} {
		if err := store.Create(item); err != nil {
			t.Fatal(err)
		}
	}

	got := store.List()
	want := []string{"a-first", "b-second", "z-last"}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("List()[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestReturnedSandboxesDoNotExposeOperationSlices(t *testing.T) {
	store := NewFile(filepath.Join(t.TempDir(), "sandboxes.json"))
	want := sandbox("demo", time.Now().UTC())
	want.Operations = []model.Operation{{Name: "original"}}
	if err := store.Create(want); err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get("demo")
	got.Operations[0].Name = "get-mutated"
	listed := store.List()
	listed[0].Operations[0].Name = "list-mutated"

	unchanged, _ := store.Get("demo")
	if unchanged.Operations[0].Name != "original" {
		t.Fatalf("internal operation = %q, want original", unchanged.Operations[0].Name)
	}
}

func TestCreateDeleteAndDuplicateErrors(t *testing.T) {
	store := NewFile(filepath.Join(t.TempDir(), "sandboxes.json"))
	want := sandbox("demo", time.Now().UTC())
	if err := store.Create(want); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(want); err == nil {
		t.Fatal("duplicate Create() succeeded")
	}
	if err := store.Delete("demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("demo"); ok {
		t.Fatal("Get(demo) found deleted sandbox")
	}
	if err := store.Delete("demo"); err == nil {
		t.Fatal("Delete missing sandbox succeeded")
	}
	if err := store.Update("demo", func(*model.Sandbox) error { return nil }); err == nil {
		t.Fatal("Update missing sandbox succeeded")
	}
}

func TestConcurrentUpdates(t *testing.T) {
	store := NewFile(filepath.Join(t.TempDir(), "sandboxes.json"))
	want := sandbox("demo", time.Now().UTC())
	if err := store.Create(want); err != nil {
		t.Fatal(err)
	}

	const updates = 20
	var wg sync.WaitGroup
	errs := make(chan error, updates)
	for range updates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.Update("demo", func(s *model.Sandbox) error {
				s.Operations = append(s.Operations, model.Operation{Name: "update"})
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.Get("demo")
	if len(got.Operations) != updates {
		t.Fatalf("len(Operations) = %d, want %d", len(got.Operations), updates)
	}
}

func TestStateFilePermissionsAndNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandboxes.json")
	store := NewFile(path)
	if err := store.Create(sandbox("demo", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state mode = %o, want 600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "sandboxes.json" {
		t.Fatalf("directory entries = %#v, want state file only", entries)
	}
}

func sandbox(name string, created time.Time) model.Sandbox {
	return model.Sandbox{
		Name:       name,
		Repository: "https://git.example/" + name + ".git",
		Buildpack:  "ruby_buildpack",
		Desired:    model.DesiredPresent,
		Phase:      model.PhaseCreating,
		CreatedAt:  created,
		UpdatedAt:  created,
	}
}
