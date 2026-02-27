.PHONY: all build test lint cover smoke clean webhook

all: build lint test

build:
	go build ./...

webhook:
	CGO_ENABLED=0 go build -o bin/nexa-webhook ./cmd/webhook

test:
	go test ./...

lint:
	golangci-lint run

cover:
	go test -cover ./...

smoke:
	go test -tags smoke -v -count=1 -timeout 10m ./test/smoke/

clean:
	rm -f bin/nexa-scheduler
	go clean ./...
