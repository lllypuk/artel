# Stage 1: build
FROM golang:1.26.3-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -o /app/agent ./cmd/agent

# Stage 2: runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/agent .
COPY config.example.yaml /app/config.example.yaml
VOLUME ["/app/data"]
ENTRYPOINT ["/app/agent", "/app/config.yaml"]
