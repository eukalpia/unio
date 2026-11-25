GO      ?= go
PKGS    ?= ./...
FUZZTIME ?= 30s

CONSOLE_DIR    ?= web/console
CONSOLE_ASSETS ?= internal/console/assets

.PHONY: all build test test-race lint vuln fuzz bench proto fmt tidy console check

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

# Builds the web console and copies dist/ into the package that embeds it.
console: $(CONSOLE_DIR)/node_modules
	cd $(CONSOLE_DIR) && npm run build
	rm -rf $(CONSOLE_ASSETS)/assets
	mkdir -p $(CONSOLE_ASSETS)
	cp -R $(CONSOLE_DIR)/dist/. $(CONSOLE_ASSETS)/

$(CONSOLE_DIR)/node_modules: $(CONSOLE_DIR)/package-lock.json
	cd $(CONSOLE_DIR) && npm ci
	touch $@

check: lint build test-race
