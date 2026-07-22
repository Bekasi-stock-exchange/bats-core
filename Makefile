.PHONY: test test-engine vet build run migrate tidy check

# Run all tests.
test:
	go test ./...

# Engine tests only — the critical gate (steps 1–2). No database required.
test-engine:
	go test ./engine/... -v

vet:
	go vet ./...

build:
	go build ./...

# Start the HTTP server. Requires JAST_DB_DSN in the environment or .env.
run:
	go run .

# Apply migrations 001..005 in order against JAST_DB_DSN.
migrate:
	go run ./cmd/migrate

tidy:
	go mod tidy

# Full local check: vet + build + all tests.
check: vet build test
