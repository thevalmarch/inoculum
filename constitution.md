# Project Specification: Inoculum — LAN-Based Task-Parallel Distributed Compute Daemon

## 1. Project Purpose

**Inoculum** is a lightweight, minimal-dependency background service (daemon) that makes the processing power (CPU/GPU) of multiple computers connected to the same local network (LAN) — for example a Linux machine and a Mac — manageable from a single central control point.

The goal is **not** to split a large AI model into pieces and shuttle them across the network (pipeline parallelism). Instead, the system distributes **independent tasks** across different machines, and each machine executes its own task completely independently (task/data parallelism). Example scenario: one machine analyzes the database schema of a codebase while another machine simultaneously generates documentation for that same codebase. The two jobs are not dependent on each other, so there is no need for high-volume, low-latency data transfer between them.

## 2. Why Task/Data Parallelism Instead of Pipeline Parallelism?

This decision is the most critical architectural choice in the project, and it stems from a hard technical constraint.

**What pipeline parallelism is, and why it was rejected:**
It is possible to split an AI model's layers across different machines and "run" the model together by passing intermediate results (particularly the KV-cache data produced by the attention mechanism) from one machine to the next. However, this approach runs into the following physical limitation:

- Within a single GPU/CPU's own memory (VRAM/RAM), data flows at hundreds of gigabytes per second.
- A standard home LAN connection (WiFi or 1 Gbps Ethernet) offers roughly 100-125 megabytes per second.
- This gap creates a bottleneck of up to 1000x. Since this intermediate data would need to cross the network for every single token the model generates, the system becomes tens to hundreds of times slower than running on a single machine.

For this reason, pipeline parallelism is not practically usable on low-bandwidth networks like home/office LANs.

**Why task/data parallelism works:**
In this approach, only the following is transferred between machines: the task definition (a few KB of text/JSON) and the final result (again small text/JSON). There is no continuous, synchronous, high-volume data flow between machines. This eliminates the network bottleneck, and the system can realistically achieve measurable speedup.

## 3. System Architecture

The system consists of two main components:

### 3.1 Coordinator
- Maintains the list of all workers (worker nodes) on the network.
- Breaks incoming jobs down into smaller tasks.
- Assigns tasks to appropriate workers (simple round-robin, or "assign to the least busy worker" logic).
- Collects and merges results returned by workers.
- Can run on any machine; it can be one of the nodes on the network or a separate dedicated machine.

### 3.2 Worker (Worker Node)
- One instance runs on each participating machine.
- On startup, it registers itself with the coordinator: machine name, available resources (CPU core count, free RAM, GPU info if any).
- Receives tasks from the coordinator, processes them locally (e.g. a local LLM call, running a script, an analysis function).
- Sends the result back to the coordinator once the task is complete.
- Periodically sends a "heartbeat" (alive signal); the coordinator removes a worker from the list if its heartbeat stops.

### 3.3 Node Discovery
To eliminate the need for manually entering IP addresses, an mDNS (multicast DNS) mechanism or a simple UDP broadcast is used. When a new worker joins the network, it automatically finds the coordinator and registers itself.

## 4. Communication Protocol

- Transport layer: starts with HTTP/JSON (for simplicity and ease of debugging). Can later move to gRPC + Protocol Buffers if performance requires it.
- The coordinator exposes the following endpoints:
  - `POST /register` — a worker introducing itself
  - `POST /heartbeat` — a worker reporting that it is alive
  - `POST /submit-job` — submitting a new job from outside (from the user)
  - `GET /status` — overall system status: how many workers are active, how many tasks are queued
- The worker exposes the following endpoint:
  - `POST /execute` — request to execute a task sent by the coordinator

## 5. Data Structures (Conceptual)

**Job:** The high-level request given to the system by the user. Can be broken down into multiple Tasks.

**Task:** The smallest unit of work a single worker can perform independently. Contains: unique ID, task type, input data, status (pending/processing/completed/failed).

**WorkerInfo:** The worker's identity, network address, timestamp of the last heartbeat, whether it is currently busy.

**Result:** Task ID, produced output, processing duration, error message if any.

## 6. Technology Choice and Rationale

**Language: Go (Golang)**
- Go's `goroutine` model makes it easy to manage a large number of concurrent connections (requests coming from workers) using lightweight threads.
- Its standard library (`net/http`, `encoding/json`) provides an HTTP server and JSON serialization without external dependencies.
- As a compiled language that produces a single binary, deployment across different operating systems (Linux, macOS) is simple — a single file can be copied to each machine without a separate installation process.
- Compared to C, the risk of memory management and socket programming errors (e.g. segmentation faults) is much lower, which increases development speed.

## 7. Metrics to Measure

Whether the system actually provides value should be verified through measurement, not assumption:

1. **Round-trip latency:** The time from the coordinator sending a task to a worker until it receives the result back (target: single-digit milliseconds, for network communication alone).
2. **Task completion time:** The time each worker takes to finish its own task.
3. **Total speedup:** Comparing the time it takes to complete the same job sequentially on a single machine versus in parallel across multiple machines. This number is the ultimate proof of whether the project delivers real value.
4. **Coordinator overhead:** The additional time the coordinator spends on task distribution and result collection.

## 8. Development Plan (Phased)

**Phase 1 — Skeleton and communication test:**
Verify that a dummy task can be sent between the two components (coordinator, worker) and a result received back. There is no real task logic yet; the goal is purely to prove the communication channel works. Round-trip latency is measured at this stage.

**Phase 2 — Node discovery:**
Enable a newly joined worker to automatically find the coordinator and register itself, without manually entering an IP address.

**Phase 3 — Real task integration:**
Replace the dummy task with an actual workload (e.g. a local language model call, a file analysis, a build/compile process).

**Phase 4 — Measurement and comparison:**
Run the same job both on a single machine and in a distributed fashion, compare the durations, and calculate the actual speedup ratio.

**Phase 5 (optional, advanced):**
Smart task distribution based on worker load (prioritizing the least busy machine), fault tolerance (reassigning a task if a worker crashes).

## 9. Explicitly Out of Scope

These decisions were made deliberately to keep the project's complexity manageable:

- **Blockchain / cryptocurrency / token economy:** Since the system operates among trusted, known machines, there is no need for a payment or trust mechanism.
- **Zero-Knowledge Proofs / verifiable computation:** For the same reason, there is no need to cryptographically prove that a computation was actually performed correctly.
- **Splitting model layers across the network (pipeline parallelism):** Deliberately excluded due to the bandwidth bottleneck explained above.
- **Building a distributed network over the public internet with unknown machines:** The system is designed only for trusted machines on a local network; this is not a "public compute marketplace."

## 10. Expected Outcome

The end result of this project: a minimal-dependency Go application, named **Inoculum**, managed from the terminal, that combines the processing power of two (or more) physical machines through automatic discovery and synchronization without user intervention, and delivers measurable, real speedup.
