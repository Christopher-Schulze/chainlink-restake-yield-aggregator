FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a static binary with version metadata injected via -ldflags.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o restake-yield-ea ./cmd/server

# --- runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/restake-yield-ea .

RUN adduser -D -u 10001 appuser
USER appuser

EXPOSE 8080

ENV PORT="8080" \
    LOG_LEVEL="info" \
    LOG_FORMAT="text" \
    TIMEOUT="10s" \
    AGGREGATION_MODE="weighted" \
    ENABLE_CIRCUIT_BREAKER="true" \
    ENABLE_VALIDATION="true" \
    ENABLE_METRICS="true" \
    ENABLE_ENTERPRISE_FEATURES="false"

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/readyz || exit 1

CMD ["./restake-yield-ea"]
