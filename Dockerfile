# Stage 1: Build agent binary + retain Go toolchain for runtime
FROM golang:1.26.3-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -o /app/agent ./cmd/agent

# Stage 2: Runtime with Go toolchain + Node.js + pi
FROM alpine:3.20
RUN apk add --no-cache \
    nodejs \
    npm \
    git \
    openssh-client \
    gcc \
    musl-dev \
    ca-certificates \
    tzdata

# Allow git operations on mounted project directories (different UID)
RUN git config --global --add safe.directory '*'

# Copy Go toolchain from builder (for go test/build of external projects)
COPY --from=builder /usr/local/go /usr/local/go
ENV GOROOT=/usr/local/go
ENV GOPATH=/root/go
ENV PATH="${PATH}:/usr/local/go/bin:/root/go/bin"

# Install pi coding agent
RUN npm install -g @earendil-works/pi-coding-agent

# Copy agent binary from builder
COPY --from=builder /app/agent /usr/local/bin/agent

# Copy example config for reference
COPY config.example.yaml /app/config.example.yaml

WORKDIR /home/agent

ENTRYPOINT ["/usr/local/bin/agent"]