package runner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExecCapsCombinedOutput(t *testing.T) {
	output, err := (Exec{}).Run(context.Background(), "sh", "-c", "yes x | head -c 100000")
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > MaxOutputBytes {
		t.Fatalf("output length = %d, want <= %d", len(output), MaxOutputBytes)
	}
}

func TestExecHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (Exec{}).Run(ctx, "sh", "-c", "sleep 10")
	if err == nil {
		t.Fatal("Run succeeded after context cancellation")
	}
	var exitErr *exec.ExitError
	if !errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &exitErr) && !strings.Contains(err.Error(), "killed") {
		t.Fatalf("Run error = %v, want cancellation", err)
	}
}
