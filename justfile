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
