# Phase 6: io-wq bounded-worker scaling diagnostic

Phase 5 used a 100%-resident 512 MiB dataset and found that normal batched
io_uring reached about 6.8 GiB/s with about one process CPU core, while adding
`IOSQE_ASYNC` reached about 9.8 GiB/s with about 3.1 cores and roughly 300
externally observed tasks. Forced async also reduced submission batching to
approximately one enter per operation. This phase asks only whether a bounded
io-wq worker maximum can retain useful forced-async parallelism without that
large task population. It is not a runtime-design proposal.

## API and setup sequence

The implementation uses local Linux UAPI values verified on this system:

```text
__NR_io_uring_register = 427       (asm/unistd_64.h)
IORING_REGISTER_IOWQ_MAX_WORKERS = 19 (linux/io_uring.h)
```

The register argument is `[2]uint32` with `[bounded, unbounded]`; `nr_args` is
2. The installed `io_uring_register(2)` manual documents that `[0, 0]` returns
the current maxima without changing either, and that every successful call
overwrites the supplied array with the values that applied before the call.
These limits are per ring/per NUMA node according to that API; they are not
observed worker counts.

Each ring now performs, before submitter/completion goroutines start:

```text
io_uring_setup
  -> mmap SQ/CQ/SQEs
  -> register MAX_WORKERS with [0, 0]       # query/no modification
  -> when explicitly requested:
       register MAX_WORKERS with [N, queried_unbounded]
  -> begin accepting READ operations
```

The second registration preserves the query-returned unbounded maximum. If a
requested setting cannot query or set successfully, ring construction fails;
it never silently runs with an unlabeled default. For an unrequested setting,
a failed query is recorded and normal ring operation remains unchanged.

## Configuration and JSON

`-uring-bounded-workers=N` has these semantics:

```text
option absent or N < 0: do not set a worker maximum
N = 0:                 reject
N >= 1:                requires -backend=uring -uring-force-async
```

`io_uring.iowq_worker_config` records the query/set attempts, API support,
previous bounded/unbounded maxima, requested bounded/unbounded maxima,
successful set-returned previous maxima, and concrete errors. It does not
claim that a configured maximum equals an observed task count.

Pinner ownership, operation IDs, admission, SQ publication, submit batching,
CQ draining, ring count, and the `IOSQE_ASYNC` selection remain otherwise
unchanged. The harness still rejects any run whose terminal reconciliation is
not clean.

## Correctness gate

On Environment B, first run the existing ring suite, whose kernel-backed
subtests cover normal and force-async modes, followed by the race build. Then
run at least small bounded settings (1 and 4) through the harness before the
curve. The race detector does not model kernel access to pinned buffers.

```sh
cd /operator-home/bench
export GO_TOOL=/operator-home/go-upstream-go1.27.1/bin/go
"$GO_TOOL" test -count=1 -buildvcs=false -v ./internal/uring ./cmd/iobaseline
"$GO_TOOL" test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline

mkdir -p phase6-gate
"$GO_TOOL" build -buildvcs=false -o phase6-gate/iobaseline ./cmd/iobaseline
"$GO_TOOL" build -buildvcs=false -o phase6-gate/phase6check ./cmd/phase6check
for workers in 1 4; do
  phase6-gate/iobaseline -backend=uring -uring-force-async -uring-bounded-workers="$workers" \
    -operation=readat -file=./data/readat-512MiB.bin -file-access=shared_fd \
    -buffer-bytes=4096 -concurrency=32 -max-outstanding=4 -gomaxprocs=4 \
    -operations=128 -seed=31908 -cache-precondition=prewarm \
    -cache-state=residency_verified_mostly_resident >"phase6-gate/bounded-${workers}.json"
  phase6-gate/phase6check -file "phase6-gate/bounded-${workers}.json" -expect-bounded-workers="$workers"
done
```

## Resident worker curve

The runner uses the authoritative toolchain, requires
`f9604c8e7b98d1f6d60cbb4ea7efb4bf9a53dfb7` to be an ancestor of `HEAD`, and
records the actual `HEAD` in every JSON. It makes no privileged change and
stops after any failed status, reconciliation, or resident precondition.

```sh
rm -rf phase6-artifacts
scripts/phase6-worker-curve.sh phase6-artifacts ./data/readat-512MiB.bin
```

Every one of the 33 processes uses:

```text
ReadAt random, shared fd, 1 MiB
1000 logical clients, max-outstanding=1000, GOMAXPROCS=4
duration=5s, warmup=0, seed=31908
prewarm and residency-verified-mostly-resident
```

The fixed interleaved order is recorded in `execution-order.txt`. It includes
three repetitions each of blocking, normal io_uring, force-async default, and
force-async bounded values `1, 2, 4, 8, 16, 32, 64, 128`. The runner's
`phase6check` accepts a JSON only if both post-prewarm and pre-measurement
mincore values are exactly 1,000,000 ppm and all io_uring invariants reconcile.

Artifacts are intentionally ignored:

```text
phase6-artifacts/environment.txt
phase6-artifacts/execution-order.txt
phase6-artifacts/runs/r<rep>-<mode>.command.txt
phase6-artifacts/runs/r<rep>-<mode>.json
phase6-artifacts/runs/r<rep>-<mode>.check.txt
```

## Worker observation

`scripts/phase4-observe-workers.sh` now samples root tasks every 25 ms during
an observational run, storing each root TID's `comm`, selected `status` fields,
and cgroup plus summary peaks/distinct matching TIDs. It also retains global
named-worker cgroup snapshots. These runs are observational only and are not
substitutes for the curve repetitions.

```sh
scripts/phase4-observe-workers.sh uring phase6-artifacts/workers-default ./data/readat-512MiB.bin --prewarm --force-async
scripts/phase4-observe-workers.sh uring phase6-artifacts/workers-4 ./data/readat-512MiB.bin --prewarm --force-async --bounded-workers=4
```

Treat `iou-wrk-*`/`io_wq*` names as evidence to investigate, not proof that all
extra tasks are io-wq or that they are charged to the benchmark process.

## Analysis boundary

For every row, report median and min--max MiB/s, effective process CPU cores,
CPU microseconds/op, peak process tasks, observed named io-wq workers, and
enters/op. Include blocking, normal io_uring, and force-async default rows;
then report ratios to blocking and force-async default separately. Do not make
a composite score or infer that the best host-specific limit is a Go runtime
policy.
