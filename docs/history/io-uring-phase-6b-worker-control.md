# Phase 6b: validating io-wq worker controls

Phase 6 is a failed control-variable experiment, not a worker-scaling result.
All 33 resident runs reconciled correctly and verified 1,000,000 resident ppm
before measurement, but changing the requested bounded maximum from 1 through
128 did not change the externally observed process-task peak materially.
The configured maximum must therefore not be presented as an observed worker
count or as a throughput-scaling variable.

The Phase 6 archive records `harness_revision`
`f69cccb803640887c782241b943ec5f4b851aeb4`. It is a descendant of the
reported Phase 6 implementation commit
`113659019d17566a4d5b430ca7368ab57f69794b`: the intervening commits only made
the runner executable and moved the io_uring statistics snapshot until after
`Ring.Close`, preventing a false final-CQE reconciliation failure. The runner
records `git rev-parse HEAD`, so the archive correctly identifies the actual
execution revision rather than the earlier implementation revision.

## Control validation added here

`IORING_REGISTER_IOWQ_MAX_WORKERS` takes `[bounded, unbounded]`. A successful
set overwrites that argument with the *previous* values, so its output cannot
verify the setting just requested. For any diagnostic worker limit, ring setup
now performs, before submitter/completion goroutines start:

```text
register MAX_WORKERS [0, 0]             -> previous/default [B, U]
register MAX_WORKERS [requested B, U]   -> returns prior [B, U]
register MAX_WORKERS [0, 0]             -> post-set [B', U']
require [B', U'] == [requested B, U]
```

When only one account is explicitly requested, the first query's value for the
other account is preserved. If the post-set query errors or differs from the
requested pair, ring construction fails before any READ is admitted. JSON
keeps previous values, requested values, set-returned previous values,
post-set values, and distinct query/registration/post-set errors. A configured
limit remains distinct from a task count observed through `/proc`.

The new options are diagnostic-only:

```text
-uring-bounded-workers=N
-uring-unbounded-workers=N
```

An absent option (or a negative value) preserves that account's queried value.
Zero is rejected. Either positive option requires both `-backend=uring` and
`-uring-force-async`. Neither option changes normal io_uring behavior when
absent.

## Exact-kernel source boundary

Environment B reports `Linux 7.2.3-1-cachyos`. Local inspection found the
installed `linux-cachyos` and `linux-cachyos-headers` packages and a
`/usr/lib/modules/$(uname -r)/build` header/build tree, but no matching
`/usr/lib/modules/$(uname -r)/source` and no local io_uring implementation
sources such as `io_uring.c` or `io-wq.c`. The available UAPI header and local
`io_uring_register(2)` manual support the registration ABI above; they do not
prove whether this exact CachyOS kernel classifies forced-async regular-file
READ as bounded or unbounded. No upstream source is substituted for that
missing exact-kernel evidence.

## Observer correction

The original 25 ms script sampled the outer `iobaseline` process. That process
is a wrapper which launches a `-child` workload and performs the harness's own
observer; its normal task count is one. It also performed global `/proc`
scans and spawned commands per task, so a sample could consume most of the
run. The corrected script discovers the workload child through the wrapper's
`/proc/<root>/task/<root>/children`, samples that child only, uses shell reads
for `comm`, and treats a TID disappearing between enumeration and read as a
nonfatal race. It writes root/workload PIDs, per-sample TID/comm data, metadata
when available, peak tasks by comm, distinct TIDs by comm, and the explicitly
named `iou-wrk*`/`io_wq*` subset. It does not claim unnamed extra tasks are
io-wq workers.

This observer is diagnostic-only. It must not be used as a throughput run.
For a five-second run, accept the observation only if `summary.txt` shows at
least 100 samples; otherwise report the observation as inadequate.

## Environment B gate

Use the authoritative toolchain and inspect the current revision first:

```sh
cd /operator-home/bench
export GO_TOOL=/operator-home/go-upstream-go1.27.1/bin/go
git rev-parse HEAD
"$GO_TOOL" test -count=1 -buildvcs=false ./...
"$GO_TOOL" test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline

mkdir -p phase6b-gate
"$GO_TOOL" build -buildvcs=false -o phase6b-gate/iobaseline ./cmd/iobaseline
"$GO_TOOL" build -buildvcs=false -o phase6b-gate/phase6check ./cmd/phase6check
```

Before the four diagnostics, a small setup/correctness gate validates each
changed account independently. These runs must succeed with the post-set
verification shown in JSON; an `EPERM`, unsupported registration, or mismatch
is a blocking result, not a fallback condition.

```sh
phase6b-gate/iobaseline -backend=uring -uring-force-async -uring-bounded-workers=1 \
  -operation=readat -file=./data/readat-512MiB.bin -file-access=shared_fd \
  -buffer-bytes=4096 -concurrency=32 -max-outstanding=4 -gomaxprocs=4 \
  -operations=128 -seed=31908 -cache-precondition=prewarm \
  -cache-state=residency_verified_mostly_resident >phase6b-gate/-uring-bounded-workers_1.json
phase6b-gate/iobaseline -backend=uring -uring-force-async -uring-unbounded-workers=1 \
  -operation=readat -file=./data/readat-512MiB.bin -file-access=shared_fd \
  -buffer-bytes=4096 -concurrency=32 -max-outstanding=4 -gomaxprocs=4 \
  -operations=128 -seed=31908 -cache-precondition=prewarm \
  -cache-state=residency_verified_mostly_resident >phase6b-gate/-uring-unbounded-workers_1.json
phase6b-gate/iobaseline -backend=uring -uring-force-async -uring-bounded-workers=1 -uring-unbounded-workers=1 \
  -operation=readat -file=./data/readat-512MiB.bin -file-access=shared_fd \
  -buffer-bytes=4096 -concurrency=32 -max-outstanding=4 -gomaxprocs=4 \
  -operations=128 -seed=31908 -cache-precondition=prewarm \
  -cache-state=residency_verified_mostly_resident >phase6b-gate/-uring-bounded-workers_1_-uring-unbounded-workers_1.json
phase6b-gate/phase6check -file phase6b-gate/-uring-bounded-workers_1.json -expect-bounded-workers=1
phase6b-gate/phase6check -file phase6b-gate/-uring-unbounded-workers_1.json -expect-unbounded-workers=1
phase6b-gate/phase6check -file phase6b-gate/-uring-bounded-workers_1_-uring-unbounded-workers_1.json -expect-bounded-workers=1 -expect-unbounded-workers=1
```

## Four account-classification diagnostics

Do not run another scaling curve. After the gate, run one 3-second resident
throughput observation for each condition below. The harness's normal external
observer provides peak process tasks; use the corrected script separately to
identify task names for any condition whose task population needs attribution.

```sh
common=(
  -backend=uring -uring-force-async -operation=readat
  -file=./data/readat-512MiB.bin -file-access=shared_fd
  -buffer-bytes=1048576 -concurrency=1000 -max-outstanding=1000 -gomaxprocs=4
  -duration=3s -warmup=0s -seed=31908
  -cache-precondition=prewarm -cache-state=residency_verified_mostly_resident
  -runtime-sample-interval=10ms -harness-revision="$(git rev-parse HEAD)"
)
phase6b-gate/iobaseline "${common[@]}" >phase6b-gate/A-default.json
phase6b-gate/iobaseline "${common[@]}" -uring-bounded-workers=1 >phase6b-gate/B-bounded-1.json
phase6b-gate/iobaseline "${common[@]}" -uring-unbounded-workers=1 >phase6b-gate/C-unbounded-1.json
phase6b-gate/iobaseline "${common[@]}" -uring-bounded-workers=1 -uring-unbounded-workers=1 >phase6b-gate/D-both-1.json
phase6b-gate/phase6check -file phase6b-gate/A-default.json
phase6b-gate/phase6check -file phase6b-gate/B-bounded-1.json -expect-bounded-workers=1
phase6b-gate/phase6check -file phase6b-gate/C-unbounded-1.json -expect-unbounded-workers=1
phase6b-gate/phase6check -file phase6b-gate/D-both-1.json -expect-bounded-workers=1 -expect-unbounded-workers=1
```

For task names and TIDs, run the observer separately, for example:

```sh
scripts/phase4-observe-workers.sh uring phase6b-gate/observe-C ./data/readat-512MiB.bin \
  --prewarm --force-async --unbounded-workers=1
scripts/phase4-observe-workers.sh uring phase6b-gate/observe-D ./data/readat-512MiB.bin \
  --prewarm --force-async --bounded-workers=1 --unbounded-workers=1
```

Interpret only the task population and verified configured pair initially.
If both limits are verified as `[1, 1]` while the task population remains near
300, stop: those tasks are not explained by the controlled maxima alone and no
performance tuning follows from this phase.
