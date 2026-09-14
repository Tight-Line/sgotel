.PHONY: all build test test-coverage test-coverage-check clean run lint lint-fix docker fmt tidy tools setup-hooks check vulncheck

# Build variables
VERSION?=0.1.0
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# golangci-lint is run through `go run` at a pinned version rather than being
# installed separately, so a contributor and CI always run the same linter, and
# it is always built with the toolchain the `go` directive selects. A linter
# built by an older Go than the one it targets refuses to start.
#
# It is deliberately not a `tool` directive in go.mod: that would pull its ~350
# dependencies into this module's graph, where Snyk would scan lint machinery
# that never ships in the binary. govulncheck is a tool directive because it
# costs 7 modules and is the scanner itself.
GOLANGCI_LINT_VERSION?=v2.13.2
GOLANGCI_LINT=go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Default target
all: lint test build

# Build the binary
build:
	go build $(LDFLAGS) -o bin/sgotel ./cmd/sgotel

# Run tests
test:
	go test -race -v ./...

# Run tests with coverage (generates report)
test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic -tags=ci ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run tests and REQUIRE coverage (or coverage:ignore comments)
test-coverage-check:
	@./scripts/check-coverage.sh

# Run the server locally for development
run: build
	./bin/sgotel

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html coverage.filtered.out

# Run linter
lint:
	$(GOLANGCI_LINT) run ./...

# Run linter and fix issues automatically where possible
lint-fix:
	$(GOLANGCI_LINT) run --fix ./...

# Build Docker image
docker:
	docker build --build-arg VERSION=$(VERSION) -t sgotel:$(VERSION) -f Dockerfile .

# Format code
fmt:
	go fmt ./...
	goimports -w -local github.com/tight-line/sgotel .

# Tidy dependencies
tidy:
	go mod tidy

# Install development tools. golangci-lint and govulncheck are not here:
# both are version-pinned and fetched on demand by their own targets.
tools:
	go install golang.org/x/tools/cmd/goimports@latest

# Set up git hooks for development
setup-hooks:
	@echo "Installing pre-commit hook..."
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

# Scan for known vulnerabilities in dependencies and the standard library.
#
# Kept out of `check` on purpose: it needs vuln.go.dev, and `check` gates
# `scripts/make-tag`, which should not fail on someone else's outage. CI runs
# this as its own job.
vulncheck:
	go tool govulncheck ./...

# Verify everything (used by CI and before releasing)
check: lint test-coverage-check build
	@echo "All checks passed. Ready for release."
