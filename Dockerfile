# Stage 1: Build the Go binary
# Updated to 1.26 to satisfy go.mod requirement of >= 1.25.7
FROM golang:1.26-alpine AS builder
WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary from your specific path
RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/api/main.go

# Stage 2: Tiny Run Image
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/main .

# Expose your API port
EXPOSE 8080
CMD ["./main"]