# --- Stage: builder ---
FROM golang:1.22 AS builder
WORKDIR /app

# Copy dependency metadata first (enables caching)
COPY go.mod go.sum ./
RUN go mod download

# Then copy source
COPY . .

# Build the binary
RUN go build -o alak-geo .

# --- Stage: runtime ---
FROM debian:bookworm-slim
WORKDIR /app

COPY --from=builder /app/alak-geo .

# Expose port if needed
EXPOSE 8081

CMD ["./alak-geo"]
