# Stage 1: Builder
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /sidecar .

# Stage 2: Final Image
FROM alpine:3.20
COPY --from=builder /sidecar /sidecar
EXPOSE 9999
ENTRYPOINT ["/sidecar"]
