# Stage 1: Builder
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build the binary with the new name
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /agnostic-agones-sidecar .

# Stage 2: Final Image
FROM alpine:3.20
# Copy the binary with the new name
COPY --from=builder /agnostic-agones-sidecar /agnostic-agones-sidecar
# Set the entrypoint to the new binary name
ENTRYPOINT ["/agnostic-agones-sidecar"]