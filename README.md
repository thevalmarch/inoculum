# Inoculum

Turn the computers you already own into a tiny compute pool.

Inoculum is a small LAN execution pool for independent, finite jobs. Run one
coordinator, connect outbound workers from your other machines, submit a batch,
and let leases and retries handle worker failure.

It is designed for 2–10 user-owned machines on a trusted LAN. Inoculum is not a
cluster orchestrator, workflow engine, distributed filesystem, or public compute
service.

## Why Inoculum?

Sometimes a workload is already a collection of independent pieces: probe many
URLs, inspect separate inputs, or run duplicate-safe diagnostics. Those pieces do
not need a DAG, containers, a message broker, or a permanent cluster.

Inoculum provides a deliberately small middle ground:

- one coordinator address and port;
- one FIFO queue with lease-based task ownership;
- workers that pull only when they have local capacity;
- automatic reassignment when a worker disappears;
- terminal progress and machine-readable results.

## Quick demo

The commands below assume the binary is available as `./inoculum`. A development
build is written to `./build/inoculum`; either path works.

Start the coordinator:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum coordinator --port 8080
```

Copy the fingerprint it prints, then connect a worker:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum worker \
  --coordinator 192.168.0.5:8080 \
  --id mac-worker \
  --coordinator-fingerprint '<fingerprint>'
```

Submit four duplicate-safe diagnostic tasks:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum submit \
  --coordinator 192.168.0.5:8080 \
  --type diagnostic_sleep \
  --input 2s \
  --tasks 4
```

Start the same worker command on another machine with a different `--id` to add
it to the pool.

## How it works

```text
                submit
                  |
                  v
            +-------------+
            | coordinator |
            +-------------+
              ^         ^
              |         |
       outbound pull  outbound pull
              |         |
        +----------+ +----------+
        | worker A | | worker B |
        +----------+ +----------+
```

Only the coordinator listens for Inoculum connections. It owns the task queue
and leases. Workers connect outbound, claim tasks, renew their leases while
executing, and return results. Workers do not advertise an address or listen for
incoming tasks.

If a worker disappears, its lease expires and the same stable task becomes
available to another worker. Delivery is therefore **at least once**: a task can
run more than once if a worker finishes but its result is lost. Workloads must be
safe under duplicate execution.

## Releases and installation

Inoculum is one Go binary with three subcommands:

```text
inoculum coordinator
inoculum worker
inoculum submit
```

Release `v1.0.0` uses one archive per platform:

- `inoculum_v1.0.0_darwin_arm64.tar.gz`
- `inoculum_v1.0.0_linux_amd64.tar.gz`
- `inoculum_v1.0.0_windows_amd64.zip`

Each archive contains the platform binary and `LICENSE`. Download the matching
archive and `SHA256SUMS` from the release after it is published.

On macOS, verify, extract, and run the arm64 archive:

```bash
grep 'darwin_arm64' SHA256SUMS | shasum -a 256 -c -
tar -xzf inoculum_v1.0.0_darwin_arm64.tar.gz
./inoculum --version
```

You may move `inoculum` into a directory on your `PATH`, such as
`/usr/local/bin`. The v1.0.0 binary is unsigned and not notarized, so macOS may
require you to explicitly allow it in local Privacy & Security settings.

On Linux amd64:

```bash
grep 'linux_amd64' SHA256SUMS | sha256sum -c -
tar -xzf inoculum_v1.0.0_linux_amd64.tar.gz
chmod +x inoculum
./inoculum --version
```

Move `inoculum` to a directory on your `PATH` if desired.

The Windows amd64 archive is **experimental**. It is compile-tested and its
configuration-path behavior is unit-tested, but the runtime has not been
physically validated on Windows. Extract the zip and run:

```powershell
.\inoculum.exe --version
```

No release binaries are currently signed.

### Build from source

Native development build:

```bash
go build -o build/inoculum ./cmd/inoculum
./build/inoculum --version
```

An ordinary source build reports `inoculum dev`. Release builds inject the
version with this linker assignment:

```text
-X github.com/inoculum/internal/version.Value=v1.0.0
```

No commit SHA, build date, local path, or machine identity is included in
`--version` output.

Linux amd64 cross-build:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -o build/inoculum-linux-amd64 ./cmd/inoculum
```

Windows amd64 compile-only build:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -o build/inoculum-windows-amd64.exe ./cmd/inoculum
```

Copy the appropriate binary to each machine using your normal software or file
distribution method. Inoculum does not transfer its own binaries.

## Quick start on a LAN

Choose the coordinator machine's LAN address. The examples use `192.168.0.5`.

### 1. Start the coordinator

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum coordinator --port 8080
```

The coordinator prints its address and certificate fingerprint. Keep the token
private and use the same value on each client machine.

### 2. Trust and start each worker

On the first connection from a machine, provide the fingerprint printed by the
coordinator:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum worker \
  --coordinator 192.168.0.5:8080 \
  --id linux-worker \
  --coordinator-fingerprint '<fingerprint>'
```

After successful verification, the coordinator identity is saved locally.
Subsequent worker starts on that machine do not need the fingerprint:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum worker \
  --coordinator 192.168.0.5:8080 \
  --id linux-worker
```

Worker and submit commands run by the same OS user share this saved trust
record. A different machine or OS user must perform its own first verification.

### 3. Submit work

Once trust is established on the submitting machine:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum submit \
  --coordinator 192.168.0.5:8080 \
  --type diagnostic_sleep \
  --input 2s \
  --tasks 4
```

`--timeout` limits how long the client waits. If it expires, the coordinator job
is not marked failed and can continue running.

## Real workload: HTTP probe manifests

`http_probe` is Inoculum's first bounded real workload. A versioned JSON manifest
turns distinct inputs into independent tasks with stable user-defined keys.

Create `probes.json`:

```json
{
  "version": 1,
  "type": "http_probe",
  "tasks": [
    {"key": "homepage", "input": "https://example.com/"},
    {"key": "docs", "input": "https://example.com/docs"}
  ]
}
```

Submit it and save the detailed result:

```bash
export INOCULUM_TOKEN='a-long-random-secret'
./inoculum submit \
  --coordinator 192.168.0.5:8080 \
  --manifest probes.json \
  --output probe-results.json \
  --timeout 2m
```

Each task is independent. Workers naturally share the batch by pulling work as
capacity becomes available. Retries preserve the user key, and the exported
tasks remain in manifest order.

Manifest V1 accepts one `http_probe` type and up to 1,000 tasks. Keys must be
unique. Inputs and the overall document are bounded so one submission cannot
grow without limit.

An HTTP probe performs a bounded `HEAD` request with normal target TLS
verification and limited redirects. It records the HTTP status, final URL,
elapsed time, declared content length, TLS certificate expiry when available,
attempt count, and final worker.

## Result export

`--output` is strongly recommended for manifest submissions. Normal terminal
output stays progress-oriented while the JSON file contains per-task detail:

```json
{
  "job_id": "pull-job-1787005804821661000",
  "state": "completed",
  "tasks": [
    {
      "key": "homepage",
      "state": "completed",
      "attempts": 1,
      "worker": "mac-worker",
      "output": {
        "status_code": 200,
        "final_url": "https://example.com/",
        "elapsed_ms": 42,
        "declared_content_length": 1256,
        "tls_certificate_expiry": "2026-11-10T23:59:59Z"
      }
    }
  ]
}
```

Known final failures are also exported before submit exits nonzero. Failed tasks
include a structured error category and message where available. Result files do
not contain Inoculum tokens or Authorization headers.

## Simple submit mode

Simple mode repeats one input a requested number of times. It remains useful for
diagnostics and lease/failover testing:

```bash
./inoculum submit \
  --coordinator 192.168.0.5:8080 \
  --type diagnostic_sleep \
  --input 2s \
  --tasks 4
```

Use manifest mode when a real batch contains distinct inputs. Manifest mode
cannot be combined with `--type`, `--input`, or `--tasks`.

## Terminal and plain modes

On a supported interactive terminal, coordinator, worker, and submit commands
show a restrained live TUI. It summarizes connectivity, workers, active work,
progress, and failures without printing one line per task.

Inoculum automatically uses stable line-oriented output for redirected output,
CI, `TERM=dumb`, and other non-interactive environments. It never emits terminal
control sequences in plain mode.

Useful presentation flags:

| Flag | Purpose |
|---|---|
| `--plain` | Force line-oriented output |
| `--verbose` | Include additional diagnostic detail |
| `--no-color` | Disable color |
| `--ascii` | Avoid Unicode status symbols |
| `--log-file <path>` | Set the operational log used by the interactive UI |

No terminal screenshots are currently included in the repository.

## Security model

Inoculum is intended for a small, trusted, user-controlled LAN. Direct exposure
to the public internet is unsupported.

- The coordinator serves HTTPS only, using one stable self-signed identity.
- First trust requires an explicit coordinator fingerprint; there is no silent
  trust on first use.
- Successful verification saves the coordinator identity for later worker and
  submit runs by the same OS user.
- Every API request uses `Authorization: Bearer` with the shared token.
- The token is accepted through `INOCULUM_TOKEN` or `--token`; environment use is
  recommended to keep it out of shell history and process arguments.
- There are no worker certificates, mutual TLS, plaintext mode, nonce cache, or
  clock-based authentication checks.
- Optional sanitized coordinator audit logging is available with
  `--audit-log <path>`.

Possession of the shared token authorizes workers to perform `http_probe`
requests to HTTP and HTTPS addresses reachable from those workers, including LAN
and private endpoints. Give the token only to machines you control.

## Lease and retry behavior

The coordinator owns one global lease and retry policy:

| Coordinator flag | Default | Meaning |
|---|---:|---|
| `--lease-duration` | `6s` | Time a worker owns a task unless it renews the lease |
| `--max-attempts` | `3` | Maximum claims/execution attempts before permanent failure |

Claiming a task creates a lease. An active worker renews it while executing. If
the worker disappears, the lease expires and the same task returns to the FIFO
queue. Its stable identity and manifest key survive reassignment, while the
attempt count increases.

Lease behavior is at least once, not exactly once. Executors should avoid
non-idempotent side effects or otherwise tolerate duplicate execution.

## Built-in task types

| Type | Intended use | Input |
|---|---|---|
| `http_probe` | Bounded HTTP/TLS endpoint inspection; primary manifest workload | One absolute HTTP or HTTPS URL |
| `diagnostic_sleep` | Duplicate-safe lease, retry, and distribution testing | A duration up to 5 minutes, such as `2s` |
| `file_analyze` | Counts lines, words, and bytes in a worker-local file | A local file path allowed by the worker's `--allowed-paths` |

`file_analyze` does not transfer files. The referenced path must exist and be
allowed on whichever worker claims the task. It is therefore most appropriate
for deliberately pre-staged or consistently mounted data.

Inoculum does not execute arbitrary shell commands or external programs.

## Platform support

| Platform | Status |
|---|---|
| macOS | Physically validated as coordinator, worker, and submit client |
| Linux amd64 | Physically validated as an outbound worker against a macOS coordinator |
| Windows amd64 | Cross-compiles and has configuration-path unit coverage; runtime is unvalidated |

The validated Mac/Linux path includes HTTPS trust, task distribution, result
reporting, worker disappearance, lease expiry, and reassignment.

## Limitations and non-goals

V1 deliberately does not provide:

- arbitrary shell or command execution;
- DAGs or task dependencies;
- persistent services;
- containers or environment management;
- worker-to-worker communication;
- file upload, file distribution, or a distributed filesystem;
- dynamic plugins;
- resource-aware scheduling or worker capability negotiation;
- autoscaling;
- coordinator state persistence;
- public-internet-grade deployment.

The coordinator keeps queue and job state in memory. Restarting it loses queued,
leased, and completed job state. The original manifest remains the resubmission
source. Persistence can be reconsidered if real long-running workloads justify
the extra operational complexity.

## Development and verification

Run the complete checks from the repository root:

```bash
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify
git diff --check
```

Build the canonical executable with:

```bash
go build -o build/inoculum ./cmd/inoculum
```

Create all v1.0.0 release archives and checksums on macOS with:

```bash
./scripts/release.sh v1.0.0
```

The script replaces `release/` with three clean platform archives and
`SHA256SUMS`. It uses temporary staging directories, so repository logs, build
outputs, certificates, trust records, probe results, and other local state are
not candidates for archive inclusion. The release notes draft is
[docs/releases/v1.0.0.md](docs/releases/v1.0.0.md).

## License

Inoculum is available under the Apache License 2.0. Copyright 2026
Volkan 'Val March' Söylemez. See [LICENSE](LICENSE).
