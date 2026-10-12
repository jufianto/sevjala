VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/jufianto/sevjala/cmd.version=$(VERSION)
RACE := $(if $(filter windows,$(shell go env GOOS)),,-race)

.PHONY: build test lint

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o sevjala .

test:
	go test $(RACE) ./...

lint:
	golangci-lint run
