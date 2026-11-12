.PHONY: check build vet test pack

check: build vet test

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

pack:
	go run ./cmd/kos-notification-pack
