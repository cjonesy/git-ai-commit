// Package git reads from and commits to a repository through the git CLI.
//
// It runs git itself rather than using a library so that everything behaves
// exactly as in git: `git -c` overrides, every include rule, hooks, the
// editor and commit signing.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is the repository containing a directory.
type Repo struct {
	dir    string // where git runs; it finds the repository from here
	gitDir string // absolute path of the .git directory
}

// Open finds the repository containing dir and checks that it can take a
// normal commit: it isn't bare, mid-merge, or holding unresolved conflicts.
func Open(ctx context.Context, dir string) (*Repo, error) {
	r := &Repo{dir: dir}
	// --show-toplevel fails in a bare repository, which has nothing to commit
	out, err := r.run(ctx, "rev-parse", "--absolute-git-dir", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r.gitDir, _, _ = strings.Cut(string(out), "\n")

	for _, head := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if _, err := os.Stat(filepath.Join(r.gitDir, head)); err == nil {
			return nil, errors.New("a merge/cherry-pick/revert is in progress; finish it with git commit")
		}
	}
	unmerged, err := r.run(ctx, "ls-files", "--unmerged", "-z")
	if err != nil {
		return nil, err
	}
	if len(unmerged) > 0 {
		return nil, errors.New("resolve the merge conflicts first; git won't commit unmerged files")
	}
	return r, nil
}

// ConfigEntry is one git config value. Keys are as git prints them, with the
// section and name lowercased.
type ConfigEntry struct {
	Key   string
	Value string
}

// Config returns every config entry in precedence order, the last value
// winning, with `git -c` overrides included.
func (r *Repo) Config(ctx context.Context) ([]ConfigEntry, error) {
	out, err := r.run(ctx, "config", "--list", "-z")
	if err != nil {
		return nil, err
	}
	var entries []ConfigEntry
	// -z prints `key\nvalue\0`, so a value with newlines can't fake a key
	for entry := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		entries = append(entries, ConfigEntry{key, value})
	}
	return entries, nil
}

// Branch returns the current branch name, or "" when HEAD is detached.
func (r *Repo) Branch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "branch", "--show-current")
	return strings.TrimSpace(string(out)), err
}

// MessageFile is where Commit writes the message it hands to git.
func (r *Repo) MessageFile() string {
	return filepath.Join(r.gitDir, "AI_COMMIT_EDITMSG")
}

// Commit opens message in the editor through `git commit -e` and returns
// git's exit code. With all, unstaged changes to tracked files are committed
// too, as with `git commit -a`. The message file is removed after a successful
// commit and kept otherwise, so a failed hook doesn't lose the message.
func (r *Repo) Commit(all bool, message string) (int, error) {
	path := r.MessageFile()
	if err := os.WriteFile(path, []byte(message+"\n"), 0o644); err != nil {
		return 0, err
	}
	args := []string{"commit", "-e", "-F", path}
	if all {
		args = append(args, "-a")
	}
	// no context: an interrupt belongs to the editor, not to us
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
		_ = os.Remove(path) // a leftover file is harmless: the next run overwrites it
		return 0, nil
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code > 0 {
			return code, nil
		}
		return 1, nil // killed by a signal
	default:
		return 0, fmt.Errorf("running git commit: %w", err)
	}
}

// run runs git and returns its stdout. A failure carries git's own message.
func (r *Repo) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %s", args[0], strings.TrimPrefix(msg, "fatal: "))
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}
