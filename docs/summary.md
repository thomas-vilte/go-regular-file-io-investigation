# Concurrent regular-file I/O in Go: investigation summary

Next: [evidence review](evidence-review.md), then [reproduction](reproduction.md).

This investigation is ready for a
maintainer discussion about the problem and alternatives, **not** a proposal to
integrate io_uring. The [adversarial review](evidence-review.md)
contains the full evidence audit, historical corrections and limitations.

## Motivation

I investigated how concurrent Linux regular-file reads interact with Go's
scheduler and native-thread population. I compared blocking `os.File.ReadAt`
with an external completion-based prototype, then reproduced selected mechanisms
on a second Linux host. io_uring was an experimental mechanism, not a presumed
solution.

The strongest negative result is important: **blocking remained materially
faster in the resident throughput tests**. On Environment B its median was
14272.53 MiB/s, versus 8684.29 for normal io_uring and roughly 10991–11112 at
selected forced-async plateau points. On C, blocking achieved 41558.01 MiB/s,
versus 6439.20 for normal io_uring and roughly 27053–27432 at the async plateau.
Lower task counts do not by themselves establish better application performance.

The question is whether the observed scheduling/resource costs deserve further
investigation inside Go, or whether simpler application/library-level controls
are the appropriate answer.

## Current Go behavior

For the inspected Go source, the relevant explicit-offset path is approximately:

```
os.File.ReadAt -> internal/poll.FD.Pread -> blocking pread syscall
```

The goroutine enters a syscall; its M executes or blocks there. The P can be
retaken so other Go work can continue, but that does not release the M from the
syscall. Sufficient simultaneous slow syscall occupancy can therefore create
native-thread pressure. This is not one thread per logical request, and it does
not mean all regular-file I/O has a thread problem.

`FD.Pread` uses an fd reference rather than the shared-offset read lock used by
`Read`. The experiments focus on `ReadAt` to avoid conflating that serialization
with the scheduler mechanism. Socket readiness/netpoll is a different path; I
did not test or propose replacing epoll. The authoritative baseline toolchain
was Go 1.27.1, upstream revision
`862c888e612ac346c7c4d99c9392bdfd265f33b0`.

## Experimental prototype

The Linux/amd64 PoC is outside the runtime and supports explicit-offset buffered
reads only. It has one io_uring, one submit goroutine, one completion consumer
and bounded admission. SQE `user_data` carries an operation ID, not a Go pointer.
A strongly reachable operation owns the pinned destination buffer until terminal
completion. Publication/accounting precedes exposing SQ entries to the kernel;
completion checks IDs, state and lifetime reconciliation.

Submission batches only immediately available work, with no artificial delay.
The completion consumer drains available CQEs and blocks when appropriate.
Diagnostic modes add `IOSQE_ASYNC`, worker maxima and a locked submitter. These
are experimental controls, not suggested runtime policies. The single-submitter
architecture, channels, operation allocations and pinning may themselves affect
performance. Preallocated buffers do not mean the complete backend allocates
nothing per operation.

## Environments and method

| | Environment B | Environment C |
| --- | --- | --- |
| OS/kernel | CachyOS, Linux 7.2.3 | Ubuntu 24.04, Linux 7.0.0-1012-aws |
| Machine | x86-64, 4 logical CPUs | AWS c8id.2xlarge, x86-64, 8 logical CPUs / 4 physical cores with SMT |
| Filesystem/storage | btrfs with zstd, local NVMe | ext4, local EC2 instance-store NVMe |
| Go | 1.27.1 | 1.27.1 |

This is directional reproduction across two environments, not controlled CPU
scaling and not proof of general Linux portability. Kernel, CPU generation,
topology, virtualization, filesystem and storage changed together.

The final resident comparisons use a prewarmed 512 MiB file, 1 MiB random
`ReadAt`, a shared descriptor, 1000 closed-loop clients and outstanding limit
1000. Each condition has three five-second runs with no warmup interval beyond
the explicit file prewarm. Both residency snapshots must report 100% resident
source pages. The deterministic seed is 31908. B uses GOMAXPROCS=4; C's resident
part uses 8. Run order is deterministic and interleaved.

This repeatedly reads only 512 aligned locations and is not an application
workload. Destination buffers total about 1 GiB and are not explicitly
prefaulted. The same per-worker seed does not produce identical global request
ordering or operation counts in backends with different completion rates.

## Main observations

### 1. Thread pressure is conditional, but observable

C's nonresident-start comparison has six runs, three per backend. All had zero
resident source pages after experiment-owned `POSIX_FADV_DONTNEED` and immediately
before measurement. Blocking's sampled task peaks were 74–164, median 131;
normal uring's were 8 in each run. Both used GOMAXPROCS=4.

This supports a difference in task populations in runs beginning from verified
nonresident data. It is **not five seconds of sustained cold disk I/O**: the file
becomes resident during execution. The sampler spans the child process lifetime,
so it does not attribute every task or peak to storage blocking. Resident blocking
on C had a median peak of only 37 tasks despite the same logical concurrency.

### 2. Normal uring used little execution parallelism in this prototype

Normal uring repeatedly measured near one process CPU-second per timed
wall-second, while blocking used substantially more. This is consistent with a
serialized issue/execution path, but I could not establish exact inline-kernel
execution as the sole cause. Kernel CPU attribution, harness synchronization and
memory-copy costs remain unresolved.

I attempted a privileged system-wide perf comparison. Userspace callchains were
usable, but with `kptr_restrict=2`, perf could not resolve the kernel frames even
with a matching unstripped vmlinux. Kernel-stack attribution remains inconclusive;
the [scoped perf record](../results/pre-upstream-controls/perf/README.md) preserves
the attempt and its limitation.

### 3. Forced async recovered parallelism, with a worker-population cost

`IOSQE_ASYNC` increased CPU parallelism and throughput when sufficient worker
capacity was available, but default worker populations could become large.
Separate task-identity observations showed matching submit-TID/worker-owner sets
under submitter migration, and one relevant owner with the submitter locked.
Locking stabilizes the topology for a diagnostic; it is not a runtime recommendation.

### 4. Throughput plateaued before task population stopped growing

With forced async and a locked submitter, I varied the empirically relevant
unbounded worker maximum, preserving the bounded default and verifying explicit
settings by a post-set query. U means that configured maximum, not an observed
process-wide worker count. Hot-path TID instrumentation was disabled.

| Resident condition | Median MiB/s | CPU/wall approximation | Median sampled peak process tasks |
| --- | ---: | ---: | ---: |
| B blocking | 14272.53 | 3.476 | 22 |
| B normal uring | 8684.29 | 1.034 | 8 |
| B async U4, representative plateau point | 10991.04 | 3.641 | 14 |
| C blocking | 41558.01 | 7.815 | 37 |
| C normal uring | 6439.20 | 1.075 | 12 |
| C async U8 | 27092.26 | 7.989 | 20 |
| C async default | 27052.83 | 7.987 | 169 |

CPU/wall approximates process CPU seconds divided by timed wall seconds. Rusage
includes some setup/teardown outside the wall interval; it is not exact core
utilization or a complete attribution of system-wide worker CPU. Sampled peaks
are not exact worker counts, and independent metric medians need not occur in
the same run.

B had a broad plateau around U4 and above; including U32/U64, the plateau-point
medians spanned approximately 10811–11112 MiB/s. Tasks grew from 14 at U4 to 205
at default. C plateaued around U8: U8/U16/default throughput ranges overlap,
while median tasks increased from 20 to 31 to 169. There is no justification for
selecting a winner from small percentage differences across three repetitions.

The results are consistent with useful execution parallelism approaching
available execution capacity. They do not prove CPU-count causation: SMT,
memory/cache behavior, filesystem and kernel differences could also shape the
curve. They do not imply workers should equal GOMAXPROCS. Separate C worker
identity diagnostics used GOMAXPROCS=4, not the canonical resident value of 8;
the default heavy observation had insufficient samples for a worker census.

## Negative results and discarded hypotheses

Reducing enter calls per operation by more than two orders of magnitude did not
materially recover the earlier throughput deficit. The hypothesis that syscall
frequency was its dominant cause was weakened, not converted into an optimization
success. That was a historical sequential comparison, not a simultaneous trial.

I discarded an early bounded-worker experiment as a scaling curve: its requested
variable did not control the relevant population. Later post-set verification and
independent account controls established the relevant account for the tested path,
not a universal classification of Linux regular-file reads.

C exposed a scheduler-dependent batching assertion and then an undeclared race
toolchain prerequisite before performance measurements. Both were corrected
without forcing production batching or changing the workload. Earlier lifetime
and observer defects, invalid fields and failed-gate artifacts remain documented
in the detailed review. Tests and reconciliation improve confidence but do not
prove kernel memory accesses safe merely because `-race` passes.

Most importantly, even the async plateau remained substantially below blocking
resident throughput on both hosts. I could not establish a general speed,
resource-efficiency or application-level advantage for io_uring.

## Bounded-blocking alternative

The [final same-revision controls](../results/pre-upstream-controls/summary.md)
make admission control a strong simpler alternative. On B, limiting blocking to
16–64 admitted operations retained roughly 76–83% of unrestricted throughput
while reducing median sampled process-task peaks from about 1000 to 21–69.
All 24 runs started with zero resident source pages in both snapshots. They
were nonresident-start runs, not sustained cold I/O. The curve was not monotonic:
256 permits added tasks without improving median throughput over 64.

Async locked U4 did not demonstrate a clear practical advantage over bounded
blocking: B16 and U4 had similar throughput ranges; U4 had fewer sampled tasks
but materially more context switches and no demonstrated CPU/op advantage.
No application objective chooses a winner. These controls collected no
queue-inclusive client latency; older Phase 2 latency is separate evidence.

Default forced async reached **989, 1011 and 1014 sampled process tasks even with
a locked submitter**, while Go-owned peaks were 10–14. Reducing Go-owned threads
does not automatically reduce total native tasks. Independent sampled maxima
cannot be subtracted to count exact workers.

I have **not demonstrated a realistic application-level advantage over
well-chosen bounded blocking**. Lower task counts alone do not justify permanent
runtime complexity.

## What this does not establish

There is no complete runtime design, application-level win, general io_uring
speed advantage, universal Linux worker policy, or evidence that completion I/O
belongs in the runtime rather than a library/application. Cancellation, deadlines,
transparent Close with pending operations, fd reuse/stale completions, fork/exec,
GC integration, fairness, fallback and kernel/security compatibility are unresolved
design gates. Writes, fsync and netpoll comparisons were not evaluated.

A restricted environment returned setup EPERM despite an enabled io_uring sysctl.
Capability must be tested, not inferred from kernel version. The correct response
is not to weaken security policy. These limitations block an integration
recommendation, but need not block asking maintainers whether the problem merits
further work.

## Questions for Go maintainers

1. Is native-thread pressure under highly concurrent regular-file I/O a runtime
   problem worth addressing, or is application/library admission control preferred?
2. If worth exploring, which alternatives should be compared before proposing a
   completion-based runtime design?
3. Would application-level evidence comparing unrestricted blocking, bounded
   blocking and completion I/O be the appropriate next prerequisite for integration
   discussion?
4. Which semantic/runtime constraints should be treated as design gates?
5. Is #31908 the appropriate place to continue, or would a narrower discussion
   be preferable later?

## Reproduction / artifact pointers

Source location: `https://github.com/thomas-vilte/go-regular-file-io-investigation`.
Reviewed results: `https://github.com/thomas-vilte/go-regular-file-io-investigation/tree/main/results`.

The [Phase 8 runbook](reproduction.md) documents the
portable runner, prerequisites, gates and complete matrix. No new measurements
were run to prepare this summary. For a future independent reproduction, after
choosing an authorized host and a clean checkout containing the portability fixes:

```bash
GO_TOOL=/path/to/go1.27.1/bin/go \
  scripts/phase8-cross-host-reproduction.sh \
  phase8-artifacts \
  ./data/readat-512MiB.bin
```

The output directory must not already exist. The data path is experiment-owned;
the runner can generate the missing file, preserves raw records, and stops on
gate/acceptance failures. Do not discard failures or replace individual results.

B's Phase 7 raw execution revision is
`f895b400d7a8ab200d9dfbb01dbd777fd44bd00e`; C's Phase 8 revision is
`3130f513681116d00d61aa4dc68e7a1bf560970b`. The latter includes test/provisioning
fixes after the Phase 8 implementation ancestor. Publish raw repetitions, order,
environment and gate evidence alongside summaries, with any metadata redactions
declared. [Provenance](provenance.md) records source revisions and historical
limitations; the [redaction log](../results/REDACTIONS.md) describes metadata
derivatives without changing the original experiments.
