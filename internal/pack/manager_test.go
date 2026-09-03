package pack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type call struct {
	name string
	args []string
}
type fakeRunner struct {
	output []byte
	err    error
	calls  []call
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, call{name, append([]string(nil), args...)})
	return append([]byte(nil), r.output...), r.err
}

type fakeSupervisor struct {
	restarts int
	err      error
}

func (*fakeSupervisor) Start(context.Context) error     { return nil }
func (s *fakeSupervisor) Restart(context.Context) error { s.restarts++; return s.err }
func (*fakeSupervisor) Ready(context.Context) error     { return nil }
func (*fakeSupervisor) Stop(context.Context) error      { return nil }

const invite = "uY4Jf9P2_abc-DEF.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestPrepareEnrollmentCreatesPrivateTokenFileAndRestarts(t *testing.T) {
	temp := t.TempDir()
	r := &fakeRunner{output: []byte(invite + "\n\n  single-use · expires 2026-09-03T18:00:00.000Z (10 minutes)\n")}
	s := &fakeSupervisor{}
	m := New(r, s, Config{Executable: "collie", TempDir: temp})
	handle, err := m.PrepareEnrollment(context.Background(), "pack.apps.example", "sandbox-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls, []call{{"collie", []string{"pack", "invite", "--address", "https://pack.apps.example"}}}) {
		t.Fatalf("calls = %#v", r.calls)
	}
	if s.restarts != 1 {
		t.Fatalf("restarts = %d", s.restarts)
	}
	info, err := os.Stat(handle.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	contents, _ := os.ReadFile(handle.Path)
	if string(contents) != invite+"\n" {
		t.Fatalf("token file = %q", contents)
	}
	if handle.ExpiresAt.IsZero() {
		t.Fatal("expiry was not parsed")
	}
	if err := handle.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(handle.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("token still exists: %v", err)
	}
}

func TestPrepareEnrollmentRedactsTokenFromErrorsAndCleansUp(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n"), err: errors.New("failed: " + invite)}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir()})
	_, err := m.PrepareEnrollment(context.Background(), "pack.example", "sandbox-a")
	if err == nil || strings.Contains(err.Error(), invite) {
		t.Fatalf("error leaked token: %v", err)
	}
	entries, _ := os.ReadDir(m.config.TempDir)
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}
}

func TestEnrollmentFileHasBoundedLifetime(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n")}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir(), TokenLifetime: 15 * time.Millisecond})
	h, err := m.PrepareEnrollment(context.Background(), "pack.example", "sandbox-a")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, err = os.Stat(h.Path)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("token file outlived configured lifetime")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMemberPresentUsesNoProbeAndNarrowRosterParsing(t *testing.T) {
	r := &fakeRunner{output: []byte("pack p (id)\nmembers:\n  sandbox-a  (peer)  sandbox.example\n    pinned abc\n")}
	m := New(r, &fakeSupervisor{}, Config{Executable: "collie", TempDir: t.TempDir()})
	present, err := m.MemberPresent(context.Background(), "sandbox-a")
	if err != nil || !present {
		t.Fatalf("present = %v, err = %v", present, err)
	}
	if !reflect.DeepEqual(r.calls[0].args, []string{"pack", "status", "--no-probe"}) {
		t.Fatalf("args = %v", r.calls[0].args)
	}
}

func TestRemoveMemberValidatesIDAndRestarts(t *testing.T) {
	r := &fakeRunner{}
	s := &fakeSupervisor{}
	m := New(r, s, Config{Executable: "collie", TempDir: t.TempDir()})
	if err := m.RemoveMember(context.Background(), "bad id; rm -rf"); err == nil {
		t.Fatal("invalid member ID accepted")
	}
	if len(r.calls) != 0 {
		t.Fatal("runner called for invalid member ID")
	}
	if err := m.RemoveMember(context.Background(), "sandbox-a"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls[0].args, []string{"pack", "remove", "sandbox-a"}) {
		t.Fatalf("args = %v", r.calls[0].args)
	}
	if s.restarts != 1 {
		t.Fatalf("restarts = %d", s.restarts)
	}
}

func TestPrepareEnrollmentUsesUniqueFiles(t *testing.T) {
	r := &fakeRunner{output: []byte(invite + "\n")}
	m := New(r, &fakeSupervisor{}, Config{TempDir: t.TempDir()})
	a, _ := m.PrepareEnrollment(context.Background(), "pack.example", filepath.Base("sandbox"))
	b, _ := m.PrepareEnrollment(context.Background(), "pack.example", filepath.Base("sandbox"))
	defer a.Cleanup()
	defer b.Cleanup()
	if a.Path == b.Path {
		t.Fatal("token files are not unique")
	}
}
