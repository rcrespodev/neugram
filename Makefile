BASE_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
WORKTREE := $(shell [ "$$(git rev-parse --git-dir 2>/dev/null)" != "$$(git rev-parse --git-common-dir 2>/dev/null)" ] && basename "$$(git rev-parse --show-toplevel)")
VERSION ?= $(BASE_VERSION)$(if $(WORKTREE),-wt-$(WORKTREE))
LDFLAGS := -X github.com/rcrespodev/neugram/internal/cli.Version=$(VERSION)

.PHONY: build install test vet e2e

build:
	go build -ldflags "$(LDFLAGS)" -o bin/neugram ./cmd/neugram

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/neugram

test:
	go test ./...

vet:
	go vet ./...

e2e:
	go test -tags e2e ./internal/e2e/...
