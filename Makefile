BIN := bin/rootout
PKG := ./...
export CGO_ENABLED = 0

.PHONY: build test lint fmt clean install release-check snapshot contract

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

# release-check is the gate before tagging (D-032): the licence public key and
# key service URL are filled in, a CHANGELOG section for the version about to
# be tagged, then tests, lint and the GoReleaser config.
# Usage: make release-check NEXT=v0.4.0
release-check:
	@test -n "$(NEXT)" || { echo "usage: make release-check NEXT=vX.Y.Z"; exit 2; }
	@if grep -q 'PublicKeyHex = ""' internal/license/codec.go; then \
		echo "release-check: internal/license/codec.go has an empty PublicKeyHex; every key would fail. Paste the public key first."; exit 1; fi
	@if grep -q 'CHANGEME' internal/license/client.go; then \
		echo "release-check: internal/license/client.go still has the CHANGEME ServiceURL. Set the deployed key service host first."; exit 1; fi
	scripts/release_notes.sh "$(NEXT)" >/dev/null && echo "CHANGELOG has $(NEXT)"
	$(MAKE) test lint
	goreleaser check

# snapshot builds both archives and the cask into dist/ without publishing.
snapshot:
	goreleaser release --snapshot --clean --skip=publish

# contract runs the licence client against a key service started with
# `npm run dev` in service/ (see docs/kb/membership.md).
contract:
	ROOTOUT_SERVICE_URL=$${ROOTOUT_SERVICE_URL:-http://127.0.0.1:8787} go test -tags contract ./internal/license/ -run TestContract -v
