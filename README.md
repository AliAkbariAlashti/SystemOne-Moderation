# Jev Moderation

A standalone text moderation service for applications that want to keep moderation
out of their codebase. Send text over HTTP or RabbitMQ; receive an `allow`,
`review`, or `block` decision with per-class reasons and the policy version.

Built in Go, backed by Jev, and licensed under MIT. Jev is an external service:
you need a Typesafe API key, and submitted text is sent to that provider.
This project does not include a human review UI or store moderation decisions.

## Quick start

```sh
cp .env.example .env
# Set TYPESAFE_API_KEY in .env.
docker compose up --build -d
curl http://localhost:8080/v1/moderate \
  -H 'Content-Type: application/json' \
  -d '{"id":"post-123","text":"Hello, community!"}'
```

No question type is required. By default, all policy classes are evaluated in one
provider call and the strongest configured action wins. Advanced callers may
set `type` to `noul`, `choice`, or `score` to evaluate only that family.

Compose binds HTTP and RabbitMQ to localhost and persists broker data in a named
volume. Set `MODERATION_API_TOKEN` for shared deployments and send
`Authorization: Bearer <token>` on `/v1/*`. Put remote access behind TLS and
configure RabbitMQ users and permissions for your applications.

For HTTP only, RabbitMQ is optional:

```sh
export TYPESAFE_API_KEY=your-key
AMQP_URL= go run ./cmd/moderation-service
```

## Integrate from any language

HTTP is useful for immediate decisions. RabbitMQ lets applications submit jobs
and consume decisions independently. Neither requires a language-specific SDK.
See [INTEGRATION.md](INTEGRATION.md) for payloads, a Python queue example,
error handling, and recovery.

| Endpoint | Purpose |
| --- | --- |
| `POST /v1/moderate` | Moderate one item; optional bearer authentication |
| `GET /v1/policy` | Read the active policy; same authentication |
| `GET /healthz` | Process liveness |
| `GET /readyz` | Verify the current policy loads and validates |

Readiness does not probe Jev or RabbitMQ. An upstream outage returns an error,
never a fabricated `allow` decision. HTTP bodies are limited to 1 MiB and text
to 100,000 bytes. Metadata is returned unchanged but is not sent to Jev.

## Policy

Edit [`config/policy.json`](config/policy.json). It is read and validated for each
item. Replace it atomically and increment `version` when changing behavior.
Invalid configuration makes requests fail until corrected.

| Class type | Meaning | Controls |
| --- | --- | --- |
| `noul` | Independent probability | `review_threshold`, `action_threshold`, `action` |
| `choice` | Mutually exclusive category | Named `criteria`, `choice_actions`, optional `min_confidence` |
| `score` | Ordered severity | Ordered `criteria`, `review_score`, `action_score`, `action` |

Precedence must list `block`, `review`, and `allow` exactly once, highest first.
Low-confidence Choice answers go to review before category actions are applied.
Missing or invalid provider answers fail the request rather than defaulting to zero.
The bundled policy is an example: evaluate it on your community's languages and
content before relying on automatic decisions. Running all classes may change
latency, cost, and decisions relative to the earlier type-specific API.

## Queue reliability and scaling

Workers use prefetch 1, mandatory result routing, publisher confirms, and manual
acknowledgements. Temporary provider failures get up to four attempts with
1/2/4-second delays. Invalid or exhausted jobs go to `moderation.requests.dead`;
unroutable replies also preserve the original job there. Queue names follow
`AMQP_INPUT_QUEUE`, including its `.dead` suffix.

The original job is acknowledged only after its result or dead-letter copy is
confirmed. Delivery is **at least once**: deduplicate decisions in your app.
Retries reset after a worker crash, and a redelivery can call Jev again.
Monitor the dead queue and pending application records; failed jobs do not
produce normal decision replies.

```sh
docker compose up -d --scale moderation-worker=5
```

`WORKER_CONCURRENCY` controls consumers per worker container. Each consumer holds
its current job during retry backoff; capacity is deliberately bounded. Tune
concurrency against provider limits. Broker persistence is not a substitute for
replication and backups.

## Development

Go 1.24+ and RabbitMQ (only for integration tests):

```sh
make check
make build
TEST_AMQP_URL=amqp://guest:guest@localhost:5672/ go test -race ./...
```

Unit tests use a fake provider and need no API key. CI also runs real broker
integration tests. [CONTRIBUTING.md](CONTRIBUTING.md) describes contributions.

The 50 labeled cases in `testdata/edge_cases.json` are live evaluations:

```sh
go run ./cmd/edge-test
```

They incur provider usage and measure agreement with labels, not a production
accuracy guarantee. Historical [load-test results](docs/LOAD_TEST_RESULTS.md)
are retained for context; rerun after changing policy or concurrency.

## Configuration

See [.env.example](.env.example). The service reads environment variables;
Docker Compose loads `.env`, while `go run` expects exported variables.
No API key or submitted text is intentionally logged. Application owners are
responsible for access control, retention of metadata/results, human review,
and enforcing decisions in their own product.
