package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"cf-herdr-poc/internal/bootstrap"
)

type commandJoiner struct{ executable string }

func (j commandJoiner) Join(ctx context.Context, args []string, input io.Reader) error {
	command := exec.CommandContext(ctx, j.executable, args...)
	command.Stdin = input
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return &joinError{message: sanitizeJoinOutput(string(output))}
	}
	_ = output
	return nil
}

type joinError struct{ message string }

func (e *joinError) Error() string {
	if e.message == "" {
		return "Collie Pack join failed"
	}
	return "Collie Pack join failed: " + e.message
}

var joinSecretPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer\s+)?|\b(?:token|secret|password)\s*[=:]\s*)\S+`)

func sanitizeJoinOutput(value string) string {
	value = joinSecretPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		return value[:1024]
	}
	return value
}

func main() {
	executable := os.Getenv("COLLIE_EXECUTABLE")
	if executable == "" {
		executable = "./bin/collie"
	}
	config := bootstrap.Config{Executable: executable, TokenPath: os.Getenv("COLLIE_JOIN_TOKEN_FILE"), ReadyPath: os.Getenv("SANDBOX_BOOTSTRAP_READY_FILE"), TrustStorePath: os.Getenv("COLLIE_PACK_TRUST_STORE"), LeadAddress: os.Getenv("COLLIE_PACK_LEAD_ADDRESS"), MemberID: os.Getenv("SANDBOX_MEMBER_ID"), SelfAddress: os.Getenv("COLLIE_PACK_SELF_ADDRESS")}
	server, err := bootstrap.New(config, commandJoiner{executable}, os.Stderr)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(address(os.Getenv("SANDBOX_BOOTSTRAP_ADDRESS"), os.Getenv("PORT")), server))
}
func address(explicit, port string) string {
	if explicit != "" {
		return explicit
	}
	if port == "" {
		port = "8080"
	}
	return ":" + port
}
