VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist
BINARY  := nugctl

.PHONY: all build test clean release-build help

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

clean:
	rm -rf $(DIST) $(BINARY)

release-build:
	@test "$(VERSION)" != "dev" || (echo "Set VERSION explicitly, e.g. make release-build VERSION=1.0.0" && exit 1)
	rm -rf $(DIST)
	mkdir -p $(DIST)
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_linux_amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_linux_arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_darwin_amd64 .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_darwin_arm64 .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)_windows_amd64.exe .
	@ls -lh $(DIST)/

help:
	@echo "Targets:"
	@echo "  build          Build $(BINARY) for the current platform (VERSION=$(VERSION))"
	@echo "  test           Run tests"
	@echo "  clean          Remove build artifacts"
	@echo "  release-build  Cross-compile release binaries into $(DIST)/ (requires VERSION=...)"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make release-build VERSION=1.0.0"
