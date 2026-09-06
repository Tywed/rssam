.PHONY: build test lint migrate run docker-up docker-down fmt

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
	@DATABASE_URL=$${DATABASE_URL:?set DATABASE_URL} go test ./internal/storage/... -tags=integration -count=1

coverage:
	go test $(PKGS) -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

race:
	go test -race $(PKGS)

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*' -not -path './tmp/*')

lint:
	golangci-lint run $(PKGS)

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
