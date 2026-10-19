VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/rcrespodev/neugram/internal/cli.Version=$(VERSION)

.PHONY: build install test vet

build:
	go build -ldflags "$(LDFLAGS)" -o bin/neugram ./cmd/neugram

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/neugram

test:
	go test ./...

vet:
	go vet ./...
