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
	writeSandboxRuntime(t, runtimeDir)
	writeFile(t, filepath.Join(runtimeDir, "config", "default.json"), 0o640, "{}\n")

	recorder := &recordingRunner{}
	recorder.run = func(name string, args []string) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"clone", "--depth", "1", "--", "https://git.example/demo.git", destination}) {
			if _, err := os.Stat(filepath.Join(destination, "sandbox-runtime")); !errors.Is(err, os.ErrNotExist) {
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
		info, err := os.Stat(filepath.Join(destination, "sandbox-runtime", path))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("mode for %s = %o, want %o", path, got, mode)
		}
	}
}

func TestPrepareOverlaysSandboxRuntimeWithStartScript(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	runtimeDir := t.TempDir()
	writeSandboxRuntime(t, runtimeDir)
	recorder := cloneRunner(destination, nil)

	if _, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "sandbox-runtime", "start.sh")); err != nil {
		t.Fatalf("sandbox runtime start script missing: %v", err)
	}
}

func TestValidateSandboxRuntimeRequiresAllStartupAssets(t *testing.T) {
	runtimeDir := t.TempDir()
	writeSandboxRuntime(t, runtimeDir)
	if err := os.Remove(filepath.Join(runtimeDir, "bin", "sandbox-bootstrap")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSandboxRuntime(runtimeDir); err == nil || !strings.Contains(err.Error(), "sandbox-bootstrap") {
		t.Fatalf("ValidateSandboxRuntime() error = %v, want missing sandbox-bootstrap asset", err)
	}
}

func TestInstallEnrollmentCopiesPrivateTokenIntoPreparedRuntime(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(destination, "sandbox-runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "invite")
	if err := os.WriteFile(source, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Builder{WorkRoot: root}).InstallEnrollment(destination, source); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destination, "sandbox-runtime", "join-token"))
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

func TestPrepareRejectsBrokenRuntimeSymlink(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	runtimeDir := t.TempDir()
	writeSandboxRuntime(t, runtimeDir)
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

func TestPrepareRejectsRuntimeSymlinksThatEscapeOrAreAbsolute(t *testing.T) {
	for _, target := range []string{"../outside", "/etc/passwd"} {
		t.Run(target, func(t *testing.T) {
			workRoot := t.TempDir()
			destination := filepath.Join(workRoot, "demo")
			runtimeDir := t.TempDir()
			writeSandboxRuntime(t, runtimeDir)
			if err := os.Symlink(target, filepath.Join(runtimeDir, "link")); err != nil {
				t.Fatal(err)
			}
			recorder := cloneRunner(destination, nil)

			_, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
				context.Background(), "https://git.example/demo.git", destination,
			)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("error = %v, want symlink rejection", err)
			}
		})
	}
}

func TestPreparePreservesInternalRuntimeSymlink(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	runtimeDir := t.TempDir()
	writeSandboxRuntime(t, runtimeDir)
	libs := filepath.Join(runtimeDir, "bin", ".bun-libs")
	writeFile(t, filepath.Join(libs, ".real-libc.so.6"), 0o755, "libc\n")
	if err := os.Symlink(".real-libc.so.6", filepath.Join(libs, ".hash-libc.so.6")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".hash-libc.so.6", filepath.Join(libs, "libc.so.6")); err != nil {
		t.Fatal(err)
	}
	recorder := cloneRunner(destination, nil)

	if _, err := (Builder{Run: recorder, RuntimeDir: runtimeDir, WorkRoot: workRoot}).Prepare(
		context.Background(), "https://git.example/demo.git", destination,
	); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(destination, "sandbox-runtime", "bin", ".bun-libs", "libc.so.6")
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != ".hash-libc.so.6" {
		t.Fatalf("runtime symlink target = %q, want %q", got, ".hash-libc.so.6")
	}
}

func TestPrepareRejectsClonedSandboxSymlinkWithoutWritingOutside(t *testing.T) {
	workRoot := t.TempDir()
	destination := filepath.Join(workRoot, "demo")
	outside := t.TempDir()
	runtimeDir := t.TempDir()
	writeSandboxRuntime(t, runtimeDir)
	recorder := &recordingRunner{run: func(_ string, args []string) ([]byte, error) {
		if args[0] != "clone" {
			return []byte("abc123\n"), nil
		}
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return nil, err
		}
		return nil, os.Symlink(outside, filepath.Join(destination, "sandbox-runtime"))
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
	writeSandboxRuntime(t, runtimeDir)
	writeFile(t, filepath.Join(runtimeDir, "config", "default.json"), 0o644, "{}\n")
	recorder := &recordingRunner{run: func(_ string, args []string) ([]byte, error) {
		if args[0] != "clone" {
			return []byte("abc123\n"), nil
		}
		if err := os.MkdirAll(filepath.Join(destination, "sandbox-runtime"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.Symlink(outside, filepath.Join(destination, "sandbox-runtime", "config"))
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
	writeSandboxRuntime(t, goodRuntime)
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
	writeSandboxRuntime(t, runtimeDir)
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

func writeSandboxRuntime(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"start.sh", "bin/bun", "bin/herdr", "bin/collie", "bin/sandbox-bootstrap"} {
		writeFile(t, filepath.Join(root, name), 0o755, "#!/bin/sh\n")
	}
}

var _ runner.Runner = (*recordingRunner)(nil)
