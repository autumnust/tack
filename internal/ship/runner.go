package ship

import (
	"bytes"
	"fmt"
	"os/exec"
)

// GHRunner runs `gh` subcommands. The default implementation shells out to
// the gh CLI binary; tests inject a fake to avoid real GitHub calls.
type GHRunner interface {
	// Run runs `gh <args...>` and returns combined stdout/stderr bytes.
	// The error reflects exit status; callers must surface stderr in the
	// error message so users see why a call failed.
	Run(args ...string) ([]byte, error)

	// RunStdin is like Run but pipes stdin into the gh process. Used for
	// commands like `gh api graphql -f query=...` where the body is large.
	RunStdin(stdin []byte, args ...string) ([]byte, error)
}

// SSHRunner runs commands on a remote host via ssh, or locally when host
// is "local". Tests inject a fake.
type SSHRunner interface {
	// Run executes the given shell command on the host. For host=="local"
	// implementations should run the command via /bin/sh -c locally.
	Run(host, command string) ([]byte, error)
}

// DefaultGH is the production GHRunner — shells out to the real `gh` binary.
type DefaultGH struct{}

func (DefaultGH) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("gh %v: %w: %s", args, err, errb.String())
	}
	return out.Bytes(), nil
}

func (DefaultGH) RunStdin(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("gh %v: %w: %s", args, err, errb.String())
	}
	return out.Bytes(), nil
}

// DefaultSSH is the production SSHRunner. host=="local" runs locally.
type DefaultSSH struct{}

func (DefaultSSH) Run(host, command string) ([]byte, error) {
	var cmd *exec.Cmd
	if host == "local" || host == "" {
		cmd = exec.Command("/bin/sh", "-c", command)
	} else {
		cmd = exec.Command("ssh", host, command)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("ssh %s %q: %w: %s", host, command, err, errb.String())
	}
	return out.Bytes(), nil
}
