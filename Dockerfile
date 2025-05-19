FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install required system packages
RUN apk add --no-cache git ca-certificates

# Copy go.mod and go.sum first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o restake-yield-ea ./cmd/server

# Use a smaller image for the final build
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/restake-yield-ea .

# Add a non-root user to run the application
RUN adduser -D appuser
USER appuser

# Expose service port
EXPOSE 8080

# Set environment variables with sane defaults
ENV PORT="8080" \
    LOG_LEVEL="info" \
    TIMEOUT="10s" \
    AGGREGATION_MODE="weighted" \
    ENABLE_CIRCUIT_BREAKER="true" \
    ENABLE_VALIDATION="true" \
    ENABLE_METRICS="true" \
    ENABLE_ENTERPRISE_FEATURES="false"

# Start the application
CMD ["./restake-yield-ea"]
