BIN := bin/rootout
PKG := ./...
export CGO_ENABLED = 0

.PHONY: build test lint fmt clean install release-check snapshot

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

# release-check is the gate before tagging (D-032): a CHANGELOG section for the
# version about to be tagged, then tests, lint and the GoReleaser config.
# Usage: make release-check NEXT=v0.4.0
release-check:
	@test -n "$(NEXT)" || { echo "usage: make release-check NEXT=vX.Y.Z"; exit 2; }
	scripts/release_notes.sh "$(NEXT)" >/dev/null && echo "CHANGELOG has $(NEXT)"
	$(MAKE) test lint
	goreleaser check

# snapshot builds both archives and the cask into dist/ without publishing.
snapshot:
	goreleaser release --snapshot --clean --skip=publish
