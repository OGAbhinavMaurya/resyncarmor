<div align="center">

# resyncarmor

**Redis and Valkey replication death spiral circuit breaker, dynamic backlog autotuner, and copy-on-write memory guard.**

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Zero Dependencies](https://img.shields.io/badge/Dependencies-Zero%20(Pure%20Go)-green)](#)

<p align="center">
  <a href="#the-problem">The problem</a> •
  <a href="#how-it-works">How it works</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#comparison">Comparison</a> •
  <a href="#installation">Installation</a> •
  <a href="#usage">Usage</a> •
  <a href="#license">License</a>
</p>

</div>

## The problem

Redis and Valkey rely on an in-memory circular replication backlog (`repl-backlog-size`) to facilitate partial resynchronization (`PSYNC`) when a replica temporarily disconnects. By default, Redis ships with a 1 MB backlog (`1048576` bytes). In production environments with 10 MB/s to 50 MB/s write throughput, a 1 MB backlog wraps around in 20 to 100 milliseconds.

When a transient network hiccup, container rescheduling, or GC pause exceeds this tiny fraction of a second, the master node rejects partial resync and falls back to a full resync (`+FULLRESYNC`):

```
  Primary Node (Master)                                         Secondary Node (Replica)
          |                                                                 |
          |  Write velocity: 15 MB/s                                        |
          |  repl-backlog-size: 1 MB (Buffer capacity: 66ms)                |
          |                                                                 |
          |                      [Transient 200ms network jitter]           |
          |                                       x <-----------------------| Connection interrupted
          |                                                                 |
          |  Circular backlog wraps around (offset purged)                  |
          |                                                                 |
          | <---------------------------------------------------------------| PSYNC <replid> <offset>
          |  Offset out of range! Backlog overwritten!                      |
          |---------------------------------------------------------------->| +FULLRESYNC <replid> <offset>
          |                                                                 |
          |  [Master forks BGSAVE snapshot process]                         |
          |  - Memory forks into child process                              |
          |  - THP causes 2 MB page amplification on single writes           |
          |  - Private_Dirty memory balloons (COW spike)                    |
          |  - Master hits Linux OOM killer -> SIGKILL                      |
          x                                                                 |
```

This triggers the replication death spiral:

1. **Circular buffer wrap-around**: A 1 MB backlog cannot absorb even 100ms of network latency under standard write loads. The Replication Safety Horizon ($T_{\text{horizon}} = \frac{\text{repl-backlog-size}}{V_{\text{write}}}$) drops into sub-second territory, making every blip catastrophic.
2. **Copy-on-write memory explosion**: When `+FULLRESYNC` triggers `fork()` for `BGSAVE`, Linux memory management relies on Copy-on-Write (COW). If Transparent Huge Pages (THP) is enabled (`[always]`), writing a single 16-byte key forces the kernel to copy an entire 2 MB huge page. Memory consumption surges, triggering the Linux Out-Of-Memory (OOM) killer.
3. **Client output buffer limit termination**: During snapshot transfer, live mutations accumulate in `client-output-buffer-limit replica 256mb 64mb 60`. If RDB serialization or network transfer takes longer than 60 seconds, the master terminates the replica socket. The replica reconnects, fails `PSYNC`, and repeats the cycle indefinitely.

### Why existing approaches fail

- **Static configuration files**: Setting a static `repl-backlog-size` in `redis.conf` fails to adapt to traffic spikes or varying cluster memory availability.
- **Monitoring alerts after the fact**: Prometheus and Datadog alert when `sync_full` counters increment, but by that time the master has already forked, memory has surged, and the cluster is destabilized.
- **Generic memory limits**: Relying strictly on `maxmemory` fails to protect against kernel COW memory spikes, which are tracked in kernel page tables rather than the Redis memory allocator.

## How it works

`resyncarmor` acts as an autonomous sentry and autotuner for Redis and Valkey instances:

```
  resyncarmor doctor / tune               Redis / Valkey Target             Host Linux Kernel
            |                                       |                               |
            |  1. Sample repl_offset over time      |                               |
            |-------------------------------------->|                               |
            |  Measures write velocity (Vwrite)     |                               |
            |<--------------------------------------|                               |
            |                                       |                               |
            |  2. Read smaps_rollup Private_Dirty   |                               |
            |---------------------------------------------------------------------->|
            |  Audits COW footprint & THP sysfs     |                               |
            |<----------------------------------------------------------------------|
            |                                       |                               |
            |  3. Calculate Replication Safety      |                               |
            |     Horizon (Target: 60s - 120s)      |                               |
            |                                       |                               |
            |  4. Execute safe CONFIG SET           |                               |
            |     (bounded by memory headroom)      |                               |
            |-------------------------------------->|                               |
            |  Backlog adjusted safely              |                               |
            |<--------------------------------------|                               |
```

1. **Replication safety horizon sentry**: Samples the master replication offset stream to calculate instantaneous write velocity in bytes per second. It evaluates the exact buffer duration and alerts when the safety horizon falls below operational safety thresholds.
2. **Kernel COW and THP auditor**: Inspects `/proc/<pid>/smaps_rollup` to quantify active `Private_Dirty` memory overhead and checks `/sys/kernel/mm/transparent_hugepage/enabled` to detect dangerous 2 MB huge page amplification risks.
3. **Dynamic backlog autotuner**: Calculates the required backlog size to survive target downtime windows (e.g., 60 to 120 seconds of network partition). It cross-checks available host RAM and safely executes runtime `CONFIG SET repl-backlog-size` without restarting the daemon.

## Architecture

`resyncarmor` is written in pure Go with zero external dependencies.

```
resyncarmor/
├── cmd/
│   └── resyncarmor/
│       ├── main.go             # CLI interface: doctor and tune commands
│       └── main_test.go        # Command-line integration tests
├── pkg/
│   ├── resp/
│   │   ├── parser.go           # RESP2 protocol encoder and decoder
│   │   ├── client.go           # Redis client for INFO, CONFIG GET, CONFIG SET
│   │   ├── mock_server.go      # In-memory Redis test server
│   │   └── resp_test.go        # Protocol and mock server tests
│   ├── kernel/
│   │   ├── thp.go              # Transparent Huge Pages configuration auditor
│   │   ├── smaps.go            # /proc/<pid>/smaps_rollup Private_Dirty parser
│   │   └── kernel_test.go      # Kernel metric parsing tests
│   └── sentry/
│       ├── doctor.go           # Write velocity and replication safety calculations
│       ├── autotune.go         # Safe backlog sizing and runtime adjustment
│       └── sentry_test.go      # Sentry and autotuning logic tests
├── go.mod
└── README.md
```

## Comparison

| Feature | Default Redis | Custom Shell Scripts | Prometheus Exporters | resyncarmor |
| :--- | :--- | :--- | :--- | :--- |
| **Replication safety horizon** | None (Static 1 MB) | Manual math | Retrospective alerts | Active measurement |
| **Write velocity tracking** | Manual calculation | Fragile parsing | Metric collection | Continuous sampling |
| **COW memory auditing** | None | Ad-hoc `grep` | Basic RSS tracking | `Private_Dirty` inspection |
| **THP hazard detection** | Log warning only | None | None | Automated validation |
| **Dynamic backlog tuning** | Static | Risky shell commands | Read-only | Headroom-bounded `CONFIG SET` |
| **Zero dependencies** | Yes | External utilities | Go / Python daemons | Pure Go (single binary) |

## Installation

### From source

```bash
git clone https://github.com/OGAbhinavMaurya/resyncarmor.git
cd resyncarmor
go build -o resyncarmor ./cmd/resyncarmor
```

### Pre-built binary

Download the latest release binary for Linux from the GitHub releases page.

## Usage

### Doctor mode

Inspect a Redis or Valkey instance to calculate write velocity, replication safety horizon, and kernel memory risks:

```bash
resyncarmor doctor --redis 127.0.0.1:6379 --sample 2s --target-horizon 60s
```

Example output:

```text
================================================================================
                           RESYNCARMOR DOCTOR REPORT                            
================================================================================
 Redis Address:             127.0.0.1:6379 (Role: master, Replicas: 2)
 Current Backlog Size:      1048576 bytes (1.00 MB)
 Measured Write Velocity:   14336.00 KB/s (14.00 MB/s)
 Safety Horizon:            0.07 seconds (Target: 60 seconds)
 Kernel THP Mode:           [always] (Hazardous: true)
 COW Memory (PrivateDirty): 412.50 MB
 Status:                    [CRITICAL]
 Summary:                   Replication Safety Horizon (0.07s) is well below target (60.00s); risk of death spiral on network jitter
 Recommendation:            Increase repl-backlog-size to at least 840.00 MB. Disable THP ('echo never > /sys/kernel/mm/transparent_hugepage/enabled').
--------------------------------------------------------------------------------
 Diagnostic Trace:
   * Current repl-backlog-size is 1.00 MB
   * Measured write velocity: 14.00 MB/s over 2.0s
   * Replica will trigger +FULLRESYNC if disconnected for more than 71ms
   * THP is [always]; COW page copies during BGSAVE will allocate 2MB chunks
================================================================================
```

### Autotune mode

Scale the replication backlog dynamically to match write throughput:

```bash
# Preview proposed changes (dry run)
resyncarmor tune --redis 127.0.0.1:6379 --target-horizon 120s --dry-run

# Apply configuration live
resyncarmor tune --redis 127.0.0.1:6379 --target-horizon 120s --max-memory-pct 15
```

Example output:

```text
================================================================================
                           RESYNCARMOR TUNE REPORT                              
================================================================================
 Redis Address:             127.0.0.1:6379
 Mode:                      DryRun=false (Applied: true)
 Previous Backlog Size:     1048576 bytes (1.00 MB)
 Tuned Backlog Size:        268435456 bytes (256.00 MB)
 New Safety Horizon:        120.00 seconds
 Summary:                   Autotune complete. Backlog scaled from 1.00 MB to 256.00 MB, extending Replication Safety Horizon to 120.0 seconds.
--------------------------------------------------------------------------------
 Tuning Actions:
   * Write velocity measured at 2.13 MB/s
   * Memory headroom verified: 12.4 GB available
   * Executed CONFIG SET repl-backlog-size 268435456
================================================================================
```

### JSON output for automation

Add `--json` to any command for integration into CI/CD pipelines or monitoring scripts:

```bash
resyncarmor doctor --redis 127.0.0.1:6379 --json
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
