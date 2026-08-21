# Build binary for current OS/arch
build:
    @goreleaser build --snapshot --clean

# Create a release build
release:
    @goreleaser release

# Run tests
test:
    @go test ./...

# Run linters
lint:
    @golangci-lint run

# Run the binary
run:
    @go run .

# Clean build artifacts
clean:
    @rm -rf dist/ pipefitter
