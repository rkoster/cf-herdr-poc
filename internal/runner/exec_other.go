//go:build !unix

package runner

import "os/exec"

func configureCancellation(_ *exec.Cmd) {}
