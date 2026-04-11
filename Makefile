.PHONY: build install clean test test-integration lint tidy image image-release

VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/agarcher/pen/internal/commands.Version=$(VERSION)"

BINARY := pen
BUILD_DIR := build

all: build

build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/pen
	codesign --force --entitlements entitlements/pen.entitlements -s - $(BUILD_DIR)/$(BINARY)

# Build for a specific macOS target (used by CI).
build-darwin-%:
	CGO_ENABLED=1 GOOS=darwin GOARCH=$* go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-$* ./cmd/pen

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test -v ./...

test-integration: build
	@test -f $(HOME)/.config/pen/images/vmlinuz && test -f $(HOME)/.config/pen/images/initrd || \
		(echo "error: VM image not found at $(HOME)/.config/pen/images/ — run 'make image' first" && exit 1)
	go test -v -tags integration -count=1 -timeout 15m ./...

lint:
	go vet ./...
	@which golangci-lint > /dev/null && golangci-lint run || echo "golangci-lint not installed, skipping"

tidy:
	go mod tidy

# Build VM image and install to local cache (~/.config/pen/images/).
image:
	./images/alpine/build.sh

# Build VM image as release artifacts (arch-suffixed, in build/).
image-release:
	RELEASE_DIR=$(BUILD_DIR) ./images/alpine/build.sh

help:
	@echo "Available targets:"
	@echo "  build          - Build pen (CGo + codesign)"
	@echo "  install        - Install to /usr/local/bin"
	@echo "  clean          - Remove build artifacts"
	@echo "  test           - Run unit tests"
	@echo "  test-integration - Run integration tests (requires macOS)"
	@echo "  lint           - Run linters"
	@echo "  tidy           - Run go mod tidy"
	@echo "  image          - Build VM image (installs to ~/.config/pen/images/)"
	@echo "  image-release  - Build VM image as release artifact (in build/)"
