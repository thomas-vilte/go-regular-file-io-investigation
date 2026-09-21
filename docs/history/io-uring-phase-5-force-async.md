# Phase 5: forced-async execution-context diagnostic

Phase 4 established a host-specific gap on a four-logical-CPU btrfs/zstd/NVMe
host: in the canonical measurement, blocking `ReadAt` used roughly 2.76
process CPU cores and reached about 10.6 GiB/s, while the batched normal
io_uring prototype used roughly one process CPU core and reached about 6.5
GiB/s. Device statistics show that the five-second measurement becomes
page-cache-resident after the initial dataset population. No `iou-wrk-*` task
was observed for normal SQEs, which do not use `IOSQE_ASYNC`.

This phase tests one causal hypothesis only: normal buffered page-cache reads
may execute through the prototype's single submission context, whereas
blocking syscalls execute that path on many threads. It does **not** propose
`IOSQE_ASYNC` as a runtime design.

## Change under test

`cmd/iobaseline` has an explicit, default-false option:

```text
-uring-force-async
```

It is accepted only with `-backend=uring`. It creates the same single ring,
single submitter, single completion consumer, batching, admission limit,
operation table, and Pinner lifecycle as the normal backend. The sole SQE
difference is that every `IORING_OP_READ` gets `IOSQE_ASYNC` (bit 4) in
`sqe.flags`. With the option absent, the assigned flags value is zero, as in
the Phase 3 implementation.

The result JSON records both `config.uring_force_async` and
`io_uring.force_async`. All existing reconciliation requirements remain
unchanged:

```text
operations_published == terminal_completions
pins_created == pins_released
operation table == 0
outstanding == 0
unknown completion IDs == 0
duplicate CQEs == 0
```

## Required Environment B run

Run from the committed revision being tested. Record its SHA before building;
each JSON must receive that same SHA through `-harness-revision`.

First run the correctness gate. The kernel-backed tests execute every existing
ring scenario in both `normal` and `force_async` subtests, including the
forced-GC lifecycle test. Stop on any failure; the Go race detector still does
not model kernel access to the pinned/mapped memory.

```sh
cd /operator-home/bench
export GO_TOOL=/operator-home/go-upstream-go1.27.1/bin/go
"$GO_TOOL" test -count=1 -buildvcs=false -v ./internal/uring ./cmd/iobaseline
"$GO_TOOL" test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline
```

Only after that gate passes, use the following three-repetition comparison:

```sh
cd /operator-home/bench
export GO_TOOL=/operator-home/go-upstream-go1.27.1/bin/go
git rev-parse HEAD
git merge-base --is-ancestor b35bd95d1c4379151f714da4127bb17dd905ee9f HEAD
run_revision=$(git rev-parse HEAD)
mkdir -p phase5-artifacts/resident
"$GO_TOOL" build -buildvcs=false -o phase5-artifacts/iobaseline ./cmd/iobaseline

common=(
  -operation=readat -file=./data/readat-512MiB.bin -file-access=shared_fd
  -buffer-bytes=1048576 -concurrency=1000 -max-outstanding=1000 -gomaxprocs=4
  -duration=5s -warmup=0s -seed=31908
  -cache-state=residency_verified_mostly_resident -cache-precondition=prewarm
  -runtime-sample-interval=10ms -harness-revision="$run_revision"
)

for rep in 1 2 3; do
  phase5-artifacts/iobaseline -backend=blocking "${common[@]}" >"phase5-artifacts/resident/blocking-r${rep}.json"
  phase5-artifacts/iobaseline -backend=uring "${common[@]}" >"phase5-artifacts/resident/uring-normal-r${rep}.json"
  phase5-artifacts/iobaseline -backend=uring -uring-force-async "${common[@]}" >"phase5-artifacts/resident/uring-force-async-r${rep}.json"
done
```

No `DONTNEED` request is made in this diagnostic. The harness prewarms the
experiment-owned file before every fresh process and records mincore snapshots.
Accept a run only if both `dataset.residency_after_prewarm` and
`dataset.residency_before_measurement` report all pages resident
(`resident_ppm == 1000000`). Otherwise preserve the JSON and mark that run as
not meeting the resident precondition; do not relabel it as resident.

The process rusage start/end snapshots provide user CPU, system CPU, maximum
RSS, and voluntary/involuntary context switches. For each JSON calculate and
record:

```text
process_cpu_ns = (rusage_end.user_ns - rusage_before.user_ns) +
                 (rusage_end.system_ns - rusage_before.system_ns)
process_cpu_per_wall = process_cpu_ns / duration_ns
cpu_us_per_operation = process_cpu_ns / operations / 1000
```

Also record the existing runtime thread, `not-in-go`, runnable, submit-batch,
CQ-drain, and enter counters directly from the JSON. Latency mode is excluded.

## Worker snapshots

The existing observer retains its Phase 4 behavior by default. `--prewarm`
selects the resident diagnostic precondition and `--force-async` adds only the
harness option above. Take one observational run for each mode; these are not
substitutes for the three-repetition counter runs.

```sh
rm -rf phase5-artifacts/workers-blocking phase5-artifacts/workers-normal phase5-artifacts/workers-force-async
scripts/phase4-observe-workers.sh blocking phase5-artifacts/workers-blocking ./data/readat-512MiB.bin --prewarm
scripts/phase4-observe-workers.sh uring phase5-artifacts/workers-normal ./data/readat-512MiB.bin --prewarm
scripts/phase4-observe-workers.sh uring phase5-artifacts/workers-force-async ./data/readat-512MiB.bin --prewarm --force-async
```

Inspect the saved `samples/` snapshots for `iou-wrk-*` or `io_wq*` tasks, their
PID/PPID/TID relationship, cgroup membership, and whether they are present in
the benchmark process's `/proc/<pid>/task` list. Names alone do not establish
ownership or CPU accounting.

## Interpretation boundary

Strong support for the execution-context hypothesis requires all of: forced
async workers observed, materially greater CPU parallelism than normal io_uring,
and materially higher throughput. If async workers appear but CPU parallelism
and throughput remain near normal io_uring, that evidence weighs against the
hypothesis. If throughput approaches blocking while worker/thread count grows
substantially, preserve that tradeoff without tuning worker limits.

This phase does not change SQPOLL, registered resources, fixed buffers, ring
count, submitter count, batching, completion draining, admission, or the Go
runtime. No Phase 5 performance result has been collected in the restricted
Codex sandbox.
