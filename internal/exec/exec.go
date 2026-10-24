// Package exec runs agent commands inside the sandbox container under the
// POSIX identity of the requested role.
//
// Every command goes through `nixos-container run <name> -- runuser -u <role>
// -- <command>`, so the planner (2000), developer (2001) and tester (2002)
// keep their DAC isolation: a role can only write where its UID is allowed to.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// containerBinary is the host tool that enters a nixos-container. It is a
// package variable so tests can substitute a fake without touching PATH.
var containerBinary = "nixos-container"

// Runner executes a command as a given role and returns its output.
//
// Implementations must return the captured streams even when err is non-nil:
// a failing `nix flake check` is diagnosed from its stderr, not from the exit
// status alone.
type Runner interface {
	Run(ctx context.Context, role string, args ...string) (stdout, stderr string, err error)
}

// ContainerRunner executes commands inside a running nixos-container.
type ContainerRunner struct {
	// Container is the nixos-container name, e.g. "factory".
	Container string
}

// Run executes args as role inside the container and waits for completion.
//
// stdout and stderr are returned as strings regardless of the outcome. A
// non-zero exit status yields an error that carries the exit status and the
// trimmed stderr, so callers can surface the compiler or nix diagnostics
// without re-reading the streams.
func (r ContainerRunner) Run(ctx context.Context, role string, args ...string) (string, string, error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("exec: no command given for role %q", role)
	}

	cmd := exec.CommandContext(ctx, containerBinary, r.buildArgs(role, args...)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		out, errOut := stdout.String(), stderr.String()
		if detail := strings.TrimSpace(errOut); detail != "" {
			return out, errOut, fmt.Errorf("exec: %q as %s: %w: %s", args[0], role, err, detail)
		}
		return out, errOut, fmt.Errorf("exec: %q as %s: %w", args[0], role, err)
	}

	return stdout.String(), stderr.String(), nil
}

// buildArgs assembles the nixos-container invocation.
//
// It is kept separate from Run so the argv layout can be asserted in tests
// without spawning a container.
func (r ContainerRunner) buildArgs(role string, args ...string) []string {
	// "run", container, "--", "runuser", "-u", role, "--" is seven entries
	// before the payload; the payload is copied so callers cannot alias it.
	out := make([]string, 0, 7+len(args))
	out = append(out, "run", r.Container, "--", "runuser", "-u", role, "--")
	return append(out, args...)
}
