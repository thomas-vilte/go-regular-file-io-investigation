# Phase 8: cross-host io_uring mechanism reproduction

This is the current reproduction entry point. Read the [summary](summary.md)
and [evidence review](evidence-review.md) for results and limitations. The
[AWS setup guide](environment/aws-environment-c.md) is optional operational detail;
the [historical notes](history/README.md) explain earlier stages.

The empirical investigation is frozen. These are preserved reproduction
instructions, not a request for more measurements. The
[final pre-upstream controls](../results/pre-upstream-controls/summary.md) are a
separate fixed same-revision admission comparison and completed perf attempt,
not a change to the Phase 8 matrix. Their recorded commands are provenance;
no further profiling or security change is needed before maintainer discussion.

Phase 7 completed the mechanism work on Environment B. It found a broad
resident-read throughput plateau after CPU parallelism was available, while
larger configured unbounded worker maxima continued to increase task
population. Those are findings for one machine, not a runtime policy.

Phase 8 packages a second-host reproduction suite. It does not tune the
backend. Its purpose is to support or falsify the portability of the mechanism
evidence before an upstream `golang/go` discussion.

## Questions

The suite tests whether another Linux host shows these qualitative mechanisms:

1. A slow/nonresident-start, high-concurrency blocking `ReadAt` workload can
   create more process tasks than normal io_uring.
2. Normal io_uring keeps task population bounded at high logical concurrency.
3. With the file explicitly resident, normal io_uring uses less execution
   parallelism than blocking.
4. `IOSQE_ASYNC`, with one locked submitter, can increase execution
   parallelism.
5. The stable-issuer unbounded-worker curve has a broad throughput plateau,
   while task population may continue to grow.

The suite reports raw metrics and does not require Environment B's GiB/s,
worker count, filesystem, or exact knee to recur.

## Second-host preferences

These are preferences rather than prerequisites: a mainstream distribution and
kernel, eight or more logical CPUs, at least 16 GiB RAM, local SSD/NVMe-backed
storage, ext4 or xfs, an io_uring-enabled environment, and no container
security policy that blocks `io_uring_setup`. A VM is acceptable. A different
filesystem is useful evidence; do not compare its absolute storage throughput
directly with Environment B's btrfs/zstd numbers.

## Portable invocation

Run from a clean checkout. The data path must be under the repository's ignored
`data/` directory because the slow part uses the experiment-owned
`POSIX_FADV_DONTNEED` path. If the file does not exist, the runner creates a
deterministic 512 MiB file with `cmd/genfile`; it never overwrites an existing
file. An existing file must be at least 512 MiB and is the caller's declared
experiment-owned input.

```sh
cd /path/to/bench
GO_TOOL=/path/to/go \
  scripts/phase8-cross-host-reproduction.sh \
  phase8-artifacts \
  ./data/readat-512MiB.bin
```

`GO_TOOL` is optional. Without it the runner resolves `go` from `PATH`; it
records the resolved executable and `go version`. It requires Bash, Git,
Python 3 standard library, ordinary Linux user tools, and a working C compiler
with libc development headers for Go's mandatory linux/amd64 race-detector
gate. The production io_uring implementation remains direct Go syscalls and
does not use cgo or liburing. The runner makes no privileged or security-related
changes.

The defaults are `GOMAXPROCS=min(4, online CPUs)` for Part A and
`min(8, online CPUs)` for Part B. Set `PHASE8_GOMAXPROCS=N` to override both,
or `PHASE8_SLOW_GOMAXPROCS=N` and `PHASE8_RESIDENT_GOMAXPROCS=N` independently.
The runner records the selected and requested values in `environment.txt`.

The runner refuses an existing output directory, checks that the Phase 8
runner's introduction commit is an ancestor of current `HEAD`, records the
actual execution revision, and refuses tracked local modifications. Existing
untracked result archives are allowed and are not touched.

## Capability and correctness gate

Before the matrix, the runner captures the host, records `go env CGO_ENABLED`,
`go env CC`, and the resolved compiler, then runs a small cgo `-race`
preflight with `CGO_ENABLED=1`. It fails with an actionable compiler/header
message before the remaining gates if that prerequisite is unavailable. It
then builds the harness with the selected toolchain, runs normal tests, verbose kernel-backed
`internal/uring` tests, an explicitly `CGO_ENABLED=1` race test, and an
`io_uring_setup` probe. It then runs a small real 4 KiB/32-client
io_uring `ReadAt` gate and checks terminal completion, pin, operation-table
and residency invariants. If setup is unavailable, it preserves
`setup-probe.json` with the concrete error and stops. A kernel test skip does
not itself prove availability; the probe and real smoke are the gates.

## Matrix

All canonical processes use random shared-fd `ReadAt`, a 1 MiB buffer, 1000
logical clients, 1000 maximum outstanding operations, five seconds, zero
warmup, seed 31908, and disabled hot-path issuer-TID recording.

Part A uses `GOMAXPROCS=min(4, online CPUs)` and runs three interleaved
repetitions of blocking and normal io_uring after the harness's authorized
`DONTNEED` preparation. It records mincore snapshots before and after the
preparation. A successful `DONTNEED` request is not called a cold-disk read.
If residency does not materially fall, `slow-thread-pressure.csv` preserves
that observation and the intended slow-start precondition is not reproduced.
The summary uses the harness's existing `<= 50,000 ppm` mostly-nonresident
label boundary and records `slow_start_precondition=not_reproduced_all_runs`
when any repetition misses it; it does not call that condition a cold cache.

Part B uses `GOMAXPROCS=min(8, online CPUs)`, prewarms the complete file, and
requires exactly 1,000,000 resident ppm both after prewarm and immediately
before measurement. It runs three interleaved repetitions of:

```text
blocking
normal io_uring
force-async + locked submitter + unbounded U1, U2, U4, U8, U16
force-async + locked submitter + default maxima
```

Explicit U values use the existing query/set/post-set-query verification. The
bounded maximum remains the initially queried value. There is no U32/U64
extension: if U16 remains below default, record that first and choose a later
diagnostic deliberately.

## Acceptance and artifacts

Each accepted process requires `status=ok`, current harness revision, clean
io_uring reconciliation where relevant, and disabled issuer-TID hot-path
recording. Resident runs require both 100% residency snapshots. Explicit U
runs require the verified post-set pair. The runner stops at the first failed
validation and leaves already written raw results intact; it never retries and
replaces a failed run.

```text
phase8-artifacts/
  environment.txt
  capabilities.txt
  dataset.txt
  setup-probe.json
  tests-normal.txt
  tests-kernel.txt
  race-preflight.txt
  race-preflight/{go.mod,cgo.go,cgo_test.go}
  tests-race.txt
  smoke.{json,stderr.txt,check.txt}
  planned-order.txt
  execution-order.txt
  runs/r{1,2,3}-{slow,resident}-{mode}.{json,command.txt,stderr.txt,check.txt}
  runs/r{1,2,3}-slow-{mode}.diskstats-{before,after}.txt
  per-run.csv
  slow-thread-pressure.csv
  resident-curve.csv
```

Raw JSON files are authoritative. `slow-thread-pressure.csv` contains each
repetition and median/min/max rows for throughput, observed residency,
task/runtime peaks, CPU/wall and context switches. `resident-curve.csv`
contains medians and ranges for throughput, effective cores, CPU microseconds
per operation, process/Go task peaks, context switches and enter/op, including
ratios to current-revision blocking and forced-default controls. There is no
composite score.

The lightweight harness `/proc` sampler is the quantitative task-peak source.
The observer commands below document the historical procedure. That host-specific
script is not shipped in this snapshot; see [legacy notes](../scripts/legacy/README.md).
The canonical runner above is supported, and the existing scoped observer results
remain available under [Environment C results](../results/environment-c/phase8/observations/).
After a successful curve, take separate, non-throughput worker identity
observations for U1, U4, U16 and default using the existing observer. Preserve
the sample count; a five-second observation with fewer than 100 samples is
inadequate for quantitative worker identity conclusions.

```sh
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-U1 ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread --unbounded-workers=1
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-U4 ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread --unbounded-workers=4
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-U16 ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread --unbounded-workers=16
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-default ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread
```

## Cross-host factual comparison

### Environment C correctness-gate discovery

On Ubuntu 24.04, Linux 7.0.0-1012-aws, x86-64, eight logical CPUs,
ext4 on local EC2 NVMe and Go 1.27.1, the kernel-backed gate at revision
`9e153d8e2dea763d567ae860904869354cb42e7a` rejected the normal subtest of
`TestBatchedPublicationReturnsCorrectBytes`. All 32 reads completed correctly;
32 operations were created/admitted/queued/published/completed, all 32 pins
were released, and the operation table, remaining pins, unknown IDs and
duplicate CQEs were zero. The failure was the requirement to observe a
submission batch larger than one: this execution produced 32 singleton
submission batches and 32 singleton completion drains.

The submitter drains only operations immediately available in `submitCh`.
Releasing concurrent clients through a start channel does not guarantee that
multiple sends precede that drain. Scheduling and fast read completion can
therefore legitimately produce only singleton batches. Force-async passing
does not make batching a guaranteed property of normal mode.

This was a test portability defect discovered by cross-host reproduction,
not a benchmark result. The integration test now checks read correctness and
accounting without a timing-dependent minimum batch size. Deterministic
publication tests supply a known pending set directly to the existing
publication unit; production submission policy and the Phase 8 matrix are
unchanged. Local sandbox test success does not replace rerunning the
kernel-backed correctness gate on Environment C. Preserve its original
partial artifacts; no Phase 8 performance measurements were authorized as
part of this correction.

The deterministic fixture uses test-owned SQ/CQ memory and an invalid ring
descriptor: `submitBatch` executes its real preparation/publication path,
then enter fails without any kernel ownership of the fixture. Tests verify
a two-entry publication across SQ wraparound, retained queued overflow,
full-SQ handling, published ownership after failure/partial/zero consumption,
and terminal cleanup via synthetic CQEs in reverse order. A separate case
reconciles exactly 32 singleton batches. An invalid second operation must
reject the entire batch before tail publication or outstanding changes.
These are mechanics tests, not successful kernel submissions or data-copy
tests. Real-kernel integration retains exact byte comparisons in both modes.
The single tail store after validating and marking all batch members remains
a source-audited ordering property; tests observing final state alone do not
prove memory ordering against a concurrent kernel.

The existing early-Close test also polled for an active operation, allowing
completion to win before Close was called. It now prepares a table-owned
queued request before enqueueing it, verifies rejection, then sends it to
the real submitter and verifies bytes, drain and successful Close. Published
outstanding rejection is covered by the synthetic batch test. Local Go
1.27.1 validation passed `go test -count=1 -buildvcs=false ./...`,
`go test -count=20 -buildvcs=false ./internal/uring`, and
`go test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline`.
Kernel-backed subtests skipped with setup `EPERM`; race detection does not
model kernel accesses to mapped rings or pinned buffers.

### Environment C race-gate discovery

After the deterministic batching-test correction, the real kernel-backed
`internal/uring` suite passed on Environment C. The next mandatory gate then
stopped before any benchmark with `go: -race requires cgo; enable cgo by
setting CGO_ENABLED=1`. The previous runner did not set `CGO_ENABLED=0`; it
invoked the race test with the ambient Go configuration. That message proves
that cgo was effectively disabled for that invocation, but the preserved log
alone cannot distinguish an explicit `CGO_ENABLED=0` from Go's automatic cgo
disablement when its default C compiler is unavailable. The prior Ubuntu
bootstrap also omitted the `gcc` and `libc6-dev` prerequisite, so it could not
establish a usable race toolchain.

The [Go race-detector requirements](https://go.dev/doc/articles/race_detector#Requirements)
document the cgo and C compiler dependency. Go 1.27.1's
`src/cmd/go/internal/cfg/cfg.go` disables default cgo when it cannot find
the default compiler and `CC` is unset; the quoted error is emitted by
`src/cmd/go/internal/work/init.go` when effective cgo is disabled.

This is a provisioning/tooling portability defect, not an io_uring or
performance result. The runner now records the ambient and effective cgo
configuration, compiler selection and resolved executable; it runs a small
cgo race preflight under `CGO_ENABLED=1` before the other gates, and runs the
mandatory project race suite with that setting scoped to the test command.
The benchmark builds retain their ordinary configuration. Preserve both
failed-gate artifact directories when rerunning on Environment C.

After preserving the Phase 7 artifact directory from Environment B and the
new Phase 8 directory from Environment C, generate a factual Markdown table:

```sh
GO_TOOL=/path/to/go
"$GO_TOOL" run ./cmd/phase8compare \
  -host-b /path/to/phase7-artifacts \
  -host-c /path/to/phase8-artifacts \
  -output phase8-artifacts/environment-b-vs-c.md
```

The helper compares raw resident medians directionally where appropriate. The
provided Phase 7 artifact has no slow/nonresident Part A control, so it marks
that cross-host row `unclear` rather than manufacturing a host-B value. It
does not encode a numerical plateau threshold; the worker throughput and task
curves remain visible for review.
