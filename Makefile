VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/weekwright/shopgen/cmd.Version=$(VERSION)

.PHONY: build test check fmt assets demo clean

build: ## Build ./shopgen
	go build -trimpath -ldflags "$(LDFLAGS)" -o shopgen .

test: ## Run the test suite with the race detector
	go test -race ./...

check: ## Everything CI runs
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	go test -race ./...

fmt:
	gofmt -w .

demo: ## Re-record the run shown in the README screenshot
	DEMO_OUT=$(CURDIR)/docs/assets/src/run-demo.ansi go test -run TestDocsDemo -count=1 ./internal/shopgen/

assets: ## Re-render docs/assets/*.png (needs Node + Playwright)
	node scripts/render-assets.cjs

clean:
	rm -rf shopgen dist
