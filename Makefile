.PHONY: fmt-check vet test build check run

fmt-check:
	@test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test -race -cover ./...

build:
	go build ./cmd/api

check: fmt-check vet test build

run:
	go run ./cmd/api
