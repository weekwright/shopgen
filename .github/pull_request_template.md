## What and why

## How it was tested

- [ ] `gofmt -l .` is empty, `go vet ./...` is clean, `go test -race ./...` passes
- [ ] New behavior has a test (the fake Admin API in `fake_test.go` covers store-facing code)
- [ ] Docs updated if flags, profile fields or output changed
