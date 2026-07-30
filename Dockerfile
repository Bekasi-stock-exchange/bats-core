FROM golang:1.26.4-alpine AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the main application and the migrate tool
RUN go build -o /app/bin/main .
RUN go build -o /app/bin/migrate ./cmd/migrate

FROM alpine:latest

WORKDIR /app

# Copy the compiled binaries
COPY --from=builder /app/bin/main /app/main
COPY --from=builder /app/bin/migrate /app/migrate

# Copy the migrations directory so the migrate tool can find it
COPY --from=builder /app/migrations ./migrations

EXPOSE 8080

CMD ["/app/main"]
