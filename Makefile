VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)

.PHONY: build test check
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o air-sensor-api .
test:
	go test ./...
check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...
