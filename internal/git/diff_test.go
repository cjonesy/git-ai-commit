package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRepo creates a repository isolated from the user's git config and
// returns a function that runs git in it.
func newTestRepo(t *testing.T) (dir string, git func(args ...string)) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir = t.TempDir()
	git = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "T")
	git("config", "user.email", "t@example.com")
	return dir, git
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func changes(t *testing.T, dir string, all bool, exclude ...string) map[string]File {
	t.Helper()
	ctx := context.Background()
	repo, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := repo.Changes(ctx, all, exclude)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	return byPath
}

// TestChanges covers the cases that make git's output tricky to pair up:
// renames, spaces in paths, binary files, a type change (which git shows as
// two patch sections), and excluded content.
func TestChanges(t *testing.T) {
	dir, git := newTestRepo(t)
	writeFile(t, dir, "a.txt", "one\n")
	writeFile(t, dir, "b.txt", strings.Repeat("line\n", 40))
	writeFile(t, dir, "link", "plain file\n")
	writeFile(t, dir, "other/Cargo.lock", "LOCK\n")
	git("add", ".")
	git("commit", "-qm", "init")

	writeFile(t, dir, "a.txt", "ONE\n")
	git("mv", "b.txt", "c d.txt")
	if err := os.Remove(filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "sub/new file.txt", "new\n")
	writeFile(t, dir, "bin.dat", "\x00\x01\x02")
	writeFile(t, dir, "other/Cargo.lock", "LOCK2\n")
	git("add", "-A")

	files := changes(t, filepath.Join(dir, "sub"), false) // from a subdirectory
	tests := []struct {
		path       string
		line       string // File.String()
		hasPatch   bool
		patchParts int // diff --git sections in the patch
	}{
		{"a.txt", "modified a.txt (+1 -1)", true, 1},
		{"c d.txt", "renamed b.txt → c d.txt (+0 -0)", true, 1},
		{"link", "type changed link (+1 -1)", true, 2},
		{"sub/new file.txt", "added sub/new file.txt (+1 -0)", true, 1},
		{"bin.dat", "added bin.dat (binary)", true, 1},
		{"other/Cargo.lock", "modified other/Cargo.lock (+1 -1)", false, 0},
	}
	if len(files) != len(tests) {
		t.Errorf("got %d files, want %d: %v", len(files), len(tests), files)
	}
	for _, tt := range tests {
		f, ok := files[tt.path]
		if !ok {
			t.Errorf("%s is missing", tt.path)
			continue
		}
		if got := f.String(); got != tt.line {
			t.Errorf("String() = %q, want %q", got, tt.line)
		}
		if (f.Patch != "") != tt.hasPatch {
			t.Errorf("%s: has patch = %v, want %v", tt.path, f.Patch != "", tt.hasPatch)
		}
		if got := strings.Count(f.Patch, "diff --git "); got != tt.patchParts {
			t.Errorf("%s: patch has %d sections, want %d:\n%s", tt.path, got, tt.patchParts, f.Patch)
		}
	}
}

func TestChangesUserExclude(t *testing.T) {
	dir, git := newTestRepo(t)
	writeFile(t, dir, "keep.txt", "k\n")
	writeFile(t, dir, "snapshots/a.snap", "s\n")
	git("add", ".")

	files := changes(t, dir, false, "snapshots")
	if files["keep.txt"].Patch == "" {
		t.Error("keep.txt lost its patch")
	}
	if f, ok := files["snapshots/a.snap"]; !ok || f.Patch != "" {
		t.Errorf("want snapshots/a.snap listed without a patch, got %+v (listed: %v)", f, ok)
	}
}

func TestChangesEverythingExcluded(t *testing.T) {
	dir, git := newTestRepo(t)
	writeFile(t, dir, "go.sum", "x\n")
	git("add", ".")

	if f := changes(t, dir, false)["go.sum"]; f.Patch != "" || f.Status != Added {
		t.Errorf("go.sum = %+v, want an addition without a patch", f)
	}
}

func TestChangesAllComparesWithTheWorkingTree(t *testing.T) {
	dir, git := newTestRepo(t)
	writeFile(t, dir, "staged.txt", "s\n")
	git("add", ".") // no commit yet: -a compares with the empty tree
	files := changes(t, dir, true)
	if _, ok := files["staged.txt"]; !ok {
		t.Fatal("staged.txt is missing before the first commit")
	}

	git("commit", "-qm", "init")
	writeFile(t, dir, "staged.txt", "s\nunstaged\n")
	writeFile(t, dir, "untracked.txt", "u\n")
	files = changes(t, dir, true)
	if !strings.Contains(files["staged.txt"].Patch, "+unstaged") {
		t.Error("the unstaged edit is missing")
	}
	if _, ok := files["untracked.txt"]; ok {
		t.Error("an untracked file is included")
	}
}

func TestChangesNothingToCommit(t *testing.T) {
	dir, _ := newTestRepo(t)
	repo, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Changes(context.Background(), false, nil); !errors.Is(err, ErrNothingToCommit) {
		t.Errorf("err = %v, want ErrNothingToCommit", err)
	}
}

func TestSplitPatch(t *testing.T) {
	patch := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-diff --git looks like a header\n+but isn't\n" +
		"diff --git a/y b/y\nnew file mode 100644\n"
	got := splitPatch(patch)
	if len(got) != 2 || !strings.HasSuffix(got[0], "+but isn't\n") || !strings.HasPrefix(got[1], "diff --git a/y") {
		t.Errorf("splitPatch = %q", got)
	}
}
