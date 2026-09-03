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
