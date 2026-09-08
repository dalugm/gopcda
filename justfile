# Optional development shortcuts; each command can also run directly.
check: lint test build

lint:
    golangci-lint run ./...

fmt:
    golangci-lint fmt ./...

test:
    go test -race ./...

build:
    go build ./...
