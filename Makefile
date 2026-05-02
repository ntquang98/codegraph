# Makefile for codegraph
# Produces a single self-contained binary at ./bin/codegraph.
#
# Targets:
#   build          – compile the binary (CGo disabled, pure-Go SQLite)
#   test           – run all tests with the race detector
#   test-property  – run property-based tests with increased iterations
#   lint           – run go vet and staticcheck (if installed)
#   clean          – remove the ./bin directory

BINARY  := ./bin/codegraph.exe
MODULE  := github.com/codegraph-cli/codegraph
GOFLAGS :=

# Number of rapid property-test iterations (override with: make test-property RAPID_CHECKS=5000)
RAPID_CHECKS ?= 1000

.PHONY: all build test test-property lint clean

## all: default target — build the binary
all: build

## build: compile the binary (CGO_ENABLED=0 ensures pure-Go SQLite via modernc.org/sqlite)
build:
	@mkdir -p ./bin
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BINARY) .
	@echo "Built $(BINARY)"

## test: run all tests with the race detector
test:
	go test -race ./...

## test-property: run property-based tests with increased iterations
## Uses pgregory.net/rapid; RAPID_CHECKS controls the number of test cases per property.
test-property:
	RAPID_CHECKS=$(RAPID_CHECKS) go test -run 'Property|Prop' -count=1 ./...

## lint: run go vet; run staticcheck if available
lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck not installed — skipping (install with: go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
	fi

## clean: remove the compiled binary directory
clean:
	rm -rf ./bin
	@echo "Removed ./bin"
