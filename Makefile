.PHONY: build frontend test installer-test dev-backend release-darwin benchmark browser-smoke clean

GOCACHE ?= /tmp/rowlight-gocache
GOMODCACHE ?= /tmp/rowlight-gomodcache
VERSION ?= dev
RELEASE_DIR ?= release

build: frontend
	mkdir -p bin
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build -trimpath -ldflags="-s -w" -o bin/rowlight .

frontend:
	npm --prefix web ci
	npm --prefix web run build

test: frontend
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test ./...

installer-test:
	sh -n ./install.sh

dev-backend: frontend
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go run . -no-open -port 7070

release-darwin: frontend
	VERSION=$(VERSION) RELEASE_DIR=$(RELEASE_DIR) GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) ./scripts/release-darwin.sh

benchmark: build
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) ./scripts/benchmark.sh ./bin/rowlight

browser-smoke:
	node ./scripts/webdriver-smoke.mjs

clean:
	rm -rf bin web/dist $(RELEASE_DIR)
