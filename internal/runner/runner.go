package runner

import (
	"context"
)

// Runner executes a program with discrete arguments and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type EnvRunner interface {
	Runner
	RunEnv(context.Context, []string, string, ...string) ([]byte, error)
}

type QuietEnvRunner interface {
	RunEnvQuiet(context.Context, []string, string, ...string) error
}
