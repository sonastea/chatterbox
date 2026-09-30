# Chatterbox

A Go WebSocket chat server with pluggable persistence and pub/sub backends.

## Run without infrastructure

```sh
go run ./cmd/server
```

Requires Go 1.27 or newer. By default, the server listens at
`ws://localhost:8443/ws`, creates `chatterbox.db` in the working directory, and
uses an in-process memory broker. No database server, message broker, C compiler,
or TLS certificates are needed. Database schemas are embedded and initialized
automatically on startup.

If your shell already exports service configuration, explicitly select local mode:

```sh
DATABASE_DRIVER=sqlite DATABASE_URL=file:chatterbox.db BROKER_DRIVER=memory go run ./cmd/server
```

## Database configuration

| Variable | Default | Description |
| --- | --- | --- |
| `DATABASE_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `DATABASE_URL` | `file:chatterbox.db` | SQLite filename/file URI or PostgreSQL connection URL |

```sh
# Persistent SQLite at a custom location (parent directory must exist)
DATABASE_DRIVER=sqlite DATABASE_URL=file:./data/chat.db go run ./cmd/server

# Ephemeral SQLite, useful for development
DATABASE_DRIVER=sqlite DATABASE_URL=:memory: go run ./cmd/server

# PostgreSQL
DATABASE_DRIVER=postgres DATABASE_URL='postgres://user:password@localhost/chatterbox?sslmode=disable' go run ./cmd/server
```

For compatibility, a `postgres://` or `postgresql://` URL selects PostgreSQL when
`DATABASE_DRIVER` is unset. An explicit driver always takes precedence.
PostgreSQL connects to an **existing database** and initializes the `chatterbox`
schema; the configured user needs schema/table creation privileges, not database
creation privileges. Existing PostgreSQL table names and data are retained.
Switching drivers does **not** transfer data between databases.

SQLite uses WAL, foreign-key enforcement on every connection, a five-second busy
timeout, and one pooled connection to serialize local writes. Users and rooms
persist across restarts; disconnected room owners are retained so foreign keys
remain valid. This refactor does not add message history.

The chat hub depends on `store.RoomRepository` and `store.UserRepository`, not
SQL or a database driver. The included SQL repositories support SQLite and
PostgreSQL; other SQL dialects or non-SQL stores can implement the same interfaces.
Queries accept contexts and missing records return `store.ErrNotFound`.

## Broker configuration

`BROKER_DRIVER` defaults to `memory`. `BROKER_URL` configures the selected external
backend. Setting a URL alone does not enable an external broker.

| Driver | Default URL | Provider-specific URL fallback |
| --- | --- | --- |
| `memory` | None | None |
| `redis` | `redis://localhost:6379/0` | `REDIS_URL` |
| `valkey` | `redis://localhost:6379/0` | `VALKEY_URL`, then `REDIS_URL` |
| `nats` | `nats://localhost:4222` | `NATS_URL` |
| `rabbitmq` | `amqp://guest:guest@localhost:5672/` | `RABBITMQ_URL`, then `AMQP_URL` |

```sh
BROKER_DRIVER=redis BROKER_URL=redis://localhost:6379/0 go run ./cmd/server
BROKER_DRIVER=valkey BROKER_URL=valkey://localhost:6379/0 go run ./cmd/server
BROKER_DRIVER=nats BROKER_URL=nats://localhost:4222 go run ./cmd/server
BROKER_DRIVER=rabbitmq BROKER_URL=amqp://guest:guest@localhost:5672/ go run ./cmd/server
```

`BROKER_URL` takes precedence over provider-specific variables. Redis and Valkey
share a protocol adapter (`redis://`/`rediss://` and `valkey://`/`valkeys://` are
accepted). NATS and RabbitMQ use their native clients.

The `broker.Broker` interface exposes `Publish`, `Subscribe`, and `Close`.
Subscriptions support exact topics and `*` matching one dot-separated segment.
Room messages use `room.<xid>`. Cancellation and closure release subscriptions;
publication and connection errors are returned rather than terminating library code.

All backends use **live, best-effort fan-out**, with no durable replay or delivery
guarantee to disconnected clients. NATS uses ordinary subscriptions, not queue
groups. RabbitMQ uses the durable `chatterbox` topic exchange with a separate
exclusive, auto-delete queue per subscription and publisher confirmations.
RabbitMQ connection loss closes the affected subscriptions and stops the server;
restart the process to reconnect. Redis/Valkey and NATS clients reconnect, but do
not replay missed events. Join/leave notices remain local to each hub.

Memory is process-local: separate processes cannot communicate through it.
For multiple server instances, use an external broker **and shared persistence**
(normally PostgreSQL); separate local SQLite files will create different room IDs.
WebSocket send queues are bounded, and slow connections are disconnected instead
of blocking delivery to other clients.

## HTTP and TLS

`HTTP_ADDR` defaults to `:8443`. TLS is opt-in: set both `TLS_CERT` and `TLS_KEY`.
For the previous certificate paths:

```sh
TLS_CERT=./certs/chatterbox-cert.pem TLS_KEY=./certs/chatterbox-key.pem go run ./cmd/server
```

Use TLS directly or terminate it at a reverse proxy for production deployments.
SIGINT and SIGTERM shut down HTTP, WebSocket clients, broker subscriptions, and
database connections.

## Logging

All server logs are emitted to **stderr** as newline-delimited **OTLP JSON**.
Each line is a complete `ExportLogsServiceRequest` envelope with `resourceLogs`,
`scopeLogs`, and one `logRecords` entry, not a generic JSON message. HTTP server,
Redis/Valkey, and NATS diagnostics use the same format.

Records include nanosecond timestamps, numeric OTLP severity, severity text, a
string body, and typed attributes. Errors are stored in the `error` attribute;
client/room identifiers, listening address, transport, and TLS mode are separate
attributes where applicable. Chat message bodies and passwords are not logged.
`service.name` defaults to `chatterbox`; set `OTEL_SERVICE_NAME` to override it.

This configures the **log format only**, not an HTTP/gRPC exporter. Collect stderr
with an agent that understands OTLP JSON envelopes. For file-based ingestion,
redirect the built server's stderr to a `.jsonl` file and configure OpenTelemetry
Collector Contrib's OTLP JSON file receiver, for example:

```yaml
receivers:
  otlp_json_file:
    include: ["/var/log/chatterbox.jsonl"]
    start_at: beginning
exporters:
  debug:
    verbosity: detailed
service:
  pipelines:
    logs:
      receivers: [otlp_json_file]
      exporters: [debug]
```

Older Collector releases call this receiver `otlpjsonfile`. Replace the `debug`
exporter with your observability backend's exporter. Container runtimes may wrap
stderr lines; remove that wrapper before decoding the OTLP JSON payload.

## Docker

```sh
docker build -t chatterbox .
docker run --rm -p 8443:8443 -v chatterbox-data:/opt/chatterbox/data chatterbox
```

The image defaults to SQLite at `/opt/chatterbox/data/chatterbox.db` and memory
messaging. Mount the data directory to retain SQLite data when replacing a
container. Pass the same environment variables above to select external services.
The deployment workflow also accepts GitHub repository variables
`DATABASE_DRIVER` and `BROKER_DRIVER`, and the `BROKER_URL` secret (or legacy
provider-specific credentials).

## Tests

```sh
# No external services required
go test -race ./...

# Simulate concurrent WebSocket users against an isolated local test server
go run ./simulations

# Starts PostgreSQL, Redis, Valkey, NATS, and RabbitMQ using Docker Compose,
# runs race-enabled repository, broker-contract, and cross-hub WebSocket tests,
# then removes only the test stack and its volumes.
./scripts/test-integration.sh
```

If Compose is installed as a standalone binary, the script also accepts
`DOCKER_COMPOSE=/path/to/docker-compose`.

The Compose stack binds test ports on loopback: `15432`, `16379`, `16380`,
`14222`, and `15672`. Individual integrations can instead be enabled with
`TEST_POSTGRES_URL`, `TEST_REDIS_URL`, `TEST_VALKEY_URL`, `TEST_NATS_URL`, or
`TEST_RABBITMQ_URL`. Use a **dedicated test database/broker** for these variables.

### Simulated users

`go run ./simulations` connects 10 users to 2 rooms and sends 10 chat messages
per user concurrently. It checks every expected delivery, including sender echoes,
and fails on missing messages, duplicates, incorrect sender identities, or messages
leaking between rooms. The simulation also verifies:

- Per-sender message ordering, including under continuing traffic.
- Join/leave notification actions, room IDs, actors, server identity, and recipients,
  with no missing or duplicate notifications. Concurrent join order may vary.
- Forged sender/action fields are replaced and non-members cannot publish.
- Repeated joins/leaves are idempotent; leaving and switching rooms remove old
  publish and receive authorization.
- Leave, room switching, disconnect, and reconnect while another room member keeps
  sending. Barrier-ordered probes check isolation, and reconnects retain room IDs,
  get new connection identities, and do not replay traffic from absent membership.
- Unicode and embedded newlines round-trip unchanged. A 1,000-byte JSON frame is
  accepted; a 1,001-byte frame closes only its sender with WebSocket code 1009,
  while the remaining clients continue chatting. These sizes include the JSON
  envelope and the encoder's final newline, not just the message body.

By default it uses the real WebSocket handler with an ephemeral HTTP port,
in-memory SQLite, and the memory broker. It does **not** start external services or
touch `chatterbox.db`, and it cleans up its connections and server when finished.
The simulation also runs as part of `go test -race ./...`.

Local runs also include a deterministic saturated-memory-broker recovery scenario.
It fills a gated subscription queue, then verifies that the hub drains it while
publication is blocked, delivering the message before its deadline and preserving
subsequent chat order. This injected-failure scenario is not run against `SIM_URL`
deployments. The churn background sender uses
a fixed 10ms interval independently of `SIM_INTERVAL`.

Run the command from the repository root. It launches the same race-enabled Go
test with caching disabled, inherits the `SIM_*` variables below, and accepts Go
test flags such as `-timeout=2m`. `./scripts/test-users.sh` is an equivalent shortcut.

```sh
# More users and messages, with a pause between each user's sends
SIM_USERS=50 SIM_ROOMS=5 SIM_MESSAGES=20 SIM_INTERVAL=25ms go run ./simulations

# Test an already-running server and its configured database/broker
SIM_URL=ws://localhost:8443/ws go run ./simulations

# Burst traffic, with a larger per-phase timeout
SIM_USERS=50 SIM_ROOMS=5 SIM_MESSAGES=50 SIM_INTERVAL=0s SIM_TIMEOUT=30s go run ./simulations
```

| Variable | Default | Description |
| --- | --- | --- |
| `SIM_URL` | Unset | Start an isolated test server, or target a `ws://` / `wss://` endpoint |
| `SIM_USERS` | `10` | Concurrent clients; at least 2 |
| `SIM_ROOMS` | `2` | Rooms; from 1 to `SIM_USERS / 2`, ensuring at least two users per room |
| `SIM_MESSAGES` | `10` | Chat messages per user in the main traffic phase; at least 1 |
| `SIM_INTERVAL` | `20ms` | Pause between each user's sends; `0s` sends without a pause |
| `SIM_TIMEOUT` | `15s` | Timeout for each connection attempt and test phase, including send pauses |

The command prints a pass/fail summary and exits nonzero on failure, suitable for CI.
It uses unique room names for each run. **Use a dedicated test deployment with
`SIM_URL`: simulated users and rooms persist in that server's database.** TLS
certificate verification remains enabled for `wss://` endpoints.

This is a correctness smoke test under configurable load, not a capacity benchmark
or proof of production readiness. Larger rooms multiply fan-out: 10 users in
2 evenly sized rooms sending 10 messages produce 500 deliveries in the main phase.
Overloading the server can legitimately drop best-effort broker events or disconnect
slow clients; the simulation reports those as failures. Late unexpected messages
are also checked during 200ms observation windows while a user remains outside its
room, before closing connections for reconnect, and at the end. Use the full unit and
Docker integration suites above to cover configuration, persistence, shutdown, and
all external broker adapters.

The hub uses one ordered publication worker and four database workers, each work
queue bounded to 256 entries. Each connection waits for its outstanding command,
preserving command order and applying backpressure to senders while the hub keeps
draining broker events. A full work queue logs the overload and disconnects the
submitting client. The five-second work deadline includes queue time; publication
failures are logged and delivery remains best-effort. Shutdown cancels queued and
in-flight work and waits for the workers before returning.
