package runner

import (
	"context"
	"os/exec"
	"time"
)

const MaxOutputBytes = 64 * 1024
const waitDelay = 200 * time.Millisecond

type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = waitDelay
	configureCancellation(command)
	output := &cappedBuffer{remaining: MaxOutputBytes}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return output.Bytes(), err
}

type cappedBuffer struct {
	data      []byte
	remaining int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	if b.remaining > 0 {
		keep := len(value)
		if keep > b.remaining {
			keep = b.remaining
		}
		b.data = append(b.data, value[:keep]...)
		b.remaining -= keep
	}
	return written, nil
}

func (b *cappedBuffer) Bytes() []byte {
	return b.data
}
