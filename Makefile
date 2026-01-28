.PHONY: all generate build test lint clean

CRAMBERRY := cramberry
SCHEMA_DIR := ./schema
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
