# Contributing

Use Go 1.24 or newer. Run `make check` and `make build` before opening a PR.
Tests use a local HTTP stub and need no Jev key. Set `TEST_AMQP_URL` to a
throwaway RabbitMQ instance to enable queue integration tests.

Keep changes focused and add regression tests for changed decision logic or
delivery guarantees. Never commit API keys or real user content. Discuss new
providers and breaking API changes in an issue before a large implementation.

Live evaluations (`go run ./cmd/edge-test`) call a paid external provider and
are separate from deterministic tests. Include anonymized evaluation results
when proposing policy or threshold changes.
