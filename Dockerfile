# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux go build -o taro ./cmd/taro

# Runtime stage
FROM alpine:latest

RUN apk add --no-cache ca-certificates rclone curl bash && \
    curl -fsSL https://app.snaix.homes/pikpaktui/install.sh | bash && \
    apk del curl bash

WORKDIR /app

COPY --from=builder /build/taro .

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/taro"]
