# ---------- build stage ----------
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install CA certificates and git (if needed for 'go get')
RUN apk add --no-cache ca-certificates git

# Copy Go modules first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code & build
COPY . .
RUN go build -o alak-gatekeeper .

# ---------- slim run stage ----------
FROM alpine:3.20

WORKDIR /app
COPY --from=builder /app/alak-gatekeeper .

# Set non-root user (optional)
# RUN adduser -D -H -u 10001 alak-gatekeeper
# USER alak-gatekeeper

EXPOSE 8090
ENTRYPOINT ["./alak-gatekeeper"]
