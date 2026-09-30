#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
project="chatterbox-tests-$$"

if [ -z "${DOCKER_COMPOSE:-}" ]; then
    if docker compose version >/dev/null 2>&1; then
        :
    elif command -v docker-compose >/dev/null 2>&1; then
        DOCKER_COMPOSE=$(command -v docker-compose)
    else
        printf '%s\n' 'Docker Compose is required. Install the plugin or set DOCKER_COMPOSE to a standalone binary.' >&2
        exit 1
    fi
fi
compose() {
    if [ -n "${DOCKER_COMPOSE:-}" ]; then
        "$DOCKER_COMPOSE" -f "$root/compose.test.yaml" -p "$project" "$@"
    else
        docker compose -f "$root/compose.test.yaml" -p "$project" "$@"
    fi
}
trap 'compose down --volumes' 0
trap 'exit 130' INT
trap 'exit 143' TERM

compose up -d --wait --wait-timeout 180
export TEST_POSTGRES_URL='postgres://chatterbox:chatterbox@localhost:15432/chatterbox_test?sslmode=disable'
export TEST_REDIS_URL='redis://localhost:16379/0'
export TEST_VALKEY_URL='valkey://localhost:16380/0'
export TEST_NATS_URL='nats://localhost:14222'
export TEST_RABBITMQ_URL='amqp://guest:guest@localhost:15672/'

cd "$root"
go test -race -count=1 "$@" ./...
