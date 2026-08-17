# Inoculum V1 Technical Specification

This document describes the current V1 runtime. User setup and examples live in
[README.md](README.md).

## Product boundary

Inoculum turns 2–10 user-owned computers on a trusted LAN into a small execution
pool for independent, finite tasks.

V1 is deliberately not a workflow engine, cluster orchestrator, remote shell,
distributed filesystem, container runtime, or public compute service.

## Runtime architecture

```text
submit client ---> coordinator <--- outbound pull workers
                       |
                       v
                 FIFO task queue
                       |
                       v
                 lease state model
```

The coordinator is the only Inoculum process that listens on the network. It
owns all in-memory job, task, queue, and lease state. Workers make outbound HTTPS
requests to claim work, renew leases, and submit results.

There is one coordinator address, one coordinator port, one FIFO queue, and one
global lease/retry policy. Worker concurrency is local to each worker; the
coordinator does not maintain load counters or select scheduler strategies.

## Task lifecycle

A task has a coordinator-generated stable ID and one of four states:

- `queued`
- `leased`
- `completed`
- `failed`

Claiming a queued task creates a unique lease containing the task ID, worker ID,
issue time, expiry time, and attempt number. A worker renews the lease while its
executor is running.

If a lease expires, the task is returned to the FIFO queue unless its configured
attempt limit is exhausted. Late or stale results are rejected. A completed task
is never duplicated in coordinator result state.

Delivery is at least once. A task can execute more than once when a worker
finishes but cannot deliver its result before lease expiry.

Coordinator defaults:

- lease duration: 6 seconds
- maximum attempts: 3

Both values are global V1 coordinator settings exposed as `--lease-duration`
and `--max-attempts`.

## Submission models

### Manifest mode

The primary real-workload input is a versioned JSON manifest:

```json
{
  "version": 1,
  "type": "http_probe",
  "tasks": [
    {"key": "homepage", "input": "https://example.com/"}
  ]
}
```

Manifest V1 accepts:

- exactly version 1;
- exactly one type for the manifest;
- 1–1,000 tasks;
- unique, nonempty user task keys up to 128 bytes;
- nonempty string inputs up to 4,096 bytes;
- a maximum document/request size of 5 MiB;
- no unknown fields.

The coordinator generates internal task IDs. User keys are correlation labels
kept in coordinator state; workers do not receive them. Keys survive retries and
worker reassignment. Final exported tasks preserve manifest order.

Manifest V1 supports only `http_probe`.

### Simple mode

Simple mode repeats one type/input pair a requested number of times. It exists
for diagnostics and compatibility with built-in worker-local executors.

## Built-in execution

### `http_probe`

Performs one bounded HTTP `HEAD` request.

- HTTP and HTTPS only;
- embedded URL credentials rejected;
- normal target TLS verification;
- 10-second request timeout;
- at most five redirects;
- no response body read;
- no Inoculum Authorization header available to or forwarded by the executor;
- LAN and private targets allowed.

The structured result can include HTTP status, final URL, elapsed milliseconds,
declared content length, and final TLS leaf-certificate expiry. Transport
failures return sanitized categories and messages.

HTTP response statuses, including 4xx and 5xx, are successful probe results.
DNS, connection, TLS, redirect, and timeout errors are executor failures and use
the normal task retry policy.

### `diagnostic_sleep`

A duplicate-safe diagnostic executor accepting a positive duration up to five
minutes.

### `file_analyze`

Counts lines, words, and bytes in a worker-local file. The resolved path must be
within the worker's configured allowed paths. No file transfer occurs, so the
file must exist on the worker that claims the task.

## Result model

Job status is derived from coordinator task state. A final manifest export
contains:

- job ID and terminal state;
- one ordered result per manifest task;
- user key;
- task state;
- attempt count;
- final worker ID when available;
- typed executor output;
- structured failure details when available.

A known partial failure still produces an output file before the submit process
returns failure. Result exports contain no authentication secrets.

## Network protocol

All routes are served by the coordinator over one HTTPS listener and require the
shared bearer token:

| Method | Route | Purpose |
|---|---|---|
| `POST` | `/worker/claim` | Claim the next FIFO task when worker capacity is free |
| `POST` | `/worker/renew` | Extend an active lease |
| `POST` | `/worker/result` | Complete, retry, or permanently fail leased work |
| `POST` | `/pull/submit` | Create an in-memory job and queued tasks |
| `GET` | `/pull/job` | Read current job/task state |
| `GET` | `/status` | Read pull-oriented coordinator status |

Worker identity is an operational label, not a separate authentication
principal. There are no worker listeners or worker certificates.

## Security model

- Coordinator HTTPS is mandatory.
- The coordinator has one stable, persisted self-signed identity.
- First client trust requires an explicit fingerprint.
- Successful trust is saved per OS user and shared by worker and submit clients.
- Unknown identities are never silently trusted.
- API authentication uses `Authorization: Bearer` with one shared token.
- A wrong coordinator identity is rejected before the token is sent.
- There is no plaintext mode, mutual TLS, private CA, nonce cache, or timestamp
  authentication dependency.
- Sanitized audit logging is optional.

This model targets user-controlled machines on a trusted LAN. Direct public
internet exposure is outside V1's supported threat model.

The shared token authorizes network probes to endpoints reachable from workers,
including private endpoints.

## Presentation

Runtime/domain state is independent from terminal rendering. Interactive
terminals receive a live coordinator, worker, or submit view. Non-interactive
terminals and explicit plain mode receive line-oriented output without terminal
control sequences.

Presentation does not alter task, lease, retry, or protocol behavior.

## State and persistence

Coordinator state is memory-only. A coordinator restart loses queued tasks,
leases, and job results. Manifest files remain the source for resubmission.

Persistence is not part of V1.

## Platform status

- macOS: physically validated.
- Linux amd64: physically validated against a macOS coordinator.
- Windows amd64: cross-compiled and configuration-path logic unit-tested;
  runtime unvalidated.

## Explicit non-goals

- arbitrary commands, shells, or executable plugins;
- DAGs and task dependencies;
- persistent services;
- containers;
- worker-to-worker data flow;
- file upload or distribution;
- distributed storage;
- resource-aware scheduling and autoscaling;
- coordinator persistence;
- public-internet deployment.
