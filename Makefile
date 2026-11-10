# spotify-tui
#
# CGO is required (the FLAC decoder has no pure-Go fallback), so every target that
# builds keeps it on explicitly instead of relying on the default.

BIN     := spotify-tui
PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin
PKG     := ./cmd/spotify-tui

.PHONY: build install run test check fmt clean

build: ## compile into ./spotify-tui
	CGO_ENABLED=1 go build -o $(BIN) $(PKG)

install: ## build and install into $(PREFIX)/bin (default ~/.local/bin)
	mkdir -p $(BINDIR)
	CGO_ENABLED=1 go build -o $(BINDIR)/$(BIN) $(PKG)
	@echo "installed $(BINDIR)/$(BIN)"

run: ## compile and run from the worktree
	CGO_ENABLED=1 go run $(PKG)

test: ## the full suite
	go test -count=1 ./...

check: ## what CI would run: format, vet, tests, build
	@test -z "$$(gofmt -l cmd internal)" || { echo "gofmt needed:"; gofmt -l cmd internal; exit 1; }
	go vet ./...
	go test -count=1 ./...
	CGO_ENABLED=1 go build ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -f $(BIN)
