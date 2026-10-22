GO      ?= go
PKGS    ?= ./...
FUZZTIME ?= 30s

.PHONY: all build test test-race lint vuln fuzz bench proto fmt tidy check

all: check

build:
	$(GO) build $(PKGS)

test:
	$(GO) test -count=1 $(PKGS)

test-race:
	$(GO) test -race -count=1 $(PKGS)

lint:
	test -z "$$(gofmt -l .)"
	$(GO) vet $(PKGS)
	staticcheck $(PKGS)

vuln:
	govulncheck $(PKGS)

# Runs every Fuzz target in PKGS for FUZZTIME each.
fuzz:
	@for pkg in $$($(GO) list $(PKGS)); do \
	  for f in $$($(GO) test -list '^Fuzz' $$pkg | grep '^Fuzz'); do \
	    echo "== $$pkg $$f"; $(GO) test -run '^$$' -fuzz "^$$f$$" -fuzztime $(FUZZTIME) $$pkg || exit 1; \
	  done; \
	done

bench:
	$(GO) test -run '^$$' -bench . -benchmem $(PKGS)

proto:
	buf lint
	buf generate

fmt:
	gofmt -w .

tidy:
	$(GO) mod tidy

check: lint build test-race
