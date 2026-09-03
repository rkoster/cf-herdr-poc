package runner

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExecCapsCombinedOutput(t *testing.T) {
	output, err := (Exec{}).Run(context.Background(), "sh", "-c", "yes x | head -c 80000; printf FINAL_DIAGNOSTIC")
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > MaxOutputBytes {
		t.Fatalf("output length = %d, want <= %d", len(output), MaxOutputBytes)
	}
	if !strings.HasPrefix(string(output), outputTruncatedMarker) || !strings.Contains(string(output), "FINAL_DIAGNOSTIC") {
		t.Fatalf("output does not retain diagnostic tail: %q", output)
	}
}

func TestExecLeavesNormalOutputUnchanged(t *testing.T) {
	output, err := (Exec{}).Run(context.Background(), "sh", "-c", "printf 'normal output'")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "normal output" {
		t.Fatalf("output = %q, want normal output", output)
	}
}

func TestBoundedWriterHandlesConcurrentWrites(t *testing.T) {
	buffer := newCappedBuffer(MaxOutputBytes)
	const writes = 100
	var wait sync.WaitGroup
	for stream, value := range map[string]string{"stdout": "O", "stderr": "E"} {
		stream, value := stream, value
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := 0; i < writes; i++ {
				if _, err := buffer.Write([]byte(stream + value + "\n")); err != nil {
					t.Errorf("Write() error = %v", err)
				}
			}
		}()
	}
	wait.Wait()
	output := string(buffer.Bytes())
	if strings.Count(output, "stdoutO\n") != writes || strings.Count(output, "stderrE\n") != writes {
		t.Fatalf("concurrent output lost writes")
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

func TestExecCancellationDoesNotWaitForDescendantOutputPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell descendant process semantics are Unix-specific")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (Exec{}).Run(ctx, "sh", "-c", "sleep 3 &")
	if err == nil {
		t.Fatal("Run succeeded after context cancellation")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run returned after %v, want prompt cancellation", elapsed)
	}
}
