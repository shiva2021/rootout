BIN := bin/rootout
PKG := ./...
export CGO_ENABLED = 0

.PHONY: build test lint fmt clean install

build:
	go build -trimpath -ldflags "-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o $(BIN) ./cmd/rootout

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

# install copies the binary to ~/.local/bin (first on PATH on this machine).
install: build
	mkdir -p $(HOME)/.local/bin
	install -m 0755 $(BIN) $(HOME)/.local/bin/rootout
	@echo "installed $$($(HOME)/.local/bin/rootout --version) to $(HOME)/.local/bin"
