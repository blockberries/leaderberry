.PHONY: all generate build test lint clean

# Cramberry code generator
CRAMBERRY := cramberry

# Schema directory contains .cram files (Cramberry schema definitions).
# These are build inputs similar to .proto files - they define network-serializable types.
# Location at project root is intentional, as they're not part of the Go package tree.
SCHEMA_DIR := ./schema

# Generated code output directory (do not edit generated files directly)
GEN_DIR := ./types/generated

all: generate build

generate:
	@echo "Generating cramberry code..."
	@mkdir -p $(GEN_DIR)
	$(CRAMBERRY) generate -lang go -out $(GEN_DIR) $(SCHEMA_DIR)/*.cram

build: generate
	go build ./...

test: generate
	go test -race -v ./...

lint:
	golangci-lint run

clean:
	rm -rf $(GEN_DIR)/*.go
	go clean ./...

# Development helpers
.PHONY: check
check: build test lint
	@echo "All checks passed"
