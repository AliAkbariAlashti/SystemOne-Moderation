# Live load-test results

Run time: 2026-09-21 13:35 UTC  
Path: RabbitMQ → Go moderation worker → Jev → RabbitMQ reply queue

## Measured result

| Metric | Value |
| --- | ---: |
| Messages published | 100 |
| Results received | 100 (100%) |
| Test duration | 37.11 seconds |
| End-to-end throughput | 2.69 messages/second |
| p50 end-to-end latency | 18.80 seconds |
| p95 end-to-end latency | 35.22 seconds |
| p99 end-to-end latency | 36.76 seconds |
| Minimum / maximum latency | 1.17 / 37.11 seconds |

The mixed workload contained 34 Noul requests, 34 Choice requests, and 32 Score requests. It returned 50 `allow`, 48 `block`, and 2 `review` results.

![Latency chart](../reports/load-test-latency-20260921T133519Z.svg)

![Action distribution chart](../reports/load-test-actions-20260921T133519Z.svg)

## What this measures

This is a real 100-message burst sent as persistent RabbitMQ messages through one running worker. Every item called live Jev evaluation and was received back on a temporary reply queue.

The latency values are complete round-trip values: broker publish, queue waiting, Jev inference, result publish, and result consumption. Since the current worker consumes one message at a time, the p50/p95/p99 values intentionally include queue wait under a burst. Throughput and 100% delivery are the useful baseline metrics here; low-latency parallel capacity needs multiple worker replicas or configurable worker concurrency.

The machine-readable source is [load-test-20260921T133519Z.json](../reports/load-test-20260921T133519Z.json).

## Scaling comparison: one worker to five workers

The same 100-message mixed workload was repeated with five competing `moderation-worker` replicas.

| Metric | 1 worker | 5 workers | Change |
| --- | ---: | ---: | ---: |
| Results received | 100 / 100 | 100 / 100 | 100% delivery in both runs |
| Duration | 37.11s | 13.02s | 64.9% lower |
| Throughput | 2.69 msg/s | 7.68 msg/s | 185.0% higher |
| p50 latency | 18.80s | 9.53s | 49.3% lower |
| p95 latency | 35.22s | 12.69s | 64.0% lower |
| p99 latency | 36.76s | 12.97s | 64.7% lower |

![Throughput scaling](../reports/throughput-comparison.svg)

PNG export: [throughput-comparison.png](../reports/throughput-comparison.png)

![p95 latency scaling](../reports/p95-latency-comparison.svg)

PNG export: [p95-latency-comparison.png](../reports/p95-latency-comparison.png)

The five-worker result is not a five-times gain because Jev/API processing and external-service concurrency become part of the bottleneck. It is nevertheless a measured, end-to-end 2.85x throughput improvement with complete result delivery.

The machine-readable five-worker source is [load-test-20260921T134348Z.json](../reports/load-test-20260921T134348Z.json).
