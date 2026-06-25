.PHONY: dev test build tidy clean

BIN := bin/server
ALERT_CONTROLLER_BIN := bin/alert-controller

dev:
	go run ./cmd/server

test:
	go test ./... -cover

build:
	go build -o $(BIN) ./cmd/server
	go build -o $(ALERT_CONTROLLER_BIN) ./cmd/alert-controller

tidy:
	go mod tidy

clean:
	rm -rf bin data
