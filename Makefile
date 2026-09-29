.PHONY: build web test race browser release

build: web
	CGO_ENABLED=0 go build -trimpath -o bin/agentmirror ./cmd/agentmirror

web:
	npm run build:web

test:
	go test . ./cmd/... ./internal/...

race:
	go test -race . ./cmd/... ./internal/...

browser:
	npm run test:browser

release:
	bash scripts/build-release.sh
