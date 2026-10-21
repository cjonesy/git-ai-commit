# List the recipes
default:
    @just --list

# Tool versions; keep in sync with .github/workflows/ci.yml
golangci_lint := "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0"
govulncheck := "golang.org/x/vuln/cmd/govulncheck@v1.9.0"

# Run everything CI checks
check: test lint vuln

# Run the tests with the race detector
test:
    go test -race ./...

# Check go.mod is tidy, lint and check formatting, and lint the shell scripts
lint:
    go mod tidy -diff
    go run {{ golangci_lint }} run
    shellcheck scripts/*.sh

# Check the code and its dependencies for known vulnerabilities
vuln:
    go run {{ govulncheck }} ./...

# Record docs/demo.gif with VHS (brew install vhs), using your ai-commit config
demo:
    #!/usr/bin/env bash
    set -euo pipefail
    command -v vhs >/dev/null || { echo "error: vhs isn't installed (brew install vhs)" >&2; exit 1; }
    DEMO_DIR=$(mktemp -d)
    export DEMO_DIR # the tape reads it
    trap 'rm -rf "$DEMO_DIR"' EXIT

    go build -o "$DEMO_DIR/bin/git-ai-commit" ./cmd/git-ai-commit
    repo=$DEMO_DIR/repo
    git init -q -b main "$repo"
    git -C "$repo" config user.name Demo
    git -C "$repo" config user.email demo@example.com
    mkdir "$repo/weather"
    cat > "$repo/weather/fetch.go" <<'EOF'
    package weather

    import "net/http"

    // Fetch gets the forecast at url.
    func Fetch(url string) (*http.Response, error) {
    	return http.Get(url)
    }
    EOF
    git -C "$repo" add .
    git -C "$repo" commit -qm "feat(weather): add Fetch"
    cat > "$repo/weather/fetch.go" <<'EOF'
    package weather

    import (
    	"net/http"
    	"time"
    )

    var client = &http.Client{Timeout: 10 * time.Second}

    // Fetch gets the forecast at url, giving up after 10 seconds.
    func Fetch(url string) (*http.Response, error) {
    	return client.Get(url)
    }
    EOF
    git -C "$repo" add .

    vhs docs/demo.tape

# Publish a release of main (`just release 0.1.0`); CI then attaches the binaries
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{ trim_start_match(version, "v") }}" # Go modules need the v
    fail() { echo "error: $*" >&2; exit 1; }

    [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "'{{ version }}' isn't a version like 1.2.3"
    [[ $(git branch --show-current) == main ]] || fail "check out main first"
    [[ -z $(git status --porcelain) ]] || fail "commit or stash your changes first"
    git fetch --quiet origin main
    [[ $(git rev-parse HEAD) == $(git rev-parse origin/main) ]] || fail "main differs from origin/main; push or pull first"

    gh release create "$tag" --target main --generate-notes
