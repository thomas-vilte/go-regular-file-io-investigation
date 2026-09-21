# Phase 6C: issuer task contexts and submitter migration

Phase 6B resolved the account-control question for this Environment B path.
With forced-async resident reads, changing the bounded maximum did not
materially reduce the approximately 300 process tasks. A verified
unbounded maximum of one reduced that peak to approximately 13--15 tasks.
This is an empirical result for Linux 7.2.3-1-cachyos, this ring, and this
workload; it is not a claim about every Linux kernel or every io_uring opcode.

The corrected observer found `iou-wrk-<tid>` tasks sharing the workload Tgid
and cgroup. In the unbounded-one run it saw several owner suffixes despite a
verified maximum of one. A worker maximum is therefore treated as a maximum
per relevant io-wq/task context, not as a process-wide task maximum. Current
upstream code is only a plausible explanatory model; no upstream source is
used as proof for this CachyOS kernel.

## Diagnostic change

The ring records bounded, sorted sets of Linux TIDs immediately before:

```text
io_uring_setup / io_uring_register  -> setup_tid and setup_register_*
submit-side io_uring_enter           -> submit_enter_*
CQ-wait io_uring_enter               -> wait_enter_*
```

Each set holds at most 64 IDs. JSON includes the IDs, stored count, and an
explicit truncation field, so the count is never silently presented as a
complete set if it exceeded the cap. This adds a `gettid(2)` diagnostic syscall
before each instrumented enter; Phase 6C performance values are therefore
topology observations, not directly comparable as exact throughput values to
un-instrumented Phase 6B.

`-uring-lock-submitter-thread` is a diagnostic-only option valid with
`-backend=uring`. The existing submit goroutine calls `runtime.LockOSThread`
when the loop begins and defers `UnlockOSThread` until that loop ends. It does
not lock logical clients, the completion consumer, or the benchmark main
goroutine; it does not enable `IORING_SETUP_SINGLE_ISSUER` or change SQ/CQ
batching, admission, ownership, worker maxima, or ring count.

Relevant JSON fields are:

```text
io_uring.lock_submitter_thread
io_uring.setup_tid
io_uring.setup_register_distinct_tids
io_uring.setup_register_tid_count
io_uring.submit_enter_distinct_tids
io_uring.submit_enter_tid_count
io_uring.wait_enter_distinct_tids
io_uring.wait_enter_tid_count
io_uring.issuer_tid_sets_truncated
```

The observer now emits `issuer_tid_correlation` in `summary.txt`, mapping each
observed `iou-wrk-<owner suffix>` to membership in submit- and wait-enter TID
sets. It remains observational only. For a five-second observation, reject a
summary with fewer than 100 samples; use the harness lightweight sampler—not
the observer—for peak process task count with hundreds of tasks.

## Environment B commands

Use the authoritative toolchain and preserve the actual current revision in
every JSON:

```sh
cd /operator-home/bench
export GO_TOOL=/operator-home/go-upstream-go1.27.1/bin/go
git rev-parse HEAD
"$GO_TOOL" test -count=1 -buildvcs=false ./...
"$GO_TOOL" test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline

mkdir -p phase6c-artifacts
"$GO_TOOL" build -buildvcs=false -o phase6c-artifacts/iobaseline ./cmd/iobaseline
```

Run exactly three repetitions of each configuration below; interleave the four
conditions rather than running all repetitions of one condition together. Do
not use latency mode.

```sh
common=(
  -backend=uring -uring-force-async -operation=readat
  -file=./data/readat-512MiB.bin -file-access=shared_fd
  -buffer-bytes=1048576 -concurrency=1000 -max-outstanding=1000 -gomaxprocs=4
  -duration=5s -warmup=0s -seed=31908
  -cache-precondition=prewarm -cache-state=residency_verified_mostly_resident
  -runtime-sample-interval=10ms -harness-revision="$(git rev-parse HEAD)"
)

# A: default worker maxima; submitter unlocked.
phase6c-artifacts/iobaseline "${common[@]}" >phase6c-artifacts/A-default-unlocked-r1.json
# B: default worker maxima; submitter locked.
phase6c-artifacts/iobaseline "${common[@]}" -uring-lock-submitter-thread >phase6c-artifacts/B-default-locked-r1.json
# C: verified unbounded=1; submitter unlocked.
phase6c-artifacts/iobaseline "${common[@]}" -uring-unbounded-workers=1 >phase6c-artifacts/C-u1-unlocked-r1.json
# D: verified unbounded=1; submitter locked.
phase6c-artifacts/iobaseline "${common[@]}" -uring-unbounded-workers=1 -uring-lock-submitter-thread >phase6c-artifacts/D-u1-locked-r1.json
```

For every JSON require `status=ok`, prewarm and pre-measurement residency of
1,000,000 ppm, and the existing io_uring reconciliation conditions. For C and
D, separately observe task identity (one observational run each):

```sh
scripts/phase4-observe-workers.sh uring phase6c-artifacts/observe-C ./data/readat-512MiB.bin \
  --prewarm --force-async --unbounded-workers=1
scripts/phase4-observe-workers.sh uring phase6c-artifacts/observe-D ./data/readat-512MiB.bin \
  --prewarm --force-async --unbounded-workers=1 --lock-submitter-thread
```

Expected ignored artifacts:

```text
phase6c-artifacts/A-default-unlocked-r{1,2,3}.json
phase6c-artifacts/B-default-locked-r{1,2,3}.json
phase6c-artifacts/C-u1-unlocked-r{1,2,3}.json
phase6c-artifacts/D-u1-locked-r{1,2,3}.json
phase6c-artifacts/observe-C/{result.json,summary.txt,samples/,task-metadata/}
phase6c-artifacts/observe-D/{result.json,summary.txt,samples/,task-metadata/}
```

## Interpretation boundary

Support for the submitter-migration explanation requires more than a changed
task peak: unlocked C must show multiple submit-enter TIDs and multiple
`iou-wrk` owner suffixes, while locked D must show one submit-enter TID and a
correspondingly reduced owner/pool topology. If D still shows multiple owner
suffixes unrelated to its one submitter TID, the hypothesis is incomplete.
No tuning follows from either result in this phase.
