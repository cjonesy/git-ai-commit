# git-ai-commit

`git ai-commit` sends your pending changes to an LLM through any
OpenAI-compatible API (a hosted provider like OpenRouter or OpenAI, or a local
server like Ollama), gets back a
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/#specification)
message, and opens it in your editor through `git commit`. The commit happens
only after you save; an empty message aborts.

## Install

Needs git 2.28 or newer.

**Prebuilt binary** (macOS and Linux, amd64 and arm64): download the archive for
your platform from [Releases](https://github.com/cjonesy/git-ai-commit/releases),
check it against `checksums.txt`, and put `git-ai-commit` on your `PATH`.

**With Go** 1.27.3 or newer (an older Go downloads it automatically):

```sh
go install github.com/cjonesy/git-ai-commit/cmd/git-ai-commit@latest
```

Or from a clone: `go install ./cmd/git-ai-commit`. Both put `git-ai-commit` in
`$(go env GOPATH)/bin`.

Once it's on your `PATH`, git picks it up as `git ai-commit`.

## Quick start

Point it at a provider, then commit:

```sh
git add src/auth.rs
git ai-commit -m "race between token refresh and logout"
```

**OpenRouter**

```sh
git config --global ai-commit.endpoint https://openrouter.ai/api/v1
git config --global ai-commit.apiKey <your-key>
git config --global ai-commit.model <model-id>   # e.g. from openrouter.ai/models
```

**OpenAI**

```sh
git config --global ai-commit.endpoint https://api.openai.com/v1
git config --global ai-commit.apiKey <your-key>
git config --global ai-commit.model <model-id>
```

**Vertex AI**

Vertex takes a short-lived OAuth token instead of an API key. `apiKeyCommand`
has gcloud print a fresh one from your
[Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials)
(ADC) on each run:

```sh
git config --global ai-commit.endpoint \
  https://us-central1-aiplatform.googleapis.com/v1/projects/<project>/locations/us-central1/endpoints/openapi
git config --global ai-commit.apiKeyCommand "gcloud auth application-default print-access-token"
git config --global ai-commit.model google/gemini-2.5-flash
```

Then set up ADC in whichever way fits where you run it:

| Where                                                                                    | ADC setup                                                                                                    |
| ---------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| Your machine, as you                                                                     | `gcloud auth application-default login`                                                                      |
| Your machine, as a service account, without a key                                        | `gcloud auth application-default login --impersonate-service-account=<sa>@<project>.iam.gserviceaccount.com` |
| A credentials file (service account key, or a Workload Identity Federation config in CI) | `export GOOGLE_APPLICATION_CREDENTIALS=/path/to/credentials.json`                                            |
| Compute Engine, GKE, Cloud Run, Cloud Shell                                              | Nothing: gcloud uses the attached service account                                                            |

The identity needs the Vertex AI User role (`roles/aiplatform.user`) on the
project. Use a region that serves your model, in both places in the endpoint
URL.

Vertex model IDs carry the publisher: `google/…` for Gemini, `meta/…-maas` for
Llama, and so on. Claude on Vertex doesn't use this OpenAI-compatible endpoint,
so it isn't supported.

**Ollama** (the default endpoint, so only the model is needed)

```sh
ollama pull qwen2.5-coder:7b
git config --global ai-commit.model qwen2.5-coder:7b
```

Anything else that speaks the OpenAI chat completions API works the same way (LM
Studio, llama.cpp server, vLLM, other hosted providers): set
`ai-commit.endpoint` to its base URL, the part before `/chat/completions`.

### Turning off thinking

Thinking models are much slower, and a commit message doesn't need the thinking.
Each API has its own switch; put yours in `ai-commit.extraBody`, which is merged
into every request:

| API                                                                 | Setting                                                                                          |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| OpenAI (gpt-5.1+), Ollama, llama.cpp server, Gemini API (2.5 Flash) | `git config --global ai-commit.extraBody '{"reasoning_effort": "none"}'`                         |
| OpenRouter                                                          | `git config --global ai-commit.extraBody '{"reasoning": {"effort": "none"}}'`                    |
| vLLM serving Qwen3                                                  | `git config --global ai-commit.extraBody '{"chat_template_kwargs": {"enable_thinking": false}}'` |

If the server doesn't accept a field, its error is shown as-is. Some models
can't turn thinking off at all (e.g. Gemini 2.5 Pro).

Your diff goes to whichever provider you configure. Use `ai-commit.exclude` to
keep files out, and note that `git config --global` stores `apiKey` in plain
text in `~/.gitconfig`. To keep it out, use `apiKeyCommand` with your secret
manager, e.g. `op read op://Private/OpenRouter/credential`.

## Usage

```text
git ai-commit [-a|--all] [-m|--message <TEXT>] [--dry-run]
git ai-commit --version
```

| Flag                     | Behavior                                                                                                                                              |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| _(none)_                 | Summarize **staged** changes.                                                                                                                         |
| `-a`, `--all`            | Also include unstaged changes to tracked files, like `git commit -a`. Untracked files are never included.                                             |
| `-m`, `--message <TEXT>` | Your intent ("fixing the login race"), given to the model as context. Unlike `git commit -m`, it isn't used verbatim. Short flags combine: `-am "…"`. |
| `--dry-run`              | Print the message to stdout and exit without committing.                                                                                              |
| `--version`              | Print the version and exit.                                                                                                                           |

> **Use `-h`, not `--help`.** `git ai-commit --help` is intercepted by git,
> which looks for a man page that doesn't exist.

Progress goes to stderr. Exit code is `0` on success, `1` on any error
(including a `--dry-run` message that is still invalid after one retry);
otherwise git's own exit code. If `git commit` fails (a pre-commit hook, an
emptied message), the generated message is kept in `.git/AI_COMMIT_EDITMSG`.

## Configuration

Set with `git config` under `ai-commit.` (see
[Global, per-repo and per-run settings](#global-per-repo-and-per-run-settings)):

| Key                       | Default                                                        | Meaning                                                                                                                                                                                                                                                                                                         |
| ------------------------- | -------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ai-commit.endpoint`      | `http://localhost:11434/v1` (Ollama)                           | Base URL of the OpenAI-compatible API.                                                                                                                                                                                                                                                                          |
| `ai-commit.model`         | _(required)_                                                   | Model ID sent in the request, as the provider names it. For one run: `git -c ai-commit.model=<id> ai-commit`.                                                                                                                                                                                                   |
| `ai-commit.apiKey`        | _(none)_                                                       | Sent as `Authorization: Bearer …`. Required by hosted providers.                                                                                                                                                                                                                                                |
| `ai-commit.apiKeyCommand` | _(none)_                                                       | Shell command whose output is used as `apiKey`, run once per run (after checking there's something to commit). For short-lived tokens or secret managers. If both this and `apiKey` are set, the one set last wins, so a repository can override a global key.                                                  |
| `ai-commit.types`         | `feat fix build chore ci docs style refactor perf test revert` | Allowed types, comma or space separated. Replaces the default list.                                                                                                                                                                                                                                             |
| `ai-commit.maxDiffChars`  | `12000`                                                        | Character budget for the diff; larger diffs are summarized per file first.                                                                                                                                                                                                                                      |
| `ai-commit.timeout`       | `300`                                                          | Seconds allowed per HTTP request.                                                                                                                                                                                                                                                                               |
| `ai-commit.extraBody`     | _(none)_                                                       | JSON object whose top-level fields are added to every request body, replacing defaults like `temperature`. See [Turning off thinking](#turning-off-thinking).                                                                                                                                                   |
| `ai-commit.exclude`       | _(none)_                                                       | Multi-valued [pathspec](https://git-scm.com/docs/gitglossary#Documentation/gitglossary.txt-aiddefpathspecapathspec) patterns, relative to the repository root (e.g. `vendor`, `*.snap`), whose diff content is never sent; the files are still listed. Lockfiles, `*.min.js`, `*.map` etc. are always excluded. |

### Global, per-repo and per-run settings

Every key works at every git config level, and the most specific one wins. The
quick start uses `--global`; leave it off to set a key for one repository only:

```sh
# every repository
git config --global ai-commit.model qwen2.5-coder:7b

# only the repository you're in (writes .git/config)
git config ai-commit.endpoint https://openrouter.ai/api/v1
git config ai-commit.apiKeyCommand "op read op://Private/OpenRouter/credential"
git config ai-commit.model <model-id>

# undo a per-repo setting, falling back to the global one
git config --unset ai-commit.model

# one run only
git -c ai-commit.model=<model-id> ai-commit
```

To share settings across a group of repositories, such as everything under
`~/work`, use git's conditional includes:

```sh
git config --global "includeIf.gitdir:~/work/.path" "~/.gitconfig-work"
git config --file ~/.gitconfig-work ai-commit.endpoint \
  https://us-central1-aiplatform.googleapis.com/v1/projects/<project>/locations/us-central1/endpoints/openapi
git config --file ~/.gitconfig-work ai-commit.apiKeyCommand "gcloud auth application-default print-access-token"
git config --file ~/.gitconfig-work ai-commit.model google/gemini-2.5-flash
```

Order of precedence, lowest to highest: global, the repository's `.git/config`,
then `git -c`. Within one file, later lines win, and an include counts as
sitting where its `includeIf` line is. So keep the `includeIf` below any
`[ai-commit]` section in `~/.gitconfig`, or the global values win even inside
`~/work`. Check what applies in a repository with
`git config --show-origin --get-regexp '^ai-commit\.'`.

## Development

```sh
go test -race ./...
golangci-lint run          # lint and format checks, configured in .golangci.yml
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

CI runs these on every push to `main` and every pull request, testing on Linux
and macOS with the latest Go, and builds the release archives as a dry run.

To release, create a GitHub release with a new semver tag (`v1.2.3`).
Publishing it runs CI again, then [GoReleaser](https://goreleaser.com) builds
the binaries and attaches them to that release, keeping the notes you wrote.

Everything that touches the repository goes through the git CLI (`internal/git`)
rather than a library, so `git -c` overrides, include rules, hooks, the editor
and commit signing all behave exactly as in git.
