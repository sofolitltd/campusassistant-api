# Stage 1: Build the Go binary
FROM golang:1.22-alpine AS builder
WORKDIR /app

# Copy go.mod and go.sum from the root to install dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary targeting your specific main file path
RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/api/main.go

# Stage 2: Create a tiny final image
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/main .

# Expose the port your Go app uses (e.g., 8080)
EXPOSE 8080
CMD ["./main"]