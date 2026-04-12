# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o taro-transfer .

# Runtime stage
FROM alpine:latest

# Install rclone
RUN apk add --no-cache rclone ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/taro-transfer .

# Copy rclone config (will be provided at runtime via volume mount or build arg)
# COPY rclone.conf /root/.config/rclone/rclone.conf

# Expose port
EXPOSE 7860

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:7860/health || exit 1

# Run
CMD ["./taro-transfer"]
