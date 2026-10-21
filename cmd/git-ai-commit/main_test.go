package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// binary is the git-ai-commit executable under test, built once by TestMain.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "git-ai-commit-test")
	if err != nil {
		log.Fatal(err)
	}
	binary = filepath.Join(dir, "git-ai-commit")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		log.Fatalf("building git-ai-commit: %v\n%s", err, out)
	}
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		log.Print(err)
	}
	os.Exit(code)
}

// isolated keeps the user's own git config and proxy out of the tests.
var isolated = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "NO_PROXY=127.0.0.1,localhost"}

// testRepo is a scratch repository to run git-ai-commit in.
type testRepo struct {
	t      *testing.T
	dir    string
	subdir string   // where git-ai-commit runs, relative to dir
	config []string // "key=value" git config passed to git-ai-commit, like `git -c`
	env    []string // extra environment for git-ai-commit
}

func newRepo(t *testing.T) *testRepo {
	t.Helper()
	r := &testRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.name", "T")
	r.git("config", "user.email", "t@example.com")
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), isolated...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (r *testRepo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// stage writes name and adds it to the index.
func (r *testRepo) stage(name, content string) {
	r.t.Helper()
	r.write(name, content)
	r.git("add", name)
}

func (r *testRepo) lastCommitMessage() string {
	r.t.Helper()
	return strings.TrimSpace(r.git("log", "-1", "--format=%B"))
}

type result struct {
	code           int
	stdout, stderr string
}

// aiCommit runs git-ai-commit against the model at endpoint.
func (r *testRepo) aiCommit(endpoint string, args ...string) result {
	r.t.Helper()
	config := append([]string{"ai-commit.endpoint=" + endpoint, "ai-commit.model=test-model"}, r.config...)
	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Join(r.dir, r.subdir)
	cmd.Env = append(os.Environ(), isolated...)
	cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(config)))
	for i, kv := range config {
		key, value, _ := strings.Cut(kv, "=")
		cmd.Env = append(cmd.Env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, key), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, value))
	}
	cmd.Env = append(cmd.Env, r.env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		r.t.Fatalf("running git-ai-commit: %v", err)
	}
	return result{cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

func (res result) wantCode(t *testing.T, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit code %d, want %d\nstderr:\n%s", res.code, want, res.stderr)
	}
}

func (res result) wantStderr(t *testing.T, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(res.stderr, w) {
			t.Errorf("stderr is missing %q:\n%s", w, res.stderr)
		}
	}
}

func (res result) wantStdout(t *testing.T, want string) {
	t.Helper()
	if got := strings.TrimSpace(res.stdout); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// fakeModel is an OpenAI-compatible server that sends canned replies in order
// and records every request.
type fakeModel struct {
	t        *testing.T
	url      string
	mu       sync.Mutex
	replies  []http.HandlerFunc
	requests []request
}

type request struct {
	header http.Header
	body   string
}

func (q request) json(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(q.body), &v); err != nil {
		t.Fatalf("request body isn't JSON: %v\n%s", err, q.body)
	}
	return v
}

func serve(t *testing.T, replies ...http.HandlerFunc) *fakeModel {
	m := &fakeModel{t: t, replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(srv.Close)
	m.url = srv.URL + "/v1"
	return m
}

func (m *fakeModel) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requests = append(m.requests, request{r.Header.Clone(), string(body)})
	var reply http.HandlerFunc
	if len(m.replies) > 0 {
		reply, m.replies = m.replies[0], m.replies[1:]
	}
	m.mu.Unlock()
	if reply == nil {
		m.t.Errorf("unexpected request:\n%s", body)
		http.Error(w, "no more replies", http.StatusInternalServerError)
		return
	}
	reply(w, r)
}

func (m *fakeModel) request(i int) request {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if i >= len(m.requests) {
		m.t.Fatalf("got %d requests, want at least %d", len(m.requests), i+1)
	}
	return m.requests[i]
}

func (m *fakeModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// completion replies with a chat completion whose message is content.
func completion(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{ // a write error means the client left
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		})
	}
}

func httpError(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestDryRun(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "hello\n")
	m := serve(t, completion("feat: add a\n\nAdds a.txt."))

	res := r.aiCommit(m.url, "--dry-run", "-m", "my hint")
	res.wantCode(t, 0)
	res.wantStdout(t, "feat: add a\n\nAdds a.txt.")
	req := m.request(0)
	for _, want := range []string{"my hint", "+hello", "test-model"} {
		if !strings.Contains(req.body, want) {
			t.Errorf("request is missing %q", want)
		}
	}
	if body := req.json(t); len(body) != 4 {
		t.Errorf("request has fields beyond model, messages, temperature and stream: %v", body)
	}
}

func TestCombinedShortFlags(t *testing.T) {
	r := newRepo(t)
	r.stage("t.txt", "one\n")
	r.git("commit", "-qm", "init")
	r.write("t.txt", "two\n")
	m := serve(t, completion("fix: t"))

	r.aiCommit(m.url, "--dry-run", "-am", "the hint").wantCode(t, 0)
	if body := m.request(0).body; !strings.Contains(body, "+two") || !strings.Contains(body, "the hint") {
		t.Errorf("-am didn't act as -a -m:\n%s", body)
	}
}

func TestUnexpectedArgument(t *testing.T) {
	r := newRepo(t)
	res := r.aiCommit("http://unused", "stray")
	res.wantCode(t, 1)
	res.wantStderr(t, `unexpected argument "stray"`)
}

func TestNothingStaged(t *testing.T) {
	r := newRepo(t)
	m := serve(t)
	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 1)
	res.wantStderr(t, "nothing to commit")
}

func TestAllIncludesTrackedButNotUntracked(t *testing.T) {
	r := newRepo(t)
	r.stage("t.txt", "one\n")
	r.git("commit", "-qm", "init")
	r.write("t.txt", "one\nUNSTAGED_LINE\n")
	r.write("u.txt", "UNTRACKED_LINE\n")
	m := serve(t, completion("fix: t"))

	r.aiCommit(m.url, "--dry-run", "-a").wantCode(t, 0)
	body := m.request(0).body
	if !strings.Contains(body, "UNSTAGED_LINE") || strings.Contains(body, "UNTRACKED_LINE") {
		t.Errorf("want the unstaged change and not the untracked file:\n%s", body)
	}
}

func TestAllOnFirstCommit(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "FIRST_LINE\n")
	m := serve(t, completion("feat: a"))

	r.aiCommit(m.url, "--dry-run", "-a").wantCode(t, 0)
	if !strings.Contains(m.request(0).body, "FIRST_LINE") {
		t.Error("the staged file of the first commit is missing")
	}
}

func TestLockfileIsListedButNotSent(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.stage("Cargo.lock", "LOCK_CONTENT\n")
	m := serve(t, completion("chore: x"))

	r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	body := m.request(0).body
	if !strings.Contains(body, "added Cargo.lock (+1 -0)") || strings.Contains(body, "LOCK_CONTENT") {
		t.Errorf("want the lockfile listed without its content:\n%s", body)
	}
}

func TestExcludesApplyFromSubdirectory(t *testing.T) {
	r := newRepo(t)
	r.stage("sub/a.txt", "x\n")
	r.stage("other/Cargo.lock", "LOCK_CONTENT\n")
	r.subdir = "sub"
	m := serve(t, completion("feat: a"))

	r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	body := m.request(0).body
	if !strings.Contains(body, "+x") || strings.Contains(body, "LOCK_CONTENT") {
		t.Errorf("want sub/a.txt's content and not the lockfile's:\n%s", body)
	}
}

func TestUserExcludes(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.stage("snapshots/big.snap", "SNAPSHOT_CONTENT\n")
	r.config = []string{"ai-commit.exclude=snapshots"}
	m := serve(t, completion("test: a"))

	r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	body := m.request(0).body
	if !strings.Contains(body, "added snapshots/big.snap") || strings.Contains(body, "SNAPSHOT_CONTENT") {
		t.Errorf("want the excluded file listed without its content:\n%s", body)
	}
}

func TestOverBudgetDiffIsSummarizedPerFile(t *testing.T) {
	r := newRepo(t)
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		r.stage(f, strings.Repeat("line\n", 50))
	}
	r.config = []string{"ai-commit.maxDiffChars=300"}
	m := serve(t, completion("sum a"), completion("sum b"), completion("sum c"), completion("feat: three files"))

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 0)
	res.wantStdout(t, "feat: three files")
	if final := m.request(3).body; !strings.Contains(final, "sum a") || !strings.Contains(final, "sum c") {
		t.Errorf("the final request is missing summaries:\n%s", final)
	}
}

func TestSummariesUseRealPaths(t *testing.T) {
	r := newRepo(t)
	r.git("config", "diff.noprefix", "true")
	r.stage("dir b/one.txt", strings.Repeat("line\n", 50))
	r.stage("two.txt", strings.Repeat("line\n", 50))
	r.config = []string{"ai-commit.maxDiffChars=300"}
	m := serve(t, completion("sum one"), completion("sum two"), completion("feat: two files"))

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 0)
	res.wantStderr(t, "Summarizing 1/2: dir b/one.txt", "Summarizing 2/2: two.txt")
	if !strings.Contains(m.request(2).body, "- dir b/one.txt: sum one") {
		t.Errorf("summary isn't labelled with its path:\n%s", m.request(2).body)
	}
}

func TestInvalidReplyIsRetried(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	m := serve(t, completion("Added a file"), completion("feat: add a file"))

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 0)
	res.wantStdout(t, "feat: add a file")
	if !strings.Contains(m.request(1).body, "Added a file") {
		t.Error("the retry doesn't show the model its invalid reply")
	}
}

func TestStillInvalidDryRunExits1(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	m := serve(t, completion("nope"), completion("still nope"))

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 1)
	res.wantStdout(t, "still nope")
	res.wantStderr(t, "warning")
}

func TestCommit(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.env = []string{"GIT_EDITOR=true"}
	m := serve(t, completion("feat: add a\n\nBody."))

	r.aiCommit(m.url).wantCode(t, 0)
	if got := r.lastCommitMessage(); got != "feat: add a\n\nBody." {
		t.Errorf("commit message = %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".git", "AI_COMMIT_EDITMSG")); !os.IsNotExist(err) {
		t.Error("the message file is left behind after a successful commit")
	}
}

func TestEmptiedMessageAbortsWithGitsCode(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.write("empty.sh", "#!/bin/sh\n: > \"$1\"\n")
	r.env = []string{"GIT_EDITOR=sh " + filepath.Join(r.dir, "empty.sh")}
	m := serve(t, completion("feat: a"))

	r.aiCommit(m.url).wantCode(t, 1)
}

func TestFailedCommitKeepsTheMessage(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.write(".git/hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(r.dir, ".git/hooks/pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.env = []string{"GIT_EDITOR=true"}
	m := serve(t, completion("feat: a"))

	res := r.aiCommit(m.url)
	res.wantCode(t, 1)
	res.wantStderr(t, "AI_COMMIT_EDITMSG")
	kept, err := os.ReadFile(filepath.Join(r.dir, ".git", "AI_COMMIT_EDITMSG"))
	if err != nil || string(kept) != "feat: a\n" {
		t.Errorf("kept message = %q, %v", kept, err)
	}
}

func TestUnreachableServer(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil { // nothing listens there now
		t.Fatal(err)
	}

	res := r.aiCommit("http://" + addr + "/v1")
	res.wantCode(t, 1)
	res.wantStderr(t, "can't reach", "is your model server running?")
}

func TestBadEndpointShowsTheRealError(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	res := r.aiCommit("ftp://127.0.0.1/v1", "--dry-run")
	res.wantCode(t, 1)
	res.wantStderr(t, "request to ftp://127.0.0.1/v1/chat/completions failed")
	if strings.Contains(res.stderr, "is your model server running") {
		t.Errorf("a bad scheme is reported as an unreachable server:\n%s", res.stderr)
	}
}

func TestTruncatedResponseIsNotCalledIncompatible(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	m := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "500")
		_, _ = io.WriteString(w, `{"choices"`)
	})

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 1)
	res.wantStderr(t, "failed")
	if strings.Contains(res.stderr, "not OpenAI-compatible") {
		t.Errorf("a cut-off response is blamed on the server's API:\n%s", res.stderr)
	}
}

func TestHTTPErrorShowsTheServerMessage(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	m := serve(t, httpError(http.StatusBadRequest, "context too long"))

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 1)
	res.wantStderr(t, "HTTP 400", "context too long")
	if n := m.count(); n != 1 {
		t.Errorf("got %d requests, want 1", n)
	}
}

func TestMultilineConfigValueCannotFakeAKey(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.config = []string{"ai-commit.note=first line\nai-commit.model wrong-model"}
	m := serve(t, completion("feat: a"))

	r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	if model := m.request(0).json(t)["model"]; model != "test-model" {
		t.Errorf("model = %v, want test-model", model)
	}
}

func TestUnmergedFilesFailBeforeCallingTheModel(t *testing.T) {
	r := newRepo(t)
	r.stage("f.txt", "a\n")
	r.git("commit", "-qm", "init")
	r.write("f.txt", "b\n")
	r.git("stash", "-q")
	r.write("f.txt", "c\n")
	r.git("commit", "-qam", "c")
	// stash pop conflicts but leaves no MERGE_HEAD, so only the index shows it
	pop := exec.Command("git", "stash", "pop")
	pop.Dir, pop.Env = r.dir, append(os.Environ(), isolated...)
	if pop.Run() == nil {
		t.Fatal("stash pop didn't conflict")
	}
	m := serve(t)

	res := r.aiCommit(m.url, "--dry-run")
	res.wantCode(t, 1)
	res.wantStderr(t, "merge conflicts")
}

func TestAPIKeyCommandOutputIsTheBearerToken(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.config = []string{`ai-commit.apiKeyCommand=printf '  tok-123\n'`}
	m := serve(t, completion("feat: a"))

	r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	if got := m.request(0).header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestFailingAPIKeyCommandStopsBeforeAnyRequest(t *testing.T) {
	for _, command := range []string{"echo 'not logged in' >&2; exit 3", "true"} {
		t.Run(command, func(t *testing.T) {
			r := newRepo(t)
			r.stage("a.txt", "x\n")
			r.config = []string{"ai-commit.apiKeyCommand=" + command}
			m := serve(t)

			res := r.aiCommit(m.url, "--dry-run")
			res.wantCode(t, 1)
			res.wantStderr(t, "ai-commit.apiKeyCommand")
		})
	}
}

func TestAPIKeyCommandDoesNotRunWithNothingToCommit(t *testing.T) {
	r := newRepo(t)
	marker := filepath.Join(r.dir, "ran")
	r.config = []string{"ai-commit.apiKeyCommand=touch '" + marker + "'"}
	m := serve(t)

	r.aiCommit(m.url, "--dry-run").wantStderr(t, "nothing to commit")
	if _, err := os.Stat(marker); err == nil {
		t.Error("apiKeyCommand ran although there was nothing to commit")
	}
}

// TestMoreSpecificAPIKeySourceWins checks that an apiKey and an apiKeyCommand
// set at different config levels override each other like any other key.
func TestMoreSpecificAPIKeySourceWins(t *testing.T) {
	t.Run("command over key", func(t *testing.T) {
		r := newRepo(t)
		r.stage("a.txt", "x\n")
		r.git("config", "ai-commit.apiKey", "repo-key")
		r.config = []string{"ai-commit.apiKeyCommand=echo command-token"}
		m := serve(t, completion("feat: a"))

		r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
		if got := m.request(0).header.Get("Authorization"); got != "Bearer command-token" {
			t.Errorf("Authorization = %q", got)
		}
	})
	t.Run("key over command", func(t *testing.T) {
		r := newRepo(t)
		r.stage("a.txt", "x\n")
		r.git("config", "ai-commit.apiKeyCommand", "exit 1")
		r.config = []string{"ai-commit.apiKey=dash-c-key"}
		m := serve(t, completion("feat: a"))

		r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
		if got := m.request(0).header.Get("Authorization"); got != "Bearer dash-c-key" {
			t.Errorf("Authorization = %q", got)
		}
	})
}

func TestCustomCommentCharIsWhatGetsRejected(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.config = []string{"core.commentChar=;"}
	r.env = []string{"GIT_EDITOR=true"}
	m := serve(t, completion("feat: a\n\n; would be stripped"), completion("feat: a\n\n# kept"))

	r.aiCommit(m.url).wantCode(t, 0)
	if !strings.Contains(m.request(0).body, "no line that starts with `;`") {
		t.Error("the prompt doesn't name the comment prefix")
	}
	if got := r.lastCommitMessage(); got != "feat: a\n\n# kept" {
		t.Errorf("commit message = %q", got)
	}
}

func TestNonStripCleanupAllowsCommentLines(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	// scissors keeps comment lines but cuts git's template off below its marker
	r.config = []string{"commit.cleanup=scissors"}
	r.env = []string{"GIT_EDITOR=true"}
	m := serve(t, completion("feat: a\n\n# kept"))

	r.aiCommit(m.url).wantCode(t, 0)
	if strings.Contains(m.request(0).body, "no line that starts with") {
		t.Error("the prompt forbids comment lines that git would keep")
	}
	if got := r.lastCommitMessage(); got != "feat: a\n\n# kept" {
		t.Errorf("commit message = %q", got)
	}
}

func TestExtraBodyIsMergedIntoTheRequest(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	r.config = []string{`ai-commit.extraBody={"reasoning": {"effort": "none"}, "temperature": 0}`}
	m := serve(t, completion("feat: a"))

	r.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	body := m.request(0).json(t)
	if effort := body["reasoning"].(map[string]any)["effort"]; effort != "none" {
		t.Errorf("reasoning.effort = %v", effort)
	}
	if body["temperature"] != 0.0 {
		t.Errorf("temperature = %v, want extraBody's 0 to replace the default", body["temperature"])
	}
	if body["model"] != "test-model" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestExtraBodyMustBeAJSONObject(t *testing.T) {
	for _, value := range []string{"reasoning_effort=none", `["reasoning_effort"]`, "null"} {
		t.Run(value, func(t *testing.T) {
			r := newRepo(t)
			r.stage("a.txt", "x\n")
			r.config = []string{"ai-commit.extraBody=" + value}
			m := serve(t)

			res := r.aiCommit(m.url, "--dry-run")
			res.wantCode(t, 1)
			res.wantStderr(t, "ai-commit.extraBody must be a JSON object")
		})
	}
}

func TestHonorsGitDirAndWorkTree(t *testing.T) {
	a := newRepo(t)
	a.stage("a.txt", "REPO_A_LINE\n")
	b := newRepo(t)
	b.stage("b.txt", "REPO_B_LINE\n")
	b.env = []string{"GIT_DIR=" + filepath.Join(a.dir, ".git"), "GIT_WORK_TREE=" + a.dir}
	m := serve(t, completion("feat: a"))

	b.aiCommit(m.url, "--dry-run").wantCode(t, 0)
	body := m.request(0).body
	if !strings.Contains(body, "REPO_A_LINE") || strings.Contains(body, "REPO_B_LINE") {
		t.Errorf("want repository a's changes:\n%s", body)
	}
}

// TestGitDashCReachesTheConfig runs git-ai-commit as a real git subcommand,
// which is how `git -c` overrides reach it.
func TestGitDashCReachesTheConfig(t *testing.T) {
	r := newRepo(t)
	r.stage("a.txt", "x\n")
	m := serve(t, completion("feat: a"))

	cmd := exec.Command("git", "-c", "ai-commit.endpoint="+m.url, "-c", "ai-commit.model=from-dash-c", "ai-commit", "--dry-run")
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), isolated...)
	cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git ai-commit: %v\n%s", err, out)
	}
	if model := m.request(0).json(t)["model"]; model != "from-dash-c" {
		t.Errorf("model = %v, want from-dash-c", model)
	}
}

func TestVersion(t *testing.T) {
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "git-ai-commit ") || strings.TrimSpace(string(out)) == "git-ai-commit" {
		t.Errorf("--version printed %q", out)
	}
}

// TestVersionFromLinkerFlag builds the binary the way the release does, which
// fails silently if the variable GoReleaser sets is ever renamed.
func TestVersionFromLinkerFlag(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "git-ai-commit")
	build := exec.Command("go", "build", "-o", bin, "-ldflags", "-X main.version=1.2.3", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "git-ai-commit 1.2.3" {
		t.Errorf("--version printed %q", got)
	}
}
