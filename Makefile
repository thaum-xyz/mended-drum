IMAGE ?= ghcr.io/thaum-xyz/mended-drum
TAG ?= dev

.PHONY: run build test vet tidy docker-build

run:
	go run ./cmd/mended-drum

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mended-drum ./cmd/mended-drum

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

docker-build:
	docker build -t $(IMAGE):$(TAG) .
