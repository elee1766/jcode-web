.PHONY: all gen web build dev check clean

all: build

gen:
	cd web && pnpm install --frozen-lockfile
	buf generate

web:
	cd web && pnpm install --frozen-lockfile && pnpm build
	touch web/dist/.keep

build: web
	go build -trimpath -ldflags "-s -w -X main.version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o jcode-web ./cmd/jcode-web

check:
	buf lint
	go vet ./...
	cd web && pnpm check

clean:
	rm -rf jcode-web web/dist/assets web/dist/*.html web/dist/*.js web/dist/*.webmanifest web/dist/*.svg
