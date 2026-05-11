.PHONY: all build test test-coverage test-coverage-check clean run lint lint-fix docker fmt tidy tools setup-hooks check

# Build variables
VERSION?=0.1.0
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

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
	golangci-lint run ./...

# Run linter and fix issues automatically where possible
lint-fix:
	golangci-lint run --fix ./...

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

# Install development tools
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest

# Set up git hooks for development
setup-hooks:
	@echo "Installing pre-commit hook..."
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed successfully."

# Verify everything (used by CI and before releasing)
check: lint test-coverage-check build
	@echo "All checks passed. Ready for release."
