.PHONY: test check build fmt
fmt:
	gofmt -w cmd internal
test:
	go test -race ./...
check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test -race ./...
build:
	go build -o bin/moderation-service ./cmd/moderation-service
