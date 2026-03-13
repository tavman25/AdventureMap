# ─── Stage 1: Build ─────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git (needed for go mod download with VCS)
RUN apk add --no-cache git gcc musl-dev

# Copy dependency files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy all source
COPY . .

# Build a static binary
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /travel-map .

# ─── Stage 2: Runtime ────────────────────────────────────────
FROM alpine:3.20

WORKDIR /app

# ca-certificates for HTTPS outbound calls (if any)
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy binary from builder
COPY --from=builder /travel-map /app/travel-map

# Persistent data volume
VOLUME ["/app/data"]

ENV PORT=8080
ENV DB_PATH=/app/data/travel.db
ENV GIN_MODE=release

EXPOSE 8080

USER appuser

ENTRYPOINT ["/app/travel-map"]
