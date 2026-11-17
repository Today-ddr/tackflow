SHELL := /bin/sh

GO_FILES := $(shell find cmd internal migrations web -name '*.go' -not -path '*/node_modules/*') assets.go

.PHONY: check fmt-check vet lint go-test go-build web-deps web-lint web-typecheck web-test web-build build dev docker-build docker-build-updater compose-up

check: fmt-check vet lint web-deps web-build go-test web-lint web-typecheck web-test go-build

fmt-check:
	@files="$(GO_FILES)"; test -z "$$(gofmt -l $$files)" || (gofmt -l $$files; exit 1)

vet:
	go vet ./...

lint:
	golangci-lint run ./...

go-test: web-build
	go test ./...

go-build: web-build
	mkdir -p bin
	go build -trimpath -ldflags "-X main.version=$$(cat VERSION) -X main.commit=$$(git rev-parse --short HEAD) -X main.buildTime=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/tack ./cmd/tack
	go build -trimpath -o bin/tack-updater ./cmd/updater
	go build -trimpath -o bin/tack-bridge ./cmd/tack-bridge

web-deps:
	pnpm -C web install --frozen-lockfile

web-lint:
	pnpm -C web lint

web-typecheck:
	pnpm -C web typecheck

web-test:
	pnpm -C web test -- --run

web-build: web-deps
	pnpm -C web build

build: go-build

dev:
	./scripts/dev.sh

docker-build:
	docker build \
		--target app \
		--build-arg VERSION=$$(cat VERSION) \
		--build-arg COMMIT=$$(git rev-parse --short HEAD) \
		--build-arg BUILD_TIME=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
		-t ghcr.io/$${TACK_GHCR_OWNER:-today-ddr}/tackflow:$${TACK_IMAGE_TAG:-dev} .

docker-build-updater:
	docker build \
		--target updater \
		-t ghcr.io/$${TACK_GHCR_OWNER:-today-ddr}/tackflow-updater:$${TACK_UPDATER_TAG:-dev} .

compose-up:
	docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
