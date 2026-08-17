.PHONY: build test vet lint clean check-deps eval

build:
	go build ./...

# runs the golden question set (eval/golden.yaml) against a running ixr
# instance; override MODELS/BASE_URL/QUESTIONS as needed, e.g.:
#   make eval MODELS=gpt-4o,claude-sonnet-4-6,auto BASE_URL=http://localhost:7000/v1
MODELS ?= auto
BASE_URL ?= http://localhost:7000/v1
QUESTIONS ?= eval/golden.yaml
eval:
	go run ./cmd/ixr-eval -base-url $(BASE_URL) -questions $(QUESTIONS) -models $(MODELS)

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

clean:
	rm -rf bin/ dist/ coverage.txt

ci: vet lint test check-deps
