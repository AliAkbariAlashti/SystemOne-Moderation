# Integrating an application

Your app sends text and enforces the returned decision. Keep new content pending
until moderation completes. `review` means your app should route it to a human
or keep it hidden; this service does not provide a reviewer UI.

## Request

The same JSON is accepted over HTTP and RabbitMQ:

```json
{"id":"post-123","text":"Hello, community!","metadata":{"app":"forum","revision":3}}
```

| Field | Required | Meaning |
| --- | --- | --- |
| `text` | Yes | Nonblank UTF-8 text, at most 100,000 bytes |
| `id` | No | Application item identifier; strongly recommended for queues |
| `metadata` | No | JSON context echoed in the result; not evaluated |
| `type` | No | Restrict to `noul`, `choice`, or `score`; omission runs all classes |

Bodies must contain one JSON object, no unknown fields, and at most 1 MiB.

## Response

Example for a Choice-only request:

```json
{
  "id":"post-123",
  "action":"review",
  "policy_version":2,
  "metadata":{"app":"forum","revision":3},
  "model":"provider-returned-model",
  "findings":[{
    "class_id":"content_class",
    "type":"choice",
    "action":"review",
    "reason":"low_confidence",
    "choice":"safe",
    "confidence":0.4
  }]
}
```

Use `action` to publish, hold, or reject content. Findings explain which classes
contributed; `reason` is a machine-readable policy outcome, not generated prose.
Store the policy version and model with the decision. Preserve a content revision
in metadata and ignore stale decisions for content that has since been edited.

## HTTP

```sh
curl --fail-with-body http://localhost:8080/v1/moderate \
  -H "Authorization: Bearer $MODERATION_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":"post-123","text":"Hello, community!"}'
```

Omit Authorization if the server has no token configured. Errors use
`{"error":"description"}`. Status codes:

- `400`: invalid JSON, text, type, or unavailable class family.
- `401`: missing or incorrect configured bearer token.
- `413`: request body exceeds 1 MiB.
- `502`: provider failure or invalid provider response.
- `503`: policy unavailable or invalid.

HTTP requests do not retry automatically. Apply bounded backoff for temporary
errors in your caller and keep the item pending on failure. A network failure
can occur after a provider call; retrying can incur another call.

## RabbitMQ

Create a durable result queue for each application. Publish persistent requests
to `moderation.requests` with `reply_to` set to that queue and a unique
`correlation_id` per content revision. The service preserves the correlation ID.
If reply_to is omitted, it uses `moderation.results`; independent applications
must not compete for replies on that shared fallback queue.

Python example (`pip install pika`):

```python
import json
import os
import uuid
import pika

connection = pika.BlockingConnection(pika.URLParameters(os.environ["AMQP_URL"]))
channel = connection.channel()
channel.queue_declare(queue="moderation.requests", durable=True)
channel.queue_declare(queue="forum.moderation.results", durable=True)
channel.confirm_delivery()
job_id = str(uuid.uuid4())
channel.basic_publish(
    exchange="", routing_key="moderation.requests", mandatory=True,
    body=json.dumps({"id":"post-123", "text":"Hello, community!",
                     "metadata":{"revision":3}}).encode(),
    properties=pika.BasicProperties(
        content_type="application/json", delivery_mode=2,
        correlation_id=job_id, reply_to="forum.moderation.results"),
)

def receive(ch, method, properties, body):
    result = json.loads(body)
    # In one database transaction:
    # 1. Deduplicate properties.correlation_id.
    # 2. Verify result.metadata.revision is still current.
    # 3. Store the decision and update content visibility.
    print(properties.correlation_id, result)
    ch.basic_ack(delivery_tag=method.delivery_tag)

channel.basic_qos(prefetch_count=1)
channel.basic_consume(queue="forum.moderation.results", on_message_callback=receive)
channel.start_consuming()
```

Replace the print with your durable application update before acknowledging.
The service's API token does not authenticate AMQP; use RabbitMQ credentials,
vhosts, permissions, and TLS for remote broker connections.

## Failures and recovery

Workers try temporary provider failures up to four times per delivery, with
1/2/4-second waits. Provider 408, 429, and 5xx responses are retryable. Other
provider HTTP errors and invalid requests are not. Malformed provider responses
and policy loading errors receive the same bounded retries.

Failed jobs are published persistently to `<input queue>.dead` with the original
body, correlation ID, message ID and reply_to. The `moderation_failure` header
is `processing_failed` or `reply_unroutable`. Fix the underlying problem, then
republish selected jobs with publisher confirms and acknowledge their dead-queue
copies only after confirmed republishing. Replaying can call the provider again.

A missing result queue does not silently discard a job. A failed or uncertain
broker publish closes the worker session, leaving the original delivery for
redelivery. Confirmed publishing and consumer acknowledgements are separate;
duplicates remain possible. See RabbitMQ's
[confirmation documentation](https://www.rabbitmq.com/docs/confirms).

No normal result is emitted for a dead-lettered job. Monitor dead-queue depth,
worker connection logs, provider failures, and age of pending records. Retry
limits apply within a session and restart after crashes. The service is not an
exactly-once system and has no built-in decision database.

## Upgrading from the initial version

Existing requests with `type` retain their class selection. Missing type now runs
all classes. Metadata, policy version, and finding reasons are additive response
fields. Low-confidence Choice results now consistently return review. Invalid
policies previously accepted may now fail startup. Input queues keep their
original declaration arguments; workers create an additional `.dead` queue.
Compose ports now bind to localhost and RabbitMQ has a persistent named volume.
Back up existing broker data before replacing an old container without a volume.
