.PHONY: build frontend test dev-backend clean

GOCACHE ?= /tmp/rowlight-gocache
GOMODCACHE ?= /tmp/rowlight-gomodcache

build: frontend
	mkdir -p bin
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go build -trimpath -ldflags="-s -w" -o bin/rowlight .

frontend:
	npm --prefix web ci
	npm --prefix web run build

test: frontend
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go test ./...

dev-backend: frontend
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) go run . -no-open -port 7070

clean:
	rm -rf bin web/dist
