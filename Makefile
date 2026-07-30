.PHONY: test test-engine vet build run migrate tidy check docs docs-tool

# Pinned because swag v2 is still a release candidate.
SWAG_VERSION := v2.0.0-rc5

# Run all tests.
test:
	go test ./...

# Engine tests only — the critical gate (steps 1–2). No database required.
test-engine:
	go test ./market/engine/... -v

vet:
	go vet ./...

build:
	go build ./...

# Start the HTTP server. Requires DB_DSN and API_KEY in the environment or .env.
run:
	go run .

# Apply every migration in filename order against DB_DSN.
migrate:
	go run ./cmd/migrate

tidy:
	go mod tidy

# Install the swag CLI. Only needed to regenerate the spec, never to build.
docs-tool:
	go install github.com/swaggo/swag/v2/cmd/swag@$(SWAG_VERSION)

# Regenerate platform/docs/swagger.{yaml,json} from the code annotations.
# Commit the result: the Dockerfile build embeds it and must not need swag.
# --ot yaml,json omits docs.go, so the server binary never links swag.
docs:
	swag init --v3.1 -g main.go -d ./ -o platform/docs --ot yaml,json

# Full local check: vet + build + all tests.
check: vet build test
