.PHONY: all build test lint cover clean

all: build lint test

build:
	go build ./...

test:
	go test ./...

lint:
	golangci-lint run

cover:
	go test -cover ./...

clean:
	rm -f bin/nexa-scheduler
	go clean ./...
