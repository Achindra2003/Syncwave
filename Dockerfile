# ── Build Stage ──────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o syncwave ./cmd/server

# ── Runtime Stage ────────────────────────────────────────────────
# Static files are embedded in the binary via Go's embed package,
# so we only need to copy the single executable.
FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/syncwave .
EXPOSE 8080
CMD ["./syncwave"]
