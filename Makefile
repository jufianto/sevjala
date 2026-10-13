VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/jufianto/sevjala/cmd.version=$(VERSION)
RACE := $(if $(filter windows,$(shell go env GOOS)),,-race)

.PHONY: build build-all build-win7 test test-integration lint release

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o sevjala .

test:
	go test $(RACE) ./...

test-integration:
	sh scripts/test-integration.sh

lint:
	golangci-lint run

build-all:
	goreleaser release --snapshot --clean

build-win7:
	goreleaser build --snapshot --clean --id win7

release:
	goreleaser release --clean
