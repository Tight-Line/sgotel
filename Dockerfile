# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build binary
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o sgotel ./cmd/sgotel

# Runtime stage
FROM alpine:3.23.3

LABEL org.opencontainers.image.source=https://github.com/Tight-Line/sgotel

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/sgotel .

# Create non-root user
RUN addgroup -g 1000 sgotel && \
    adduser -u 1000 -G sgotel -s /bin/sh -D sgotel && \
    chown -R sgotel:sgotel /app

USER sgotel

# Expose webhook port
EXPOSE 8080

ENTRYPOINT ["./sgotel"]
