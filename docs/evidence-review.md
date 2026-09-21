# Red-team review: evidence for a Go regular-file I/O discussion

Start with the [summary](summary.md); continue to [reproduction](reproduction.md)
or the [historical notes](history/README.md).

## Executive conclusion

Review baseline: repository HEAD `3130f513681116d00d61aa4dc68e7a1bf560970b`.
Final addendum: same-revision controls at
`ec82557a9e295fcff895234212e76ad01b8cec19`, plus the completed perf attempt.
Historical findings below retain their original execution provenance.
This is a documentation-only adversarial audit, not a public upstream comment,
integration proposal, benchmark run, or new experimental phase. Raw JSON takes
precedence over prose and generated CSV. All throughput below is **MiB/s** unless
explicitly converted; GiB/s = MiB/s / 1024.

The evidence warrants a narrowly framed discussion of the scheduling/resource
cost of concurrent regular-file I/O and possible alternatives. It does **not**
warrant recommending io_uring integration. Completion-based reads in this
prototype can keep thread populations small, but the resident throughput tests
strongly favor blocking reads. The simpler bounded-blocking alternative remains
unbeaten at the application level.

The most defensible contribution is mechanism discovery, including negative
results: syscall batching did not recover the throughput deficit; a bounded-worker
curve did not control the relevant population; issuer migration multiplied
observed worker-owner contexts; stable issuer experiments separated useful worker
parallelism from excess task population. None establishes a runtime policy.

## Discussion readiness verdict

**Ready for discussion: yes. Ready to propose integration: no.** A maintainer
conversation can productively decide whether this resource-pressure problem is
important enough to pursue, and which alternatives matter, without first proving
that io_uring wins. Presenting this as a performance improvement or a nearly
finished backend would misrepresent the evidence.

The strongest objection is not a missing small benchmark. It is that bounded
blocking already addresses much of the thread problem with far less machinery,
while no realistic application demonstrates a net advantage from the prototype.
A maintainer could reasonably conclude that library-level admission control is
sufficient and decline further runtime work. That is an acceptable outcome.

## What was investigated

The external Linux/amd64 prototype implements explicit-offset buffered reads,
one ring, one submit goroutine and one completion consumer. Later diagnostic
variables were forced async, worker maxima and submitter thread locking. It is
not an os.File replacement. Destination read buffers are preallocated per logical client;
operation bookkeeping/pinning still has costs. No runtime or netpoll integration
was evaluated.

Later canonical runs use a 512 MiB file, 1 MiB requests, 1000 closed-loop logical
clients, outstanding limit 1000, shared descriptor, deterministic per-worker
offset streams, five-second runs, and three repetitions. There are only 512
aligned request locations. These are useful controlled mechanisms, not a diverse
file-I/O workload or application trace.

Environment B: Linux 7.2.3 CachyOS, four logical CPUs, x86-64, btrfs/zstd, local
NVMe. Environment C: Ubuntu 24.04, Linux 7.0.0-1012-aws, EC2 c8id.2xlarge,
eight logical CPUs/four physical cores with SMT, ext4 on local instance-store
NVMe. Both use Go 1.27.1. CPU generation, topology, virtualization, filesystem,
kernel and storage changed together. This is not a controlled CPU-count experiment.

## Current Go regular-file I/O problem statement

The local upstream Go source supports the following model:

```
os.File.ReadAt -> pread -> internal/poll.FD.Pread -> syscall.Pread
                  G enters syscall; its M executes/blocks in that syscall
                  P can be retaken; other work may reuse or require another M

prototype ReadAt -> admission -> owned/pinned operation -> SQ publication
                  caller waits; CQ consumer resolves operation -> caller resumes
```

In `src/internal/poll/fd_unix.go`, `FD.Pread` takes an fd reference, not the shared
offset read lock, and uses the pread syscall without a poll wait. `FD.Read` has
different locking/offset semantics. `src/os/file.go` implements ReadAt validation
and retry semantics around partial reads. Regular files generally cannot use
epoll readiness as sockets do; the Linux runtime scheduler can retake a P from
a syscall without unblocking the M. Sysmon's documented timing is not a fixed
20-microsecond switch or a promise to allocate one M per request.

Thus high simultaneous slow syscall occupancy *can* require many native threads
to sustain other runnable work. It does not imply every file call is slow, every
goroutine gets its own thread, or thread growth is itself harmful. Hot resident
blocking reads on C achieved 41558 MiB/s with a median sampled peak of 37 tasks.
That is direct negative evidence against an unconditional thread-pathology story.

The Phase 0 narrative inspected a distro source tree without a Git SHA. The
authoritative baseline subsequently used upstream Go revision
`862c888e612ac346c7c4d99c9392bdfd265f33b0`. Do not silently give the initial
source archaeology the provenance of the later toolchain build.

## Evidence chain Phase 0–8

| Stage | Observation and adversarial interpretation |
| --- | --- |
| 0 | Reconstructed syscall/M/P behavior and Windows IOCP lifetime mechanisms. Windows is a useful reference, not proof of portable offset, cancellation or ownership semantics. |
| 1 | Thread-pressure signal under highly concurrent regular-file reads; early raw results lack a per-run harness SHA. Establishes a lead, not a complete reproducibility record. |
| 1.5 | Residency measurements, admission limits, read-lock controls and smaller buffers narrowed explanations. Mostly one repetition per point; latency excludes semaphore waiting. |
| 2 | Environment A returned setup EPERM under security policy. Environment B ran external completion reads. Fewer threads but materially worse throughput; sustained execution exposed a real Pinner ownership bug, subsequently fixed. |
| 3 | Median enter/op fell to about 0.0052, while throughput changed from 5591 to 5609 MiB/s. Dominant enter-frequency explanation is weakened substantially. This was a sequential historical comparison, not simultaneous randomized runs. |
| 4 | Normal uring used about one process CPU-second per wall-second; blocking used more. Only about one file's physical reads occurred while logical traffic was tens of GiB. Attribution did not obtain reliable kernel stack evidence for the dominant system CPU. |
| 5 | On resident data, forced async increased CPU parallelism and throughput, but native tasks proliferated and batching deteriorated. More enter calls accompanied more throughput: another objection to enter-count-first optimization. |
| 6 | Requested bounded maxima 1–128 left task peaks around 285–321. This is a failed control-variable experiment, not a worker scaling curve. |
| 6B | Post-set queries verified values. Changing unbounded to 1 collapsed tasks; bounded=1 did not. Each canonical condition had one run. Classification is empirical for this path, not all regular-file reads on Linux. |
| 6C | Unlocked submit TID set matched worker-owner suffix set; locking gave one submit TID and matching owner. U1 locked reduced useful parallelism/throughput. Default locked did not show the same loss. Historical wait-TID field was wrong. |
| 7 | With stable issuer and hot-path gettid disabled, useful throughput plateaued broadly around U4 on B while tasks continued growing. No fixed-worker policy follows. |
| 8 | C reproduced low normal-uring CPU parallelism, forced-async scaling and task growth beyond a plateau near U8. Blocking remained substantially faster. Gates exposed test/provisioning portability defects before measurements. |

The Phase 3 throughput repetitions were 5608.89, 5636.84 and 5414.81 MiB/s.
The approximate flat median is meaningful against a roughly 99.7% enter/op
reduction, but does not prove all submission costs are negligible. Batching can
change other costs simultaneously, and no profile decomposes every component.

## Cross-host reproduction

The following are recomputed from canonical raw JSON, not copied from narrative
GiB/s shorthand. CPU/wall is a process-accounting approximation with unequal
measurement windows described below. Task values are medians of sampled peaks,
not worker counts. Ranges are observed minima/maxima of three runs, not intervals
of statistical confidence.

| Host / resident condition | MiB/s median | MiB/s min–max | CPU/wall median | Peak tasks median |
| --- | ---: | ---: | ---: | ---: |
| B blocking | 14272.53 | 14100.1–14411.9 | 3.476 | 22 |
| B normal uring | 8684.29 | 7093.5–9144.1 | 1.034 | 8 |
| B U1 | 8899.88 | 8795.6–8934.4 | 1.271 | 10 |
| B U2 | 10427.74 | 8354.9–10676.2 | 2.323 | 12 |
| B U4 | 10991.04 | 10980.5–11024.2 | 3.641 | 14 |
| B U8 | 11073.61 | 10743.2–11089.4 | 3.549 | 19 |
| B U16 | 11069.63 | 9690.7–11117.2 | 3.601 | 28 |
| B default async | 11111.77 | 10940.7–11131.0 | 3.553 | 205 |
| C blocking | 41558.01 | 41521.0–42119.6 | 7.815 | 37 |
| C normal uring | 6439.20 | 6362.4–6550.7 | 1.075 | 12 |
| C U1 | 6276.94 | 6144.2–6374.6 | 1.314 | 12 |
| C U2 | 12124.09 | 12090.0–12385.2 | 2.563 | 13 |
| C U4 | 22003.45 | 21338.9–22949.1 | 4.939 | 16 |
| C U8 | 27092.26 | 26729.2–27135.5 | 7.989 | 20 |
| C U16 | 27431.58 | 26507.5–27590.7 | 7.986 | 31 |
| C default async | 27052.83 | 26280.3–27574.4 | 7.987 | 169 |

U denotes the configured unbounded maximum, forced async, locked submitter, and
preserved bounded default. B's queried defaults were [16,63206]; C's were
[32,56743]. Configured limits are not process-wide observed worker counts.

C's fastest listed uring median is only about 66% of blocking throughput; normal
uring is about 15.5%. Blocking is not a marginal winner. These results cannot
support a general speed claim or hide behind the thread advantage. U8/U16/default
are a broad plateau with overlapping ranges, not evidence that U16 wins.

The curve is consistent with useful parallel execution approaching available
capacity. Competing explanations include memory-copy/cache saturation, SMT
sharing, worker/harness synchronization and kernel-specific execution behavior.
The changed knee does not isolate CPU count causally. C's exposed L3 cache is
480 MiB, near the 512 MiB source dataset; virtualized topology reporting is not
proof of exclusive cache capacity, but highlights memory-hierarchy sensitivity.

### Nonresident-start reproduction

| C condition | MiB/s median (range) | CPU/wall | Individual sampled task peaks |
| --- | ---: | ---: | --- |
| Blocking | 39441.57 (38038.3–40076.9) | 7.019 | 131, 164, 74 |
| Normal uring | 6397.27 (6354.7–6450.6) | 1.066 | 8, 8, 8 |

All six runs had zero resident pages both after DONTNEED and immediately before
measurement. This supports: **runs beginning from a verified nonresident file
exhibited different sampled process task populations**. It does not directly
timestamp those peaks to page faults, establish that every added task blocked on
storage, or make the five-second workload sustained cold I/O. The source rapidly
becomes resident. External sampling also spans setup/teardown. Part A uses P=4,
resident Part B P=8, so their differences cannot be assigned solely to residency.
Blocking syscalls can execute kernel work on more CPUs than the number of Ps;
CPU/wall above four in Part A is not by itself invalid.

### Topology observations and acceptance audit

C U1/U4/U16 diagnostic observations had respectively 187/172/110 samples and
1/4/16 identified iou-wrk tasks, each with one owner suffix matching one submit
TID. These are separate P=4 diagnostics, not P=8 canonical repetitions. Default
had six heavy samples and 47 vanished-TID reads: insufficient for quantitative
worker census. Lightweight canonical task peaks remain the population evidence.

All 30 Phase 8 canonical JSON records are status=ok at the recorded revision.
All 24 resident records satisfy both 1000000-ppm checks. All uring records have
published/terminal/consumed reconciliation, balanced pins, zero remaining table,
pins/outstanding and unknown/duplicate completions. Explicit worker post-set
pairs match requests; hot-path TID recording is false. Both Phase 8 archives
contain byte-identical canonical JSON; the complete archive adds observations.
These checks establish tested lifecycle/accounting, not universal buffer safety
or byte-by-byte verification of every benchmark read.

## Claim-by-claim support table

Each classification applies to the claim as worded, with its stated scope.

| Claim | Classification | Evidence and limitation |
| --- | --- | --- |
| A. Blocking file I/O can translate concurrency into native-thread pressure | STRONGLY SUPPORTED | Source path, B concurrency/admission experiments, C nonresident-start task peaks. Existential claim; not one thread per logical request universally. |
| B. Completion I/O can decouple logical concurrency from Go/native threads | SUPPORTED WITH CAVEATS | Normal ring has small populations at 1000 clients on both hosts, but lower throughput; forced async defaults can replace Go threads with hundreds of native workers. |
| C. Normal buffered uring in this prototype often uses little available CPU parallelism | STRONGLY SUPPORTED | Repeated near-one CPU/wall versus substantially larger blocking/async values. Approximate process windows, not direct per-core tracing. |
| D. That behavior is specifically caused by inline execution in the submitter | SUGGESTIVE ONLY | Forced-async intervention and one-submitter architecture fit this explanation. No exact kernel stacks/time attribution exclude harness serialization or other issue-path work. |
| E. IOSQE_ASYNC can increase execution parallelism here | STRONGLY SUPPORTED | Large repeated process CPU and throughput increases under default/sufficient workers; not guaranteed for U1 or all filesystems. |
| F. Stable-issuer useful parallelism approaches CPU capacity then plateaus | SUPPORTED WITH CAVEATS | B/C curves and CPU observations agree qualitatively. Memory hierarchy, SMT and multiple host changes prevent CPU-count causation or universal knee. |
| G. Tasks can keep increasing after throughput plateaus | STRONGLY SUPPORTED | B U4–default and C U8–default show large population changes without corresponding throughput gains. Sampled process tasks, not exact pool occupancy. |
| H. Submitter migration can multiply relevant io-wq/task contexts on tested path | SUPPORTED WITH CAVEATS | B submit/owner set equality and lock intervention are strong topology evidence. Exact kernel object lifetime was not traced; gettid is adjacent to, not atomic with, enter. |
| I. Locking removes that multiplication on tested path | SUPPORTED WITH CAVEATS | One observed relevant submit owner under locking, including C identity diagnostics. Not proof that all possible ring/task contexts become globally singular. |
| J. Mechanisms reproduce enough to justify upstream investigation | SUPPORTED WITH CAVEATS | Two-host directional reproduction supports asking maintainers about problem relevance and alternatives, not population-wide Linux generalization. |
| K. io_uring improves Go file I/O overall | UNSUPPORTED / DO NOT STATE | Resident blocking materially wins; no overall workload/resource/application utility evidence. |
| L. io_uring should replace epoll/netpoll | UNSUPPORTED / DO NOT STATE | No network comparison or netpoll integration design. |
| M. Go should use IOSQE_ASYNC | UNSUPPORTED / DO NOT STATE | Diagnostic intervention with CPU/task costs, not validated policy. |
| N. Go should permanently lock one submitter | UNSUPPORTED / DO NOT STATE | Topology control, not evaluated scheduler architecture. |
| O. Go should derive worker maxima from GOMAXPROCS | UNSUPPORTED / DO NOT STATE | Per-context/kernel account details, CPU quota/topology and mixed workload requirements unresolved. |
| P. PoC is ready for runtime integration | UNSUPPORTED / DO NOT STATE | Narrow read-only lifetime experiment lacks major API/runtime semantics. |
| Q. Evidence proves Go needs io_uring | UNSUPPORTED / DO NOT STATE | Simpler competitor remains viable; resource-pressure importance is not established for representative applications. |

## Bounded-blocking alternative

The final [same-revision controls](../results/pre-upstream-controls/summary.md)
supersede reliance on older revisions for this comparison. All 24 nonresident-
start records have zero source residency after DONTNEED and immediately before
measurement, clean accounting and execution SHA
`ec82557a9e295fcff895234212e76ad01b8cec19`. B uses Linux 7.2.3-1-cachyos,
Intel i3-9100F, four logical CPUs, about 15.55 GiB RAM, btrfs/zstd and local NVMe,
with Go 1.27.1. The input becomes resident during execution.

| Final control | Median MiB/s (min–max) | CPU/wall | Median sampled process tasks |
| --- | ---: | ---: | ---: |
| Blocking o4 | 6998 (6029–8733) | 1.814 | 10 |
| Blocking o16 | 7616 (7598–7851) | 2.166 | 21 |
| Blocking o64 | 8306 (7880–8464) | 2.471 | 69 |
| Blocking o256 | 7914 (6651–8730) | 2.266 | 262 |
| Blocking o1000 | 10056 (8686–10558) | 2.392 | 1005 |
| Normal uring o1000 | 5536 (5384–6037) | 0.952 | 8 |
| Async locked U4 o1000 | 7423 (7410–8402) | 2.244 | 15 |
| Async locked default o1000 | 8485 (8365–9650) | 2.537 | 1011 |

Admission-limited blocking is an even stronger simpler competitor: o16–o64
retained roughly 76–83% of unrestricted throughput, with 21–69 rather than
about 1000 sampled tasks. This nonmonotonic curve does not select an optimum.
B16 and async U4 had similar throughput ranges. U4 had fewer tasks but more
context switches and no CPU/op advantage demonstrated here. Default async had
989/1011/1014 process-task peaks despite a locked submitter, versus 10/11/14
Go-owned peaks. These independent maxima are not an exact worker census.
Completion I/O is not by itself a solution to total native-task proliferation.

No queue-inclusive client latency was collected in the final control. The
following earlier results remain historical evidence with different revisions
and methods, not latency measurements for the new matrix. There is still no
application-level advantage over bounded blocking. Discussion readiness remains
YES; readiness to propose integration remains NO, with a weaker integration case.

This is the principal challenge to the project, not an appendix. Phase 1.5's
P4 v3 curve showed o4 = 7999 MiB/s / 10 tasks, o64 = 7892 / 69, o1000 = 10828 /
1005. The high-outstanding point bought throughput at enormous population cost,
but the low limit retained substantial throughput. One repetition per point
does not locate an optimum. Smaller buffers also reached large task populations,
weakening a solely 1 GiB-buffer explanation.

Phase 1.5's reported 5.86-ms versus 613.7-ms service p99 excluded semaphore
waiting. Those numbers cannot establish better client tails. Phase 2 corrected
this and separately measured queue-inclusive latency. Its same-revision B data:

| Backend / limit | Throughput median MiB/s | Service p99 median ms | End-to-end p99 median ms (range) |
| --- | ---: | ---: | ---: |
| Blocking / 4 | 7058.84 | 6.65 | 567.03 (545.69–615.79) |
| Blocking / 64 | 7089.72 | 82.97 | 480.58 (478.55–708.67) |
| Blocking / 1000 | 8059.02 | 737.48 | 737.49 (650.63–751.22) |
| Uring v0 / 1000 | 5591.26 | 466.12 | 466.12 (454.33–466.73) |

Throughput and latency are separate runs, not jointly achieved values. In
particular, bounded blocking o64 was faster than uring o1000, and its latency
range does not establish a decisive client-tail disadvantage. Later phases
do not repeat bounded blocking on C or demonstrate application outcomes.

Completion I/O uniquely demonstrates that many admitted requests need not occupy
many Go syscall threads. A semaphore instead reduces admitted requests and queues
callers. Whether preserving admission concurrency has useful application value
is unresolved. Native workers, CPU, memory, queue time and fairness must count,
not just Go thread numbers. An eventual representative comparison of unrestricted
blocking, bounded blocking and completion I/O would be valuable **before an
integration recommendation**, but is not required to ask maintainers whether
that question is worth pursuing.

## Negative results / falsified hypotheses

- Normal uring is materially slower than blocking in both resident host controls;
  even worker-controlled async does not close the resident gap on C.
- Enter/op optimization did not materially improve Phase 3 throughput. It cannot
  remain the favored explanation for the old gap without new evidence.
- Phase 6 bounded settings failed as a population control. Successful registration
  was not evidence that the manipulated account controlled these tasks.
- Default forced async creates many tasks unnecessarily at the observed plateau.
  Reduced Go-owned threads alone hides that cost.
- U1 locked can suppress execution capacity and lose throughput. Stabilizing
  topology is not a free performance improvement.
- C's resident blocking population is modest even at 1000 clients. High logical
  concurrency alone does not create the earlier thousand-thread regime.
- Runnable peaks varied widely while Phase 4 throughput barely changed. This
  weakens a simple burst-size-dominates-throughput story; it does not exclude
  tail/fairness effects that were not temporally measured.
- Restricted Environment A returned EPERM despite io_uring_disabled=0. Transparent
  availability cannot be inferred from kernel version or that sysctl alone.

## Methodological mistakes and corrections

These corrections both increase confidence in particular checked properties and
reveal fragile observability/test assumptions. Passing later gates does not
retroactively validate earlier invalid fields or establish complete correctness.

| Discovery | Consequence / what can still be used |
| --- | --- |
| Sustained harness exposed leaked Pinner ownership | Real lifecycle defect, fixed with persistent ownership before publication. Small tests had missed it. A serious warning against extrapolating a working read path to runtime readiness. |
| Setter output represents previous maxima; no post-set verification initially | Retain historical values as previous values, not effective settings. Later query/set/query verifies configuration; Phase 6 is not a valid active-worker curve. |
| Heavy observer initially sampled wrapper/wrong process | Early absence of worker names is not evidence of no workers. Later child/TID identity diagnostics supersede this inference. |
| Phase 6C wait-TID collection instrumented wrong enter path | Historical wait sets cannot locate CQ context. Submit/owner correlation is independent; do not rehabilitate the bad field. |
| Real C normal test formed 32 singleton batches | Correct reads and balanced lifetime were valid. A scheduling-dependent >1-batch assertion was a test portability defect, not an I/O failure. Deterministic mechanics now test batching; production was not changed to force it. |
| Race gate lacked explicit cgo/compiler prerequisite | C kernel tests passed, then race provisioning failed. This is tooling portability, not performance. Production direct syscalls do not require liburing/cgo; the race gate does. |
| Shell/revision/observer fixes changed execution SHAs | Use artifact SHA, not earlier announced implementation SHA. Preserve ancestry and failed attempts. |

Normal/race test passes cover userspace checks; the Go race detector does not
model kernel accesses through pinned addresses or mmap rings. Deterministic
publication tests, accounting tests and known-data kernel tests are complementary,
not a proof that every asynchronous lifetime/race is handled.

### Unit and documentation discrepancies

Historical decimal rescaling was sometimes labeled GiB/s. For example Phase 4's
10568.70/6462.58 MiB/s are **10.321/6.311 GiB/s**, not 10.6/6.5 GiB/s. Phase 7's
8899.88/10991.04/14272.53 MiB/s are **8.69/10.73/13.94 GiB/s**, not
8.90/10.99/14.27 GiB/s. Raw results win; this review does not propagate the labels.

Several requested early documents are absent from the Git repository and were
found under `/operator-home/docs` instead (see map). They are historical local
sources, not current tracked documentation. Phase 2's early report stops at A's
EPERM; the later B archive contains real results. Its old validation narrative
places accounting after enter, superseded by the ownership fix. Phase 3's old
batching-test description is superseded by the C test fix. Phase 4's report has
pending tables despite available raw artifacts. These are provenance/staleness
issues, not licenses to silently fill historical documents with later conclusions.

Requested `docs/io_uring.md` is absent. The located `/operator-home/zonda/io_uring.md`
is a separate project's prescriptive integration roadmap. Its per-P/epoll and
purported maintainer-requirement statements have no evidentiary authority here.

The harness's generic JSON limitation text says no per-I/O allocation, but uring
ReadAt creates operation/waiter bookkeeping and pins on each call. Preallocated
data buffers do not justify a zero-allocation claim for the whole backend.

## Confounder audit

M = mechanism interpretation; P = absolute performance/resource comparison.

| Confounder | Threat | Assessment |
| --- | --- | --- |
| Two hosts only | M/P | Directional replication, not representative Linux sampling or universal portability. |
| VM, SMT, CPU generation and cache hierarchy | M/P | Logical CPUs are not independent equal physical cores. C's exposed cache is unusually large relative to input. |
| Kernels and no exact CachyOS source audit | M/P | Account/issue behavior may be version/vendor specific; upstream source is a model, not exact-host proof. |
| btrfs/zstd versus ext4 | M/P | Compressed storage/page population and filesystem work differ. Resident results reduce but do not eliminate filesystem-path effects. |
| NVMe devices, EC2 storage, readahead | M/P | Start-up latency and physical traffic differ; logical bytes are not device bytes. |
| 512 MiB repeated source, page cache | M/P | Five-second traffic overwhelmingly reuses pages; cannot extrapolate to streaming/cold datasets. |
| mincore snapshots | M/P | Point-in-time source page residency, not sustained state, device-cache state, absence of contention, or fault-free destinations. |
| 1000 x 1 MiB destination buffers | M/P | Allocation precedes timing but pages are not explicitly prefaulted. Memory bandwidth, RSS and first-touch faults remain. |
| 1 MiB aligned random ReadAt/shared fd | M/P | Large copies over 512 slots; tiny I/O, sequential offsets, writes and diverse file sets untested in final curve. |
| 1000 clients/outstanding | M/P | Artificial saturation; not offered-load application behavior or resource demand distribution. |
| Closed-loop deterministic per-worker streams | M/P | Same seed is not identical global interleaving or total operations across faster/slower backends. |
| Five-second windows, three repetitions | P/M | Broad effects/ranges only; startup/drain, host drift and isolated outliers matter. No tiny-delta significance claims. |
| CPU/rusage window mismatch | M/P | Numerator includes setup/allocation/teardown beyond timed wall. Approximate execution capacity, not exact utilization. |
| Process versus kernel/global CPU | M/P | Process accounting cannot locate time or assume every workqueue CPU is charged here. Total system efficiency unresolved. |
| Sampled process peaks | M/P | 10ms nominal whole-child sampling undercounts short peaks and lacks phase attribution. |
| Runtime versus external samples | M/P | Different windows and peak times; subtracting independent maxima cannot count non-Go workers. |
| Heavy observer cadence/disappearance | M/P | Identity evidence only where sampled; default insufficient. Do not substitute its throughput or peaks for canonical measurements. |
| Different GOMAXPROCS | M/P | B resident4, C resident8; C diagnostics4 and slow4. No clean cross-host/within-C residency-only intervention. |
| One submitter/one CQ consumer | M/P | Harness-specific serialization/synchronization may dominate; not an intrinsic io_uring limit. |
| No real application workload | M/P | No demonstrated SLO, mixed-CPU fairness, memory-pressure or operational advantage from lower threads. |

### Measurement-window details from the current harness

`cmd/iobaseline/main.go` takes RusageBefore after cache preparation but before
ring setup, buffer allocation and worker startup. The timed elapsed interval
starts later and ends after workload drain but before ring Close. RusageEnd is
later, after cleanup/residency inspection. CPU/op and context-switch deltas share
this broader numerator window; CPU/wall near eight is not precise proof of eight
fully occupied cores during just the timed interval.

The external wrapper samples the entire child's life, including prewarm/setup.
The internal runtime samples cover a different interval through drain/close.
Consequently, the observed native/Go peak difference is suggestive of extra
tasks but cannot identify them; comm/TID/Tgid/cgroup diagnostics supply identity.

Service latency starts after admission but includes backend bookkeeping,
submission, kernel work, completion notification and scheduling to return. It
is not kernel-only latency. End-to-end starts inside the closed-loop worker,
not when an externally offered request first wants CPU. Fixed-operation latency
runs and five-second throughput runs weight initial population differently.

## Alternative explanations

| Explanation | Existing evidence | Assessment |
| --- | --- | --- |
| Enter syscall frequency dominates old uring deficit | WEAKENED | Huge frequency reduction with little throughput change; async increases throughput despite more enters. Not every submission cost excluded. |
| Sustained device bandwidth explains resident ceiling | MOSTLY RULED OUT | Explicit resident source and repeated logical traffic; does not eliminate memory copies/filesystem CPU or initial nonresident work. |
| btrfs compression alone creates low normal-mode parallelism | WEAKENED | Same qualitative effect on ext4 C, but absolute B/C gaps remain filesystem-sensitive. |
| Page-cache/memory bandwidth and buffer-copy cost | UNRESOLVED | Large copies, large destinations, cache hierarchy and CPU saturation plausibly shape plateau and residual blocking advantage. |
| Single submitter / synchronous issue path | UNRESOLVED | Fits intervention strongly; no precise kernel execution attribution separates inline reads from other serialized issue work. |
| Go scheduler/channel/operation ownership overhead | UNRESOLVED | Different user-space backend machinery; kernel/system CPU predominance weakens an exclusively userspace CPU explanation, not scheduling bottlenecks. |
| SMT determines curve shape | UNRESOLVED | C has four physical/eight logical CPUs; no within-host topology isolation. |
| CQ wakeup burst size dominates throughput | WEAKENED | Large runnable-peak variation without throughput variation; no temporal drain/runqueue attribution or tail test establishes absence of all effects. |
| Kernel/version-specific io-wq account behavior | UNRESOLVED | Verified controls and observed owners establish local effects, not general account rules. |
| Worker API never changed limits | MOSTLY RULED OUT | Later post-set query validates requested pair; task response to unbounded confirms an effective local control. Historical Phase 6 remains invalid. |
| Observed small-limit workers are all unidentified Go tasks | MOSTLY RULED OUT | Adequate comm/TID/Tgid/cgroup diagnostics identify iou-wrk tasks. Does not census every default run. |
| 1 GiB buffers alone explain blocking threads | WEAKENED | Earlier smaller-buffer tests retained large populations; memory still affects performance and thresholds. |
| Storage readahead explains initial population behavior | UNRESOLVED | No precise fault/block tracing; nonresident-start peak attribution remains limited. |
| Sampling/accounting artifacts explain all large effects | WEAKENED | Repeated task identity and directionally consistent CPU/throughput interventions; exact peaks, timing and CPU decomposition remain uncertain. |

Phase 4 perf text includes user-only events (`:u` and `cycles/Pu`). Zero
context-switch or migration counts in those outputs must not be read as no
actual switches. These profiles do not attribute the overwhelmingly system-time
CPU to btrfs, copy-to-user, io-wq, or inline execution. No dominant kernel stack
claim is justified by the available profiles.

## What the evidence does NOT establish

### Final perf attempt

The [scoped perf record](../results/pre-upstream-controls/perf/README.md) supersedes
the earlier attribution attempt, not its historical raw data. Privileged
system-wide recordings used perf 7.2.6-1 with `perf_event_paranoid=2` and
`kptr_restrict=2`. Userspace callchains reached `os.(*File).ReadAt` /
`internal/poll.(*FD).Pread` / `syscall.pread` in blocking and the Ring submit
loop/batch/enter path in normal uring. Recorded kernel frames remained unresolved.
An unstripped matching vmlinux and the recordings shared kernel build ID
`a816ef0fc034196a61b0ac54dcfc3d06802bb809`. Explicitly supplying it did not resolve
the frames; perf warned that kernel address maps were restricted. There is no
basis for naming the unknown addresses or ranking kernel read/copy/issue work.
Inline execution remains **SUGGESTIVE ONLY**. No further profile or security
change is recommended before discussion.

It does not establish an application-visible win, lower total system CPU, lower
RSS/GC cost overall, better fairness, production cancellation semantics, safe
transparent fallback, a universal account classification, exact physical worker
limits across kernels, or general Go I/O improvement. It does not compare a mature
optimized blocking policy to a complete completion-based runtime design.

Three repetitions support large directional effects and broad plateaus, not
high-confidence significance or choosing a 1–2% winner. Medians of different
metrics are not necessarily achieved in one run. No composite score is meaningful
without an application's requirements and resource tradeoffs.

## Remaining runtime/design problems

These must be acknowledged **before discussion**, but generally resolved before
a production CL rather than before asking maintainers which approach to pursue.

| Area | Current gap / production requirement |
| --- | --- |
| Cancellation and deadlines | No cancellation protocol; cancel acknowledgement is not necessarily target terminal completion. Define ownership and wakeup races, deadline compatibility. |
| Close with pending I/O | Experimental Close requires no outstanding operations; harness drains. Real API close/cancel ordering and blocked callers need a design. |
| fd reuse, dup, stale CQEs | Ring-local monotone operation IDs are not fd lifetime/generation management. Current file remains open; reuse races are not demonstrated safe. |
| fork/exec, descriptor inheritance | Define CLOEXEC, fork locks and mappings/kernel ownership around process creation; not validated here. |
| Runtime/process shutdown | No full runtime teardown, async failure recovery, or scheduler-exit protocol. Fail-stop is not transparent fallback. |
| GC/pinning | Explicit Ring.ops reachability and terminal Unpin are necessary and tested, not sufficient for full runtime GC/liveness/stack interactions or bounded memory under all callers. |
| Memory accounting | Ring memory, pinned buffers, per-operation allocations, goroutine queues, native/kernel workers and resource limits all count. |
| Scheduler integration | External channels and locked diagnostic submitter do not establish M/P/G integration, blocking policy, wakeup fairness or observability. |
| Capability/fallback | Preserve EPERM finding; setup may fail due to seccomp/LSM/sysctl/resources. Probe required features/opcodes and distinguish pre-publication fallback from in-flight failure. |
| Kernel compatibility/security | No version-only policy; support baseline, feature differences, CQ overflow/error behavior and kernel attack surface require assessment. |
| Fairness/backpressure | Harness limits admitted calls; arbitrary callers, mixed sizes, cancellation, saturated queues and starvation need bounded production semantics. |
| Errors/partial reads | Go ReadAt retry/error rules must be preserved. Prototype short completion maps to EOF rather than proving all short-read retry equivalence. EINTR and delayed errors need coverage. |
| Writes/fsync/Open/Close | Not implemented as completion operations. Partial writes, append, offset ordering, durability and error propagation unaddressed. |
| Buffered versus direct I/O | Buffered experiment cannot determine direct-I/O alignment/lifetime/performance behavior or a universal backend choice. |
| internal/poll/Windows/network | Reference locking, pollDesc generations, IOCP semantics and Linux readiness invariants require a compatibility design; no epoll replacement evidence. |

The prototype's persistent ownership before SQ.tail publication and terminal-only
Unpin is valuable correctness work. It does not justify reusing a channel-based
external implementation unchanged inside the runtime. Kernel-owned buffers must
remain safe even when callers cancel, close, panic or exit; the experimental
happy-path drain avoids many of these cases.

## Is another experiment required before discussion?

**NO MORE EXPERIMENTS BEFORE DISCUSSION.** The unresolved question is value and
scope, not whether another carefully chosen setting can make the prototype look
better. Maintainers can discuss whether the observed scheduling/resource problem
matters, whether bounded blocking belongs at application/library level, and what
evidence would justify runtime complexity. The final same-revision bounded-
blocking comparison is now available and favors the simpler alternative as a
serious contender. Missing application-level benefits must remain explicit.

Further application evidence would change an integration recommendation, but its
absence does not prevent this narrowly honest conversation. Do not commission
another kernel tuning phase merely to reduce the unfavorable throughput gap.

## Narrow recommended upstream scope

The scope is a question about concurrent regular-file scheduling/resource costs:
are the observed thread-pressure regimes important enough to explore in Go, and
what simpler alternatives and semantic constraints should determine whether any
completion-based design deserves further work? The discussion should foreground
that blocking wins these resident throughput workloads and that bounded blocking
has not been displaced. io_uring is an experimental mechanism, not the conclusion.

This is a scope recommendation, not a draft public comment or request to replace
netpoll. Maintainership costs, portability, debugging and API semantics take
precedence over selecting this PoC's current configuration.

## Public-claim red flags

Do not publish the following statements:

- “io_uring is faster” or “Go needs io_uring.”
- “Blocking creates one thread per request.” Say that simultaneous slow syscall
  occupancy can create large populations in measured conditions.
- “U4/U8 is ideal” or “workers should equal GOMAXPROCS.”
- “Doubling CPUs caused the knee to double” or “we proved CPU-count scaling.”
- “The second host proves portability.” It replicated selected mechanisms only.
- “Normal io_uring always runs on one core.” These are approximate process CPU
  observations from one-submitter buffered-read experiments.
- “This was sustained cold disk I/O.” Only initial residency was verified.
- “No workers were present” based on the old wrapper observer.
- “The io-wq limit is a process-wide thread cap” or “all Linux file reads use the
  unbounded account.”
- “Lower Go threads means lower total resource usage.” Native/kernel work matters.
- “Service p99 is application p99” or a comparison mixing queue-exclusive and
  queue-inclusive definitions.
- “perf proved inline execution/btrfs was the bottleneck.” It did not.
- “All reads allocate nothing,” “race proves kernel buffer safety,” or “the
  prototype is production ready.”
- “Go maintainers require this design,” based on the unrelated roadmap.

## Artifact / revision map

Raw local artifacts were read without running performance measurements or
altering historical results. Archives are not added to Git by this review.

| Evidence | Actual available source / execution revision |
| --- | --- |
| Phase 0 and early reports | `/operator-home/docs/current-go-io-model.md`, `baseline-results.md`, `baseline-phase-1.5.md`, `io-uring-poc-phase-2.md`, `io-uring-phase-2-validation.md`, `io-uring-phase-3-batching.md`; outside repository, provenance limitation |
| Requested general roadmap | No repository `docs/io_uring.md`; separate `/operator-home/zonda/io_uring.md` is not authority |
| Phase 1 | `results/*.json`: 49 status-ok records, no per-run harness revision field |
| Phase 1.5 | `results/phase15`: 109 usable status-ok JSON and one empty attempted file; several revisions, v3 residency curve at `d870547…` |
| Phase 2 A | `results/phase2`: 18 blocking records and two unavailable/setup records; `02999c27…`; security failure retained |
| Phase 2 B | `phase2-matrix.tar.gz`: 36 canonical throughput/latency records; `57034821f397487e358327b0a2f5479cc334888a` |
| Phase 3 | Top-level `phase3-*throughput*.json` and latency files; `eddac7c354d6d6d9c73415f1547883531d718853` |
| Phase 4 | `phase4-artifacts.tar.gz`; `5106ae67038bb83fa3c5a50e07d740e21b4d4f13`; counter/profile limitations above |
| Phase 5 | `phase5-artifacts.tar.gz`; `f9604c8e7b98d1f6d60cbb4ea7efb4bf9a53dfb7` |
| Phase 6 | `phase6-artifacts.tar.gz`; `f69cccb803640887c782241b943ec5f4b851aeb4`, not announced `113659019…`; intervening executable-bit and final-CQ snapshot fixes |
| Phase 6B | `phase6b-gate.tar.gz`; canonical `cd23a8ad32b209e0edce3b5b91ceeceb0323f307`, observers `d7e483f45461a3d2aca82b7af5a761c06fe8ec0b`; three small gate records have revision `unknown` |
| Phase 6C | `phase6c-artifacts.tar.gz`: 12 canonical plus observations at `5530125f167090cf134038f3ab882623c374b07d`; `phase6c-complete.tar.gz` contains only two later observer results at `0fdc1b2…`, despite its name |
| Phase 7 | `phase7-artifacts.tar.gz` / extracted directory; 30 canonical runs at `f895b400d7a8ab200d9dfbb01dbd777fd44bd00e` |
| Phase 8 | `phase8-artifacts.tar.gz` and `phase8-artifacts-complete.tar.gz`; identical 30 canonical JSON at `3130f513681116d00d61aa4dc68e7a1bf560970b`; complete adds observations |
| C gate failures | `phase8-gate-failures.tar.gz`; preserve scheduler-dependent test and race-prerequisite failures, not benchmark records |
| Final pre-upstream controls | [24 raw runs and scoped perf evidence](../results/pre-upstream-controls/summary.md); `ec82557a9e295fcff895234212e76ad01b8cec19`; no new general phase |

Tracked Phase 4–8 documents and the AWS runbook were reviewed alongside source
in `cmd/iobaseline` and `internal/uring`. The Phase 8 implementation ancestor is
`dfc28d1823dcdb577194cf0b4091ff6cdbd98d57`; subsequent portability corrections
mean execution must be attributed to the actual raw SHA above, not that ancestor.
The archival gaps and stale prose should be disclosed if packaging evidence for
others; they do not change the independently inspectable later raw results.

## Final verdict

```
READY FOR UPSTREAM DISCUSSION:
    YES

READY TO PROPOSE IO_URING INTEGRATION:
    NO

ANOTHER PRE-DISCUSSION EXPERIMENT REQUIRED:
    NO
```

Discussion is justified by source-grounded thread behavior, replicated large
directional observations, and unusually useful falsifications of earlier
explanations. Its purpose must be deciding whether regular-file scheduling costs
are a problem maintainers consider worth addressing and which alternatives merit
evaluation. It is not a request for approval of the present backend.

Integration is not justified: resident blocking is substantially faster, a
well-chosen bounded-blocking approach remains a serious simpler competitor, no
real application benefit is demonstrated, and full file/runtime semantics are
unresolved. Lower sampled task count alone does not pay for permanent runtime
complexity.

No additional experiment is necessary to make that limited conversation useful.
The unresolved application value, attribution and compatibility questions should
be stated plainly, allowing maintainers to reject the premise or guide future
work rather than extending a host-specific tuning exercise indefinitely.
