# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

# Set GOTOOLCHAIN to auto to allow using newer toolchain if needed
ENV GOTOOLCHAIN=auto

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the comprehensive example
WORKDIR /build/examples/comprehensive
RUN CGO_ENABLED=0 GOOS=linux go build -o comprehensive-example main.go

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates wget

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/examples/comprehensive/comprehensive-example .

EXPOSE 8080 9090

CMD ["./comprehensive-example"]

