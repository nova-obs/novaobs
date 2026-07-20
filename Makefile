.PHONY: dev test build tidy clean docker-build docker-push docker-build-push docker-inspect

BIN := bin/server
DOCKER ?= docker
REGISTRY ?= hub-test.service.ucloud.cn/logsplatfrom
IMAGE_NAME ?= novaapm-backend
TAG ?= 0.1.1
PLATFORM ?= linux/amd64
IMAGE ?= $(REGISTRY)/$(IMAGE_NAME):$(TAG)

dev:
	go run ./cmd/server

test:
	go test ./... -cover

build:
	go build -o $(BIN) ./cmd/server

docker-build:
	$(DOCKER) buildx build --platform $(PLATFORM) --load -t $(IMAGE) .

docker-push:
	$(DOCKER) push $(IMAGE)

docker-build-push:
	$(DOCKER) buildx build --platform $(PLATFORM) --push -t $(IMAGE) .

docker-inspect:
	$(DOCKER) image inspect $(IMAGE) --format '{{.Os}}/{{.Architecture}}'

tidy:
	go mod tidy

clean:
	rm -rf bin data
