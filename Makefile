BIN := bin/purge
PKG := ./...
export CGO_ENABLED = 0

.PHONY: build test lint fmt clean

build:
	go build -trimpath -ldflags "-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o $(BIN) ./cmd/purge

test:
	go test $(PKG)

lint:
	gofmt -l . | tee /dev/stderr | test -z "$$(cat)"
	go vet $(PKG)
	golangci-lint run

fmt:
	gofmt -w .

clean:
	rm -rf bin dist
