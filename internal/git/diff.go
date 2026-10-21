package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Status is how a file changed.
type Status byte

// Statuses as git reports them in --raw output.
const (
	Added       Status = 'A'
	Copied      Status = 'C'
	Deleted     Status = 'D'
	Modified    Status = 'M'
	Renamed     Status = 'R'
	TypeChanged Status = 'T'
)

func (s Status) String() string {
	switch s {
	case Added:
		return "added"
	case Copied:
		return "copied"
	case Deleted:
		return "deleted"
	case Renamed:
		return "renamed"
	case TypeChanged:
		return "type changed"
	default:
		return "modified"
	}
}

// File is one changed file.
type File struct {
	Status     Status
	Path       string
	OldPath    string // the source of a rename or copy
	Insertions int
	Deletions  int
	Binary     bool
	// Patch is the file's diff, or "" when its content is excluded.
	Patch string
}

// String describes f in one line, such as "modified src/a.go (+3 -1)".
func (f File) String() string {
	path := f.Path
	if f.OldPath != "" && f.OldPath != f.Path {
		path = f.OldPath + " → " + f.Path
	}
	size := fmt.Sprintf("+%d -%d", f.Insertions, f.Deletions)
	if f.Binary {
		size = "binary"
	}
	return fmt.Sprintf("%s %s (%s)", f.Status, path, size)
}

// ErrNothingToCommit means there are no changes for a commit to contain.
var ErrNothingToCommit = errors.New("nothing to commit (stage changes with git add, or use -a)")

// errDiffMismatch means git's patch output didn't line up with its file list.
var errDiffMismatch = errors.New("git diff output didn't match its file list")

// builtinExcludes are files whose content is noise to a reader: lockfiles and
// generated assets. They're matched by name in any directory.
var builtinExcludes = []string{
	"Cargo.lock", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml",
	"bun.lockb", "poetry.lock", "uv.lock", "Pipfile.lock", "Gemfile.lock", "composer.lock",
	"go.sum", "flake.lock", "*.min.js", "*.min.css", "*.map",
}

// Changes returns the files a commit would contain: staged changes, or with
// all, also unstaged changes to tracked files, as with `git commit -a`.
// Untracked files are never included.
//
// Every file is listed, but the Patch is left empty for files matching
// exclude (git pathspec patterns relative to the repository root) and for
// lockfiles and generated assets.
func (r *Repo) Changes(ctx context.Context, all bool, exclude []string) ([]File, error) {
	diff, err := r.diffCommand(ctx, all)
	if err != nil {
		return nil, err
	}
	files, err := r.listFiles(ctx, diff)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrNothingToCommit
	}
	if err := r.addPatches(ctx, diff, files, exclude); err != nil {
		return nil, err
	}
	return files, nil
}

// diffCommand returns the git diff arguments comparing HEAD with the index,
// or with all, with the working tree. Options that would change the output
// format are pinned so user config can't break parsing.
func (r *Repo) diffCommand(ctx context.Context, all bool) ([]string, error) {
	args := []string{"diff", "-z", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative", "--submodule=short"}
	if !all {
		return append(args, "--cached"), nil
	}
	tree, err := r.headTree(ctx)
	if err != nil {
		return nil, err
	}
	return append(args, tree), nil
}

// headTree returns HEAD's tree, or the empty tree before the first commit.
func (r *Repo) headTree(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--quiet", "--verify", "HEAD^{tree}")
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// --quiet --verify fails silently only when there is no HEAD yet;
		// hash-object gives the empty tree for the repository's hash algorithm
		out, err = r.run(ctx, "hash-object", "-t", "tree", "--stdin")
	}
	return strings.TrimSpace(string(out)), err
}

// listFiles lists every changed file with its line counts.
func (r *Repo) listFiles(ctx context.Context, diff []string) ([]File, error) {
	out, err := r.run(ctx, append(slices.Clone(diff), "--raw", "--numstat")...)
	if err != nil {
		return nil, err
	}
	files, rest, err := parseRaw(strings.Split(string(out), "\x00"))
	if err != nil {
		return nil, err
	}
	for i := range files {
		if rest, err = parseNumstat(rest, &files[i]); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// addPatches fills in the Patch of each file that isn't excluded.
func (r *Repo) addPatches(ctx context.Context, diff []string, files []File, exclude []string) error {
	args := append(slices.Clone(diff), "--patch-with-raw", "--")
	args = append(args, excludePathspecs(exclude)...)
	out, err := r.run(ctx, args...)
	if err != nil {
		return err
	}
	included, rest, err := parseRaw(strings.Split(string(out), "\x00"))
	if err != nil || len(included) == 0 {
		return err
	}
	// after the file list come an empty field and then the whole patch
	if len(rest) != 2 || rest[0] != "" || !strings.HasPrefix(rest[1], "diff --git ") {
		return errDiffMismatch
	}

	sections := splitPatch(rest[1])
	byPath := make(map[string]*File, len(files))
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	for _, inc := range included {
		n := 1
		if inc.Status == TypeChanged {
			n = 2 // git shows a type change as a deletion and an addition
		}
		if len(sections) < n {
			return errDiffMismatch
		}
		// a rename whose new path is excluded shows up here as a deletion of
		// the old path, which matches no listed file and is rightly dropped
		if f, ok := byPath[inc.Path]; ok {
			f.Patch = strings.Join(sections[:n], "")
		}
		sections = sections[n:]
	}
	if len(sections) > 0 {
		return errDiffMismatch
	}
	return nil
}

func excludePathspecs(user []string) []string {
	specs := make([]string, 0, len(builtinExcludes)+len(user))
	for _, name := range builtinExcludes {
		specs = append(specs, ":(top,exclude,glob)**/"+name)
	}
	for _, pattern := range user {
		specs = append(specs, ":(top,exclude)"+pattern)
	}
	return specs
}

// parseRaw reads the leading --raw entries of -z output and returns the
// fields after them. Each entry is a `:modes ids status` field followed by
// one path, or two for a rename or copy.
func parseRaw(fields []string) ([]File, []string, error) {
	var files []File
	for len(fields) > 0 && strings.HasPrefix(fields[0], ":") {
		meta := strings.Fields(fields[0])
		if len(meta) != 5 || meta[4] == "" {
			return nil, nil, fmt.Errorf("unexpected git diff --raw entry %q", fields[0])
		}
		f := File{Status: Status(meta[4][0])}
		paths := 1
		if f.Status == Renamed || f.Status == Copied {
			paths = 2
		}
		if len(fields) < 1+paths {
			return nil, nil, fmt.Errorf("git diff --raw entry %q is missing its path", fields[0])
		}
		f.Path = fields[paths]
		if paths == 2 {
			f.OldPath = fields[1]
		}
		files = append(files, f)
		fields = fields[1+paths:]
	}
	return files, fields, nil
}

// parseNumstat reads f's --numstat entry from the front of fields: counts and
// a path, or counts with an empty path followed by both paths of a rename.
// Binary files show "-" for both counts.
func parseNumstat(fields []string, f *File) ([]string, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("git diff --numstat has no entry for %s", f.Path)
	}
	insertions, rest, _ := strings.Cut(fields[0], "\t")
	deletions, path, _ := strings.Cut(rest, "\t")
	fields = fields[1:]
	if path == "" && len(fields) >= 2 {
		path, fields = fields[1], fields[2:]
	}
	if path != f.Path {
		return nil, fmt.Errorf("git diff --numstat entry %q doesn't match %s", path, f.Path)
	}
	if insertions == "-" {
		f.Binary = true
		return fields, nil
	}
	var err error
	if f.Insertions, err = strconv.Atoi(insertions); err != nil {
		return nil, fmt.Errorf("unexpected git diff --numstat count %q", insertions)
	}
	if f.Deletions, err = strconv.Atoi(deletions); err != nil {
		return nil, fmt.Errorf("unexpected git diff --numstat count %q", deletions)
	}
	return fields, nil
}

// splitPatch splits a patch at each `diff --git` header. No content line can
// look like a header, because git starts each one with ' ', '+', '-' or '\'.
func splitPatch(patch string) []string {
	const header = "\ndiff --git "
	var sections []string
	for {
		end := strings.Index(patch, header)
		if end < 0 {
			return append(sections, patch)
		}
		sections = append(sections, patch[:end+1])
		patch = patch[end+1:]
	}
}
