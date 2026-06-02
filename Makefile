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

# build binary for Apple Silicon (darwin/arm64)
# Intel Mac, Linux, Windows targets will be added later
dist:
	@mkdir -p $(DIST)
	@echo "Building $(VERSION) for Apple Silicon (darwin/arm64)..."
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST)/ixr-darwin-arm64 ./cmd/ixr
	@echo ""
	@echo "Binary written to $(DIST)/ixr-darwin-arm64"

clean:
	rm -rf bin/ dist/ coverage.txt

ci: vet lint test check-deps
