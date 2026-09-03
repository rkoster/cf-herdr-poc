package runner

import (
	"context"
)

// Runner executes a program with discrete arguments and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}
