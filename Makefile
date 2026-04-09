.PHONY: build build-guest-agent install clean test test-integration lint tidy

VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/agarcher/pen/internal/commands.Version=$(VERSION)"

BINARY := pen
BUILD_DIR := build

all: build

build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/pen
	codesign --force --entitlements entitlements/pen.entitlements -s - $(BUILD_DIR)/$(BINARY)

GUEST_GOARCH := $(if $(filter arm64 aarch64,$(shell uname -m)),arm64,amd64)

build-guest-agent:
	GOOS=linux GOARCH=$(GUEST_GOARCH) CGO_ENABLED=0 go build -o $(BUILD_DIR)/pen-agent ./guest/agent

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test -v ./...

test-integration:
	go test -v -tags integration ./...

lint:
	go vet ./...
	@which golangci-lint > /dev/null && golangci-lint run || echo "golangci-lint not installed, skipping"

tidy:
	go mod tidy

help:
	@echo "Available targets:"
	@echo "  build              - Build pen (CGo + codesign)"
	@echo "  build-guest-agent  - Cross-compile guest agent (linux/arm64)"
	@echo "  install            - Install to /usr/local/bin"
	@echo "  clean              - Remove build artifacts"
	@echo "  test               - Run unit tests"
	@echo "  test-integration   - Run integration tests (requires macOS + Apple Silicon)"
	@echo "  lint               - Run linters"
	@echo "  tidy               - Run go mod tidy"
