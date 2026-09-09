package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandJoinerIncludesSanitizedCollieError(t *testing.T) {
	script := filepath.Join(t.TempDir(), "collie")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' 'error: unable to connect token=super-secret' >&2\nexit 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := (commandJoiner{executable: script}).Join(context.Background(), []string{"pack", "join"}, strings.NewReader("token"))
	if err == nil || !strings.Contains(err.Error(), "unable to connect") || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error=%v", err)
	}
}
