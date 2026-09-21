# Jev moderation service

A small, application-neutral text moderation service built in Go. It calls Jev once per item with every configured class, then applies your thresholds in code.

## Start it

```sh
cp .env.example .env
# Set TYPESAFE_API_KEY in .env
docker compose up --build
```

The HTTP API listens at `http://localhost:8080`; RabbitMQ's management UI is at `http://localhost:15672` (`guest` / `guest`).

## Scale RabbitMQ moderation workers

`moderation-worker` is a dedicated AMQP-only service with no exposed HTTP port. RabbitMQ distributes messages among its replicas. Start five workers locally:

```sh
docker compose up -d --scale moderation-worker=5
```

Set `WORKER_CONCURRENCY` above `1` to add independent AMQP consumers inside each replica. Start conservatively and increase only while Jev latency/rate limits remain healthy. Each consumer uses RabbitMQ prefetch `1`, so it does not reserve more work than it can process.

## RabbitMQ: primary application integration

Publish a persistent JSON request to the durable `moderation.requests` queue. This is the recommended path for apps that do not need the decision inline with a browser request.

```json
{
  "id": "post-123",
  "text": "Example text to assess",
  "metadata": { "app": "social-web", "user_id": "user-42" }
}
```

Set AMQP properties on the published message:

- `correlation_id`: your application's request/job ID.
- `reply_to`: a durable, app-owned queue such as `social-web.moderation.results`.
- `delivery_mode`: persistent.

The service sends the result to `reply_to` and preserves `correlation_id`. Thus every integrating app receives only its own moderation results. If `reply_to` is omitted, results go to the fallback `moderation.results` queue.

The worker acknowledges a request only after it publishes a result. A temporary Jev or broker failure causes the message to be requeued for retry.

## HTTP: health and optional synchronous use

```sh
curl -sS http://localhost:8080/v1/moderate \
  -H 'Content-Type: application/json' \
  -d '{"id":"post-123","text":"Example text to assess","type":"noul"}'
```

`GET /v1/policy` returns the active configuration and `GET /healthz` is the health check.

## Configure classes

Edit `config/policy.json`; it is reloaded for each item, so no restart is required. Every request must provide a `type` of `noul`, `choice`, or `score`. The service runs only classes with that type. Every class has an `id`, a plain-language `definition`, an action, and one of:

| Type | Use | Required controls |
| --- | --- | --- |
| `noul` | Independent category: a text may match many classes. | `review_threshold`, `action_threshold` |
| `choice` | One mutually-exclusive category. | `criteria` map; optional `choice_actions` and `min_confidence` |
| `score` | Ordered severity. | ordered `criteria`, `review_score`, `action_score` |

Actions are `allow`, `review`, and `block`; `actions_precedence` decides which result wins when classes disagree. Add `choice_actions`, for example `{"spam":"review","abuse":"block"}`, to make a Choice result affect moderation.

## Edge-case evaluation

`testdata/edge_cases.json` has 50 labeled boundary cases: 17 Noul, 17 Choice, and 16 Score. Run them against Jev after setting `TYPESAFE_API_KEY`:

```sh
docker run --rm --env-file .env -v "$PWD:/src" -w /src \
  golang:1.24-alpine go run ./cmd/edge-test
```

Each JSON output row gives the expected label, Jev's returned label/rounded score, the final action, and whether they match. These are evaluation cases, not a deterministic unit test: review misses, then tune the definitions and thresholds in `config/policy.json`.

## Security

Keep `TYPESAFE_API_KEY` in `.env` or your deployment secret manager only. Do not put it in policy, source code, Docker images, or messages. The key previously shared in chat should be rotated before production use.
