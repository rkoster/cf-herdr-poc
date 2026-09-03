package runner

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

const MaxOutputBytes = 64 * 1024
const waitDelay = 200 * time.Millisecond
const outputTruncatedMarker = "[older command output truncated]\n"

type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = waitDelay
	configureCancellation(command)
	output := newCappedBuffer(MaxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return output.Bytes(), err
}

type cappedBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(value)
	b.data = append(b.data, value...)
	payloadLimit := b.limit
	if b.truncated || len(b.data) > b.limit {
		b.truncated = true
		payloadLimit -= len(outputTruncatedMarker)
	}
	if len(b.data) > payloadLimit {
		b.data = append(b.data[:0], b.data[len(b.data)-payloadLimit:]...)
	}
	return written, nil
}

func (b *cappedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := make([]byte, 0, b.limit)
	if b.truncated {
		result = append(result, outputTruncatedMarker...)
	}
	return append(result, b.data...)
}
