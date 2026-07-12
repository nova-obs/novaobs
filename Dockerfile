ARG GO_VERSION=1.26.1
ARG KUBECTL_VERSION=v1.35.3

FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/novaapm ./cmd/server

FROM registry.k8s.io/kubectl:${KUBECTL_VERSION} AS kubectl

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 novaapm \
    && adduser -S -D -H -u 10001 -G novaapm novaapm \
    && mkdir -p /app/configs /tmp/novaapm \
    && chown -R 10001:10001 /app /tmp/novaapm
COPY --from=builder /out/novaapm /app/novaapm
COPY --from=kubectl /bin/kubectl /usr/local/bin/kubectl
WORKDIR /app
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/novaapm"]
