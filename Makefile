# Between releases: the last tag plus the commits since it, e.g. 0.3.0-alpha.1+4.
TAG := $(shell git describe --tags --abbrev=0 2>/dev/null)
BUILD := $(shell git rev-list --count $(TAG)..HEAD 2>/dev/null)$(if $(shell git status --porcelain --untracked-files=no 2>/dev/null),.dirty)
VERSION ?= $(if $(TAG),$(patsubst v%,%,$(TAG))+$(BUILD),dev)
LDFLAGS := -s -w -X github.com/jufianto/sevjala/cmd.version=$(VERSION)
RACE := $(if $(filter windows,$(shell go env GOOS)),,-race)

.PHONY: build build-all build-win7 test test-integration lint release ui

ui:
	cd ui && npm ci --no-fund --no-audit && npm run build

build: ui
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o sevjala .

test:
	go test $(RACE) ./...
	cd ui && npm test

test-integration:
	sh scripts/test-integration.sh

lint:
	golangci-lint run

build-all:
	SEVJALA_VERSION=$(VERSION) goreleaser release --snapshot --clean

build-win7:
	SEVJALA_VERSION=$(VERSION) goreleaser build --snapshot --clean --id win7

release:
	goreleaser release --clean
