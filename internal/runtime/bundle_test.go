package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cf-herdr-poc/internal/runner"
)

type command struct {
	name string
	args []string
}

type recordingRunner struct {
	commands []command
	run      func(name string, args []string) ([]byte, error)
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	copyArgs := append([]string(nil), args...)
	r.commands = append(r.commands, command{name: name, args: copyArgs})
	if r.run != nil {
		return r.run(name, copyArgs)
	}
	return nil, nil
}

func TestPrepareClonesBeforeOverlayAndReturnsRevision(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	runtimeDir := t.TempDir()
	writeFile(t, filepath.Join(runtimeDir, "start.sh"), 0o755, "#!/bin/sh\n")
	writeFile(t, filepath.Join(runtimeDir, "config", "default.json"), 0o640, "{}\n")

	recorder := &recordingRunner{}
	recorder.run = func(name string, args []string) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"clone", "--depth", "1", "--", "https://git.example/demo.git", destination}) {
			if _, err := os.Stat(filepath.Join(destination, ".sandbox")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("runtime overlay existed before clone completed: %v", err)
			}
			if err := os.MkdirAll(destination, 0o755); err != nil {
				t.Fatal(err)
			}
			return nil, nil
		}
		if reflect.DeepEqual(args, []string{"-C", destination, "rev-parse", "HEAD"}) {
			return []byte("abc123\n"), nil
		}
		return nil, errors.New("unexpected command")
	}

	result, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != "abc123" {
		t.Fatalf("Revision = %q, want abc123", result.Revision)
	}
	wantCommands := []command{
		{name: "git", args: []string{"clone", "--depth", "1", "--", "https://git.example/demo.git", destination}},
		{name: "git", args: []string{"-C", destination, "rev-parse", "HEAD"}},
	}
	if !reflect.DeepEqual(recorder.commands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", recorder.commands, wantCommands)
	}
	for path, mode := range map[string]os.FileMode{"start.sh": 0o755, filepath.Join("config", "default.json"): 0o640} {
		info, err := os.Stat(filepath.Join(destination, ".sandbox", path))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("mode for %s = %o, want %o", path, got, mode)
		}
	}
}

func TestInstallEnrollmentCopiesPrivateTokenIntoPreparedRuntime(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(destination, ".sandbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "invite")
	if err := os.WriteFile(source, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Builder{WorkRoot: root}).InstallEnrollment(destination, source); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destination, ".sandbox", "join-token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestPrepareRejectsUnsafeInputsBeforeRunningCommands(t *testing.T) {
	workRoot := t.TempDir()
	tests := []struct {
		name        string
		repository  string
		destination string
	}{
		{name: "option-like repository", repository: "--upload-pack=bad", destination: filepath.Join(workRoot, "demo")},
		{name: "work root", repository: "https://git.example/demo.git", destination: workRoot},
		{name: "lexical escape", repository: "https://git.example/demo.git", destination: filepath.Join(workRoot, "..", "escape")},
		{name: "absolute escape", repository: "https://git.example/demo.git", destination: filepath.Join(workRoot, "demo", "..", "..", "escape")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &recordingRunner{}
			_, err := (Builder{Run: recorder, RuntimeDir: t.TempDir(), WorkRoot: workRoot}).Prepare(
				context.Background(), tt.repository, tt.destination,
			)
			if err == nil {
				t.Fatal("Prepare succeeded, want error")
			}
			if len(recorder.commands) != 0 {
				t.Fatalf("ran commands for unsafe input: %#v", recorder.commands)
			}
		})
	}
}

func TestPrepareRejectsRuntimeSymlink(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	runtimeDir := t.TempDir()
	if err := os.Symlink("target", filepath.Join(runtimeDir, "link")); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingRunner{run: func(_ string, args []string) ([]byte, error) {
		if args[0] == "clone" {
			return nil, os.MkdirAll(destination, 0o755)
		}
		return []byte("abc123\n"), nil
	}}

	_, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink rejection", err)
	}
	if len(recorder.commands) != 1 || recorder.commands[0].args[0] != "clone" {
		t.Fatalf("commands = %#v, want clone only", recorder.commands)
	}
}

func TestPrepareRejectsClonedSandboxSymlinkWithoutWritingOutside(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	outside := t.TempDir()
	runtimeDir := t.TempDir()
	writeFile(t, filepath.Join(runtimeDir, "start.sh"), 0o755, "runtime\n")
	recorder := &recordingRunner{run: func(_ string, args []string) ([]byte, error) {
		if args[0] != "clone" {
			return []byte("abc123\n"), nil
		}
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return nil, err
		}
		return nil, os.Symlink(outside, filepath.Join(destination, ".sandbox"))
	}}

	_, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want sandbox symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "start.sh")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("overlay wrote outside work root: %v", statErr)
	}
}

func TestPrepareRejectsSymlinkInExistingOverlayPath(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	outside := t.TempDir()
	runtimeDir := t.TempDir()
	writeFile(t, filepath.Join(runtimeDir, "config", "default.json"), 0o644, "{}\n")
	recorder := &recordingRunner{run: func(_ string, args []string) ([]byte, error) {
		if args[0] != "clone" {
			return []byte("abc123\n"), nil
		}
		if err := os.MkdirAll(filepath.Join(destination, ".sandbox"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.Symlink(outside, filepath.Join(destination, ".sandbox", "config"))
	}}

	_, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want overlay path symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "default.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("overlay wrote through nested symlink: %v", statErr)
	}
}

func TestPrepareRejectsDestinationThroughSymlinkedAncestor(t *testing.T) {
	workRoot := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workRoot, "redirect")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingRunner{}

	_, err := (Builder{Run: recorder, RuntimeDir: t.TempDir(), WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", filepath.Join(link, "demo"),
	)
	if err == nil {
		t.Fatal("Prepare succeeded through symlinked ancestor, want error")
	}
	if len(recorder.commands) != 0 {
		t.Fatalf("ran commands for physically escaped destination: %#v", recorder.commands)
	}
}

func TestPrepareRemovesCreatedDestinationAfterOverlayFailureAndCanRetry(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	badRuntime := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(badRuntime, "link")); err != nil {
		t.Fatal(err)
	}
	goodRuntime := t.TempDir()
	writeFile(t, filepath.Join(goodRuntime, "start.sh"), 0o755, "runtime\n")
	recorder := cloneRunner(destination, nil)

	_, err := (Builder{Run: recorder, RuntimeDir: badRuntime, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err == nil {
		t.Fatal("Prepare succeeded with invalid runtime")
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after overlay failure: %v", statErr)
	}
	if _, err := (Builder{Run: recorder, RuntimeDir: goodRuntime, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	); err != nil {
		t.Fatalf("retry Prepare failed: %v", err)
	}
}

func TestPrepareRemovesCreatedDestinationAfterRevisionFailureAndCanRetry(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	runtimeDir := t.TempDir()
	writeFile(t, filepath.Join(runtimeDir, "start.sh"), 0o755, "runtime\n")
	failRevision := true
	recorder := cloneRunner(destination, func() ([]byte, error) {
		if failRevision {
			failRevision = false
			return nil, errors.New("revision failed")
		}
		return []byte("abc123\n"), nil
	})

	_, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err == nil {
		t.Fatal("Prepare succeeded with revision failure")
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after revision failure: %v", statErr)
	}
	if _, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	); err != nil {
		t.Fatalf("retry Prepare failed: %v", err)
	}
}

func TestPrepareNeverRemovesPreExistingDestination(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	writeFile(t, filepath.Join(destination, "keep.txt"), 0o644, "keep\n")
	recorder := &recordingRunner{run: func(_ string, _ []string) ([]byte, error) {
		return nil, errors.New("clone failed")
	}}

	_, err := (Builder{Run: recorder, RuntimeDir: t.TempDir(), WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	)
	if err == nil {
		t.Fatal("Prepare succeeded, want clone failure")
	}
	contents, readErr := os.ReadFile(filepath.Join(destination, "keep.txt"))
	if readErr != nil || string(contents) != "keep\n" {
		t.Fatalf("pre-existing destination changed: contents %q, error %v", contents, readErr)
	}
}

func cloneRunner(destination string, revision func() ([]byte, error)) *recordingRunner {
	return &recordingRunner{run: func(_ string, args []string) ([]byte, error) {
		if args[0] == "clone" {
			return nil, os.MkdirAll(destination, 0o755)
		}
		if revision != nil {
			return revision()
		}
		return []byte("abc123\n"), nil
	}}
}

func writeFile(t *testing.T, path string, mode os.FileMode, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

var _ runner.Runner = (*recordingRunner)(nil)
