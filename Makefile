.PHONY: build test vet lint clean check-deps dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"
DIST     := dist

build:
	go build ./...

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	staticcheck ./...

# enforces the dependency rule: domain must never import adapters
check-deps:
	@echo "Checking dependency rule..."
	@if go list -f '{{.ImportPath}}: {{.Imports}}' ./internal/domain/... | grep -q 'internal/adapters'; then \
		echo "FAIL: internal/domain imports internal/adapters"; exit 1; \
	fi
	@echo "OK"

# build binaries for macOS and Linux — Windows will be added later
dist:
	@mkdir -p $(DIST)
	@echo "Building $(VERSION) for macOS and Linux..."
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST)/ixr-darwin-arm64 ./cmd/ixr
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(DIST)/ixr-darwin-amd64 ./cmd/ixr
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o $(DIST)/ixr-linux-amd64  ./cmd/ixr
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o $(DIST)/ixr-linux-arm64  ./cmd/ixr
	@echo ""
	@echo "Binaries written to $(DIST)/:"
	@ls -lh $(DIST)/ixr-darwin-* $(DIST)/ixr-linux-*
	@echo ""
	@echo "  Apple Silicon (M1/M2/M3/M4):  $(DIST)/ixr-darwin-arm64"
	@echo "  Intel Mac:                    $(DIST)/ixr-darwin-amd64"
	@echo "  Linux (amd64):                $(DIST)/ixr-linux-amd64"
	@echo "  Linux (arm64):                $(DIST)/ixr-linux-arm64"

clean:
	rm -rf bin/ dist/ coverage.txt

ci: vet lint test check-deps
