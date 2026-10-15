# Recipes use POSIX shell; on Windows run them from Git Bash or WSL.

# Pinned to match .github/workflows/ci.yml. Bump both together.
golangci_version := "v2.14.0"
govulncheck_version := "v1.9.0"

# Everything CI runs.
check: build test lint hygiene

build:
    go build ./...

test:
    go test -race ./...

lint:
    go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ golangci_version }} run ./...
    GOOS=windows go vet ./...
    GOOS=linux go vet ./...
    go run golang.org/x/vuln/cmd/govulncheck@{{ govulncheck_version }} ./...

# Forbidden-strings check; gitleaks runs in CI.
hygiene:
    scripts/check-forbidden.sh
