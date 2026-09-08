.PHONY: build test lint vuln fuzz migrate run docker-up docker-down fmt

BIN := rssam
PKGS := $(shell go list ./... | grep -vE '/tmp(/|$$)')
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H%M%SZ)
LDFLAGS := -s -w \
	-X rssam/internal/version.Version=$(VERSION) \
	-X rssam/internal/version.Commit=$(COMMIT) \
	-X rssam/internal/version.Date=$(DATE)

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BIN) ./cmd/rssam

test:
	go test $(PKGS)

test-integration:
	@DATABASE_URL=$${DATABASE_URL:?set DATABASE_URL} go test ./internal/storage/... ./internal/e2e/... -tags=integration -count=1

coverage:
	go test $(PKGS) -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

race:
	go test -race $(PKGS)

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*' -not -path './tmp/*')

lint:
	go vet $(PKGS)
	go tool staticcheck $(PKGS)
	golangci-lint run ./...

vuln:
	go tool govulncheck ./...

# Run every Fuzz* target for FUZZTIME each (go test can fuzz only one target
# per invocation). New crashers land in <pkg>/testdata/fuzz/ and fail the run.
FUZZTIME ?= 30s
fuzz:
	@set -e; for pkg in $$(go list $(PKGS)); do \
	  for f in $$(go test -list '^Fuzz' $$pkg 2>/dev/null | grep '^Fuzz' || true); do \
	    echo "== $$pkg $$f"; \
	    go test -run='^$$' -fuzz="^$$f\$$" -fuzztime=$(FUZZTIME) $$pkg; \
	  done; \
	done

migrate:
	@DATABASE_URL=$${DATABASE_URL:?set DATABASE_URL} go run ./cmd/rssam -migrate

run:
	@DATABASE_URL=$${DATABASE_URL:?set DATABASE_URL} LISTEN_ADDR=$${LISTEN_ADDR:-:8080} go run -ldflags="$(LDFLAGS)" ./cmd/rssam

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down

docker-build:
	docker compose build app

ui-css:
	@echo "app.css is committed; edit web/static/app.css directly or run tailwind locally"
