# Build binary for current OS/arch
build:
    @goreleaser build --snapshot --clean

# Create a release build
release:
    @goreleaser release

# Run every test. Pass go test flags through, e.g. `just test -v -count=1`
test *flags:
    @go test {{flags}} ./...

# Run tests without the CLI scripts, which log every script line under -v
unit-test *flags:
    @go test {{flags}} -skip TestScripts ./...

# Run every test under the race detector, as CI does
test-race *flags:
    @go test -race {{flags}} ./...

# Regenerate the golden output embedded in the CLI scripts
update-scripts:
    @go test . -run TestScripts -update

# Run linters
lint:
    @golangci-lint run

# Run the binary
run:
    @go run .

# Clean build artifacts
clean:
    @rm -rf dist/ pipefitter
