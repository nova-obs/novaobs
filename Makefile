.PHONY: dev test build tidy clean

BIN := bin/server

dev:
	go run ./cmd/server

test:
	go test ./... -cover

build:
	go build -o $(BIN) ./cmd/server

tidy:
	go mod tidy

clean:
	rm -rf bin data
