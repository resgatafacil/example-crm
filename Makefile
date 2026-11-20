.PHONY: run mock test build lint fmt tidy

run:
	go run ./cmd/funil

mock:
	go run ./cmd/mockresgata

test:
	go test -race -count=1 ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/funil ./cmd/funil
	CGO_ENABLED=0 go build -trimpath -o bin/mockresgata ./cmd/mockresgata

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "execute: make fmt" && exit 1)
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy
