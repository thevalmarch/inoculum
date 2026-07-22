# Inoculum

**LAN-based distributed compute daemon — zero dependencies, pure Go.**

Inoculum combines the processing power of multiple machines on a local network by distributing independent tasks across them in parallel. Each machine runs its own task completely — no shared state, no inter-node data flow, no bandwidth bottleneck.

> **Measured result:** 1.6x real-world speedup across 2 workers on a home LAN, with TLS encryption and authentication enabled.

## Why task parallelism?

Splitting a model's layers across machines (pipeline parallelism) requires continuous data transfer between nodes. On a typical LAN (~125 MB/s), this creates a **1000x bottleneck** compared to local memory bandwidth — making it slower than a single machine.

Inoculum takes the opposite approach: send a small task description (a few KB), let each machine work independently, collect the result. The network only carries instructions and answers, not intermediate data.

## Quick Start

```bash
# Build
go build -o bin/coordinator ./cmd/coordinator
go build -o bin/worker     ./cmd/worker
go build -o bin/benchmark  ./cmd/benchmark
```

**1. Start the coordinator** (note the fingerprint it prints):
```bash
./bin/coordinator -port 8080 -token mysecret
```

**2. Start one or more workers** (use the fingerprint from step 1):
```bash
./bin/worker -port 9000 -coordinator localhost:8080 \
  -token mysecret -coordinator-fingerprint <FINGERPRINT>

./bin/worker -port 9001 -coordinator localhost:8080 \
  -token mysecret -coordinator-fingerprint <FINGERPRINT>
```

**3. Run the benchmark:**
```bash
./bin/benchmark -coordinator localhost:8080 -tasks 10 -type dummy \
  -token mysecret -coordinator-fingerprint <FINGERPRINT>
```

Workers on other machines will auto-discover the coordinator via UDP broadcast — no IP configuration needed. If your network blocks broadcasts, use `-coordinator <IP>:8080` explicitly.

## How It Works

```
                    ┌──────────────────┐
                    │   Coordinator    │
 User ──POST───▶   │  • Registry      │   ◀──heartbeat──  Workers
  /submit-job       │  • Scheduler     │
                    │  • Rate Limiter  │
                    └──────┬───────────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Worker 1 │ │ Worker 2 │ │ Worker N │
        │ /execute │ │ /execute │ │ /execute │
        └──────────┘ └──────────┘ └──────────┘
```

The coordinator accepts jobs, splits them into tasks, and distributes them across available workers using round-robin or least-busy scheduling. If a worker crashes mid-task, the coordinator automatically retries on another worker (up to 2 retries). Workers that stop sending heartbeats are removed from the pool after 30 seconds.

## Security

All traffic is encrypted and authenticated — designed to defend against real LAN threats (ARP spoofing, rogue nodes, traffic interception):

| Layer | How it works |
|-------|-------------|
| **Token Auth** | Shared secret (`-token`) verified on every request |
| **TLS Encryption** | Auto-generated certificates, persisted across restarts |
| **TOFU Pinning** | Certificate fingerprints pinned on first contact (SSH-style `known_hosts`) |
| **Replay Protection** | Unique nonce + timestamp on every request; 30s window |
| **Path Traversal Guard** | `file_analyze` restricted to `-allowed-paths` directories |
| **Rate Limiting** | Token bucket per IP on `/submit-job` (60 burst, 1/sec steady) |
| **Audit Log** | All events written as structured JSON to `inoculum-audit.log` |

## Task Types

| Type | Description | Input |
|------|-------------|-------|
| `dummy` | Sleeps 10ms, returns a message (for benchmarking) | Any string |
| `file_analyze` | Counts lines, words, and bytes in a file | File path |
| `http_fetch` | Fetches a URL, returns status + content length | URL |

## API

**Coordinator** exposes:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/register` | Worker registration |
| `POST` | `/heartbeat` | Worker alive signal |
| `POST` | `/submit-job` | Submit a job |
| `GET` | `/status` | System status |

**Workers** expose:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/execute` | Execute a task |

All endpoints require `X-Inoculum-Token`, `X-Inoculum-Nonce`, and `X-Inoculum-Timestamp` headers.

## Configuration Flags

**Coordinator:**
| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8080` | HTTPS listen port |
| `-token` | *required* | Shared authentication secret |
| `-strategy` | `round-robin` | Scheduling: `round-robin` or `least-busy` |
| `-discovery` | `true` | Enable UDP broadcast discovery |
| `-audit-log` | `inoculum-audit.log` | Audit log file path |

**Worker:**
| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `9000` | HTTPS listen port |
| `-coordinator` | *auto-discover* | Coordinator address (`host:port`) |
| `-token` | *required* | Shared authentication secret |
| `-coordinator-fingerprint` | — | Pinned coordinator certificate fingerprint |
| `-concurrency` | `1` | Max concurrent tasks |
| `-allowed-paths` | `.` | Directories allowed for `file_analyze` |
| `-audit-log` | `inoculum-audit.log` | Audit log file path |

## Project Structure

```
├── cmd/
│   ├── coordinator/main.go     # Coordinator entry point
│   ├── worker/main.go          # Worker entry point
│   └── benchmark/main.go       # Benchmark tool
├── internal/
│   ├── types/types.go          # Shared data structures
│   ├── audit/logger.go         # Structured JSON audit logger
│   ├── auth/
│   │   ├── auth.go             # Token middleware + nonce validation
│   │   └── replay.go           # NonceCache for replay protection
│   ├── coordinator/
│   │   ├── server.go           # HTTPS server & handlers
│   │   ├── scheduler.go        # Scheduling strategies
│   │   ├── registry.go         # Worker registry & heartbeats
│   │   └── ratelimit.go        # Token bucket rate limiter
│   ├── crypto/tls.go           # TLS cert generation & TOFU pinning
│   ├── worker/
│   │   ├── server.go           # Worker HTTPS server
│   │   ├── executor.go         # Task executors
│   │   ├── executor_test.go    # Path traversal tests
│   │   └── registration.go     # Coordinator registration
│   └── discovery/udp.go        # UDP broadcast auto-discovery
├── SPEC.md                     # Full technical specification
├── LICENSE                     # MIT
└── README.md
```

## License

MIT — see [LICENSE](LICENSE).
