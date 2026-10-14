## What and why

## How it was tested

- [ ] `gofmt -l .` is empty, `go vet ./...` is clean, `go test -race ./...` passes
- [ ] New behavior has a test (store-facing code is tested against `internal/shopify/shopifytest`)
- [ ] Docs updated if flags, profile fields or output changed
