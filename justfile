#
# Simple justfile for developing this project
# See https://github.com/casey/just
#

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

default:
    @just --list

# Build the application binary.
build:
    go build ./cmd/prometheus-snapshot-manager

# Build all packages.
build-all:
    go build ./...

# Run static checks.
lint:
    go fmt ./...
    go vet ./...

# Run unit tests.
test:
    go test -v ./...

# Run all local CI-like checks.
check: lint test build

# Run the daemon with the default config path.
run:
    go run ./cmd/prometheus-snapshot-manager run --config ./config.example.yaml

# Validate configuration file.
validate:
    go run ./cmd/prometheus-snapshot-manager validate --config ./config.example.yaml

# Build the Docker image locally.
docker-build:
    docker build -t prometheus-snapshot-manager:local .

# Start development compose stack.
docker-up:
    docker compose -f docker-compose.dev.yml up -d --build

# Stop development compose stack.
docker-down:
    docker compose -f docker-compose.dev.yml down

# Tail logs from development compose stack.
docker-logs:
    docker compose -f docker-compose.dev.yml logs -f

# Restart development compose stack.
docker-restart:
    docker compose -f docker-compose.dev.yml restart

# Start production-like compose stack.
compose-up:
    docker compose -f docker-compose.yml up -d --build

# Stop production-like compose stack.
compose-down:
    docker compose -f docker-compose.yml down

# Tail logs from production-like compose stack.
compose-logs:
    docker compose -f docker-compose.yml logs -f
