FROM golang:1.25.3-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/go-exchange-api ./cmd/api \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/go-exchange-worker ./cmd/worker

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/go-exchange-api /app/go-exchange-api
COPY --from=builder /out/go-exchange-worker /app/go-exchange-worker
COPY config /app/config

EXPOSE 3000
