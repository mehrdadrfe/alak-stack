# --- Builder Stage ---
FROM golang:1.21 AS builder
WORKDIR /app

# Copy go module files and download deps
COPY go.mod go.sum ./
RUN go mod download

# Copy the actual code
COPY . .

# Build the binary
RUN go build -o alak-controller .

# --- Runtime Stage ---
FROM debian:bookworm-slim
WORKDIR /app

# Copy built binary
COPY --from=builder /app/alak-controller .

# Expose API port
EXPOSE 8080

# Run the service
CMD ["./alak-controller"]
