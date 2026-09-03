# The deliverable is ONE static binary. Everything here exists to keep it that
# way: no cgo, no runtime, nothing to install alongside it.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build test lint install clean dist server-up server-down server-logs bench bench-seed

ENGRAM_URL ?= http://127.0.0.1:8081

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o engram ./cmd/engram

test:
	go test ./...

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

install: build
	install -d $(HOME)/.local/bin
	install -m 0755 engram $(HOME)/.local/bin/engram
	@echo "installed: $(HOME)/.local/bin/engram"

# dist builds every release artifact, each with its own checksum line, so
# install.sh can verify what it downloaded without trusting the transfer.
dist:
	rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
	    go build -trimpath -ldflags "$(LDFLAGS)" -o dist/engram$$ext ./cmd/engram || exit 1; \
	  if [ "$$os" = "windows" ]; then \
	    if command -v zip >/dev/null 2>&1; then \
	      (cd dist && zip -q engram_$(VERSION)_$${os}_$${arch}.zip engram$$ext && rm engram$$ext); \
	    else \
	      echo "  note: zip is not installed — packing windows as .tar.gz instead."; \
	      echo "  (the release workflow runs on a runner that has zip, so published"; \
	      echo "   releases always carry the .zip install.sh points Windows users at)"; \
	      (cd dist && tar czf engram_$(VERSION)_$${os}_$${arch}.tar.gz engram$$ext && rm engram$$ext); \
	    fi; \
	  else \
	    (cd dist && tar czf engram_$(VERSION)_$${os}_$${arch}.tar.gz engram && rm engram); \
	  fi; \
	done
	cd dist && sha256sum * > SHA256SUMS
	@ls -1 dist

clean:
	rm -rf dist engram

# --- server (the store itself) ----------------------------------------------
server-up:
	cd server && docker compose up -d --build

server-down:
	cd server && docker compose down

server-logs:
	cd server && docker compose logs -f --tail 100 app

# Seed a local store with the example corpus, then measure. ENGRAM_INGEST_TOKEN
# must match the running server's (it is in server/.env).
bench-seed:
	cd server && python3 bin/import_tree.py bench/corpus --owner acme --repo shared \
	  --url $(ENGRAM_URL) --token "$$ENGRAM_INGEST_TOKEN"

bench:
	cd server && python3 bench/baseline_grep.py
	cd server && python3 bench/eval_index.py --url $(ENGRAM_URL) --prefix acme/shared
