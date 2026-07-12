# Inoculum

**LAN-Based Task-Parallel Distributed Compute Daemon**

Inoculum is a lightweight, zero-dependency Go service that distributes independent tasks across multiple machines on a local network. It uses **task/data parallelism** — each machine executes its own task completely independently, avoiding the bandwidth bottleneck of pipeline parallelism.

## Architecture

```
                    ┌──────────────────┐
                    │   Coordinator    │
 User ──POST───▶   │                  │   ◀──heartbeat──  Workers
  /submit-job       │  • Registry      │
                    │  • Scheduler     │
                    │  • Task Dispatch │
                    └──────┬───────────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Worker 1 │ │ Worker 2 │ │ Worker N │
        │ POST     │ │ POST     │ │ POST     │
        │ /execute │ │ /execute │ │ /execute │
        └──────────┘ └──────────┘ └──────────┘
```

## Quick Start

### Prerequisites

- Go 1.21 or later

### Build

```bash
# Build all binaries
go build -o bin/coordinator ./cmd/coordinator
go build -o bin/worker     ./cmd/worker
go build -o bin/benchmark  ./cmd/benchmark
```

### Run on a Single Machine (Development/Testing)

**Terminal 1 — Start the coordinator:**
```bash
./bin/coordinator -port 8080
```

**Terminal 2 — Start a worker:**
```bash
./bin/worker -port 9000 -coordinator localhost:8080
```

**Terminal 3 — (Optional) Start a second worker:**
```bash
./bin/worker -port 9001 -coordinator localhost:8080
```

> **Note on Concurrency:** The `-concurrency` flag on the worker limits how many tasks a single worker can process at once (default is `1`). This is important for getting realistic multi-worker speedup numbers during benchmarking, as it forces the coordinator to distribute work across actual workers rather than a single worker multiplexing everything concurrently via goroutines.

**Terminal 4 — Submit a test job:**
```bash
curl -X POST localhost:8080/submit-job \
  -H "Content-Type: application/json" \
  -d '{"task_type": "dummy", "inputs": ["hello"]}'
```

**Check system status:**
```bash
curl -s http://localhost:8080/status | jq .
```

### Run on Two Machines on the Same LAN

**Machine A (Coordinator + Worker):**
```bash
# Start the coordinator (with auto-discovery enabled by default)
./bin/coordinator -port 8080

# Optionally also run a worker on this machine
./bin/worker -port 9000 -coordinator localhost:8080
```

**Machine B (Worker):**
```bash
# Option 1: Auto-discovery (coordinator is found automatically via UDP broadcast)
./bin/worker -port 9000

# Option 2: Manual address
./bin/worker -port 9000 -coordinator 192.168.1.10:8080
```

The worker on Machine B will automatically discover the coordinator via UDP broadcast on port 9999. If your network blocks UDP broadcasts, use the `-coordinator` flag with the coordinator's IP address.

## API Endpoints

### Coordinator

| Method | Endpoint       | Description                          |
|--------|----------------|--------------------------------------|
| POST   | `/register`    | Worker registration                  |
| POST   | `/heartbeat`   | Worker alive signal                  |
| POST   | `/submit-job`  | Submit a new job with task list      |
| GET    | `/status`      | System status (workers, tasks, jobs) |

### Worker

| Method | Endpoint    | Description              |
|--------|-------------|--------------------------|
| POST   | `/execute`  | Execute a task           |

## Task Types

| Type           | Description                                      | Input                    |
|----------------|--------------------------------------------------|--------------------------|
| `dummy`        | Sleeps 10ms and returns a message (Phase 1 test)  | Any string               |
| `file_analyze` | Counts lines/words/bytes in a file (pure Go)      | File path                |
| `http_fetch`   | Fetches a URL and returns status + content length | URL                      |

### Example: File Analysis Tasks

```bash
curl -s -X POST http://localhost:8080/submit-job \
  -H "Content-Type: application/json" \
  -d '{
    "task_type": "file_analyze",
    "inputs": [
      "/etc/hostname",
      "/etc/os-release"
    ]
  }' | jq .
```

## Benchmarking (Phase 4)

Compare distributed vs sequential execution:

```bash
# With dummy tasks (10 tasks)
./bin/benchmark -coordinator localhost:8080 -tasks 10 -type dummy

# With file analysis tasks
./bin/benchmark -coordinator localhost:8080 -tasks 8 -type file_analyze -input /etc/os-release
```

The benchmark reports:
- **Round-trip latency**: Network communication time per task
- **Task completion time**: Processing time on each worker
- **Total speedup**: Sequential ÷ distributed time
- **Coordinator overhead**: Extra time for dispatch and collection

## Scheduling Strategies

| Strategy      | Flag                    | Description                          |
|---------------|-------------------------|--------------------------------------|
| Round-robin   | `-strategy round-robin` | Cycles through workers in order      |
| Least-busy    | `-strategy least-busy`  | Assigns to the worker with fewest active tasks |

## Fault Tolerance (Phase 5)

- If a worker fails to respond to a task, the coordinator retries up to 2 times on other available workers.
- Workers that stop sending heartbeats are removed from the active pool after 30 seconds.
- Workers that get rejected on heartbeat automatically re-register.

## Node Discovery (Phase 2)

Workers can auto-discover the coordinator without knowing its IP:

1. The coordinator listens on UDP port 9999 for `INOCULUM_DISCOVER` broadcast messages.
2. When a worker starts without `-coordinator`, it sends a UDP broadcast.
3. The coordinator responds with its HTTP address.
4. The worker registers and begins accepting tasks.

Disable discovery on the coordinator with `-discovery=false`.

## Project Structure

```
├── cmd/
│   ├── coordinator/main.go   # Coordinator entry point
│   ├── worker/main.go        # Worker entry point
│   └── benchmark/main.go     # Benchmark tool (Phase 4)
├── internal/
│   ├── types/types.go        # Shared data structures
│   ├── coordinator/
│   │   ├── server.go         # HTTP server & endpoint handlers
│   │   ├── scheduler.go      # Round-robin & least-busy scheduling
│   │   └── registry.go       # Worker registry & heartbeat tracking
│   ├── worker/
│   │   ├── server.go         # Worker HTTP server (/execute)
│   │   ├── executor.go       # Task executors (dummy, file, HTTP)
│   │   └── registration.go   # Register & heartbeat with coordinator
│   └── discovery/
│       └── udp.go            # UDP broadcast auto-discovery
├── go.mod
├── micro-compute-daemon-spec-en.md
└── README.md
```

## Out of Scope

Per the [spec](micro-compute-daemon-spec-en.md), the following are deliberately excluded:

- Blockchain / cryptocurrency / token economy
- Zero-Knowledge Proofs / verifiable computation
- Pipeline parallelism (splitting model layers across network)
- Public internet / untrusted machine networks
