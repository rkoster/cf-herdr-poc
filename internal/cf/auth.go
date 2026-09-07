package cf

import (
	"context"
	"errors"

	"cf-herdr-poc/internal/runner"
)

type EnvRunner interface {
	RunEnv(context.Context, []string, string, ...string) ([]byte, error)
}

type QuietEnvRunner interface {
	RunEnvQuiet(context.Context, []string, string, ...string) error
}

type CFAuthenticator interface {
	Authenticate(context.Context) error
}

type Authenticator struct {
	Run               EnvRunner
	Executable        string
	API               string
	Username          string
	Password          string
	Org               string
	Space             string
	CFHome            string
	SkipSSLValidation bool
}

func (a Authenticator) Authenticate(ctx context.Context) error {
	if a.Run == nil {
		return errors.New("CF CLI authentication failed")
	}
	environment := []string{"CF_HOME=" + a.CFHome}
	apiArgs := []string{"api", a.API}
	if a.SkipSSLValidation {
		apiArgs = append(apiArgs, "--skip-ssl-validation")
	}
	if err := a.run(ctx, environment, apiArgs...); err != nil {
		return errors.New("CF CLI authentication failed")
	}
	if err := a.run(ctx, environment, "auth", a.Username, a.Password); err != nil {
		return errors.New("CF CLI authentication failed")
	}
	if err := a.run(ctx, environment, "target", "-o", a.Org, "-s", a.Space); err != nil {
		return errors.New("CF CLI authentication failed")
	}
	return nil
}

func (a Authenticator) run(ctx context.Context, environment []string, args ...string) error {
	if quiet, ok := a.Run.(QuietEnvRunner); ok {
		return quiet.RunEnvQuiet(ctx, environment, a.Executable, args...)
	}
	_, err := a.Run.RunEnv(ctx, environment, a.Executable, args...)
	return err
}

var _ EnvRunner = runner.Exec{}
