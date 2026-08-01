.PHONY: test build server tidy

test:
	go test ./...

tidy:
	go mod tidy

build:
	mkdir -p bin
	go build -o bin/server ./cmd/server
	go build -o bin/build_segments ./cmd/build_segments
	go build -o bin/pipeline ./cmd/pipeline
	go build -o bin/loadraw ./cmd/loadraw
	go build -o bin/loadcontent ./cmd/loadcontent
	go build -o bin/reconcile ./cmd/reconcile
	go build -o bin/bench ./cmd/bench

server:
	go run ./cmd/server
