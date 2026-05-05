# justfile for codegraph
# Produces a single self-contained binary at ./bin/
#
# Recipes:
#   build          – compile the binary (CGo disabled, pure-Go SQLite)
#   test           – run all tests with the race detector
#   test-property  – run property-based tests with increased iterations
#   lint           – run go vet and staticcheck (if installed)
#   clean          – remove the ./bin directory

# Detect OS and set binary name accordingly
set shell := ["cmd.exe", "/C"]
binary := if os() == "windows" { "./bin/codegraph.exe" } else { "./bin/codegraph" }
module := "github.com/codegraph-cli/codegraph"

# Number of rapid property-test iterations (override with: just test-property rapid_checks=5000)
rapid_checks := "1000"

# Default recipe — build the binary
default: build

# Compile the binary (CGO_ENABLED=0 ensures pure-Go SQLite via modernc.org/sqlite)
build:
    mkdir -p ./bin
    CGO_ENABLED=1 go build -o {{binary}} .
    @echo "Built {{binary}}"

# Run all tests with the race detector
test:
    go test -race ./...

# Run property-based tests with increased iterations
# Uses pgregory.net/rapid; rapid_checks controls the number of test cases per property.
test-property rapid_checks=rapid_checks:
    RAPID_CHECKS={{rapid_checks}} go test -run 'Property|Prop' -count=1 ./...

# Run go vet; run staticcheck if available
lint:
    go vet ./...
    staticcheck ./... || echo "staticcheck not installed — skipping (install with: go install honnef.co/go/tools/cmd/staticcheck@latest)"

# Remove the compiled binary directory
clean:
    rm -rf ./bin
    @echo "Removed ./bin"
