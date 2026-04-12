package planning

import (
	"fmt"
	"os/exec"
	"time"
)

// Syncer abstracts version-control sync for the store directory.
// Implementations handle pulling remote changes before reads and
// committing+pushing local changes after writes.
type Syncer interface {
	// Pull fetches and integrates remote changes into the working tree.
	Pull() error
	// CommitAndPush stages the given paths (relative to the repo),
	// creates a commit with msg, and pushes to the remote.
	CommitAndPush(msg string, paths []string) error
}

// GitSyncer implements Syncer using git CLI commands.
type GitSyncer struct {
	dir string // directory inside the git work tree
}

// NewGitSyncer returns a GitSyncer if dir is inside a git work tree, or nil otherwise.
func NewGitSyncer(dir string) *GitSyncer {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return nil
	}
	return &GitSyncer{dir: dir}
}

func (g *GitSyncer) Pull() error {
	return exec.Command("git", "-C", g.dir, "pull", "--rebase").Run()
}

func (g *GitSyncer) CommitAndPush(msg string, paths []string) error {
	args := append([]string{"-C", g.dir, "add"}, paths...)
	if err := exec.Command("git", args...).Run(); err != nil {
		return err
	}
	if err := exec.Command("git", "-C", g.dir, "commit", "-m", msg).Run(); err != nil {
		return err // nothing to commit or other error
	}
	return exec.Command("git", "-C", g.dir, "push").Run()
}

// SyncMsg returns a standard commit message for a tack save operation.
func SyncMsg(label string) string {
	return fmt.Sprintf("tack: %s (%s)", label, time.Now().Format("2006-01-02 15:04:05"))
}
