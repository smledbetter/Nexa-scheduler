.PHONY: all build test lint cover smoke clean

all: build lint test

build:
	go build ./...

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
