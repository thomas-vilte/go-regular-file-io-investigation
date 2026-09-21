# Final pre-upstream controls

Public export note: the 24 canonical JSON change only the authorized
`dataset.path` metadata to `data/readat-512MiB.bin`; all other bytes match the
private originals. Original/public hashes are in [FILE-MANIFEST.json](../FILE-MANIFEST.json).

## Purpose

Close two specific objections, not optimize the prototype: compare admission-
limited blocking on the same revision, and obtain kernel/user perf attribution.
The 24-run comparison is complete. The privileged perf attempt is also complete,
but kernel-stack attribution is inconclusive. No new phase or architecture is
proposed; no more empirical work is required before maintainer discussion.

## Exact environment/revision

Environment B, Linux 7.2.3-1-cachyos, Intel i3-9100F, four logical CPUs, about
15.55 GiB RAM, btrfs `compress=zstd:1` on local NVMe. Dataset: existing 512 MiB
experiment-owned file; path, SHA-256, mount options and toolchain recorded in
`environment.txt`. Go 1.27.1 linux/amd64, upstream
`862c888e612ac346c7c4d99c9392bdfd265f33b0`.

Starting private HEAD: `605280a3bad0cf070e465135c4bc73b60d245f83`.
Implementation AND all 24 execution revisions:
`ec82557a9e295fcff895234212e76ad01b8cec19`.
Only new runner/checker/report tooling and an ignored output path were committed;
`cmd/` and `internal/` were unchanged. This public result group adds reviewed
evidence and documentation only; it does not alter the executed implementation.

Tests, real kernel-backed normal/race tests and vet passed before measurement,
with no skipped tests. Setup outside the restricted sandbox reported available,
NoNewPrivs=0 and Seccomp=0. No policy was changed. The same host's sandbox probe
had returned EPERM with NoNewPrivs=1/Seccomp=2; those execution contexts differ.

## Same-revision bounded-blocking result

All 24 canonical runs passed status, workload/revision and applicable lifecycle
checks. Both after-DONTNEED and immediately-before-measurement source residency
were **zero ppm in all 24 runs**. This is nonresident-start, not sustained cold
disk I/O. The five-second workload repeatedly accesses the same 512 MiB source.

Medians of three runs; ranges are observed min–max, not confidence intervals.
Independent metric medians need not have occurred in the same repetition.
Full precision, user/system CPU, all runtime peaks, residency and batching
counters are in `per-run.csv`, `bounded-blocking.csv` and authoritative JSON.

| Mode / admitted limit | MiB/s median (min–max) | / unrestricted blocking | CPU/wall | CPU µs/op | Sampled process tasks | Go threads | Voluntary / involuntary CS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Blocking / 4 | 6998 (6029–8733) | 0.696 | 1.814 | 259.2 | 10 | 10 | 83008 / 52668 |
| Blocking / 16 | 7616 (7598–7851) | 0.757 | 2.166 | 284.4 | 21 | 21 | 96550 / 48824 |
| Blocking / 64 | 8306 (7880–8464) | 0.826 | 2.471 | 297.5 | 69 | 69 | 100807 / 52546 |
| Blocking / 256 | 7914 (6651–8730) | 0.787 | 2.266 | 286.4 | 262 | 262 | 96716 / 44225 |
| Blocking / 1000 | 10056 (8686–10558) | 1.000 | 2.392 | 238.9 | 1005 | 1005 | 90494 / 48209 |
| Normal uring / 1000 | 5536 (5384–6037) | 0.551 | 0.952 | 172.0 | 8 | 8 | 6440 / 2314 |
| Async locked U4 / 1000 | 7423 (7410–8402) | 0.738 | 2.244 | 302.9 | 15 | 11 | 163359 / 103953 |
| Async locked default / 1000 | 8485 (8365–9650) | 0.844 | 2.537 | 299.0 | 1011 | 11 | 55081 / 29386 |

Bounded blocking is a **stronger, same-revision simpler competitor** after this
control. Small/medium limits retained roughly 70–83% of unrestricted throughput
with far fewer tasks. B16 and U4 have comparable throughput ranges; U4 has fewer
sampled process tasks but more context switches and no demonstrated CPU/op win.
B64 has substantial throughput with 69 tasks, not the roughly thousand-task
population of unrestricted blocking or default async. There is no clean
monotonic curve or universal optimum: B256 added population without improving
the median over B64, with overlapping and sometimes broad ranges.

Normal uring uses less CPU and many fewer context switches, but also much less
throughput. That tradeoff must remain visible, not treated as either an automatic
win or a universal loss. No application objective selects one of these points.

The strongest additional negative result is **default async process-task
proliferation despite a locked submitter**: peaks were 989, 1011 and 1014, while
Go-owned peaks were 10, 11 and 14. These are process tasks, not an identified
worker census. Do not subtract independent sampled peaks to count exact workers.
Unrestricted blocking peaks were 1005, 1006 and 449: even the large pressure
signal varies across repetitions. U4 process peaks were 15 in all three runs.

Every uring run queried default maxima [16,63206]; U4 explicitly verified
post-set [16,4]. Normal and default async made no limit-setting call. Median
enter/op was 0.00397 normal, 1.35237 U4 and 0.30399 default. The raw JSON retains
all submission/CQ batches, consumed/requested entries, pin counts and zero
remaining/unknown/duplicate counters. No batching policy was changed.

## What bounded blocking does/does not answer

This answers why a semaphore deserves serious consideration: it demonstrably
contains native-task pressure while retaining substantial throughput. It does
not demonstrate equivalent client latency or a real application benefit for
either backend. The unchanged duration harness does not collect latency;
queue-inclusive latency requires its separate fixed-operation mode. No latency
matrix was added and no service-only tail is presented as end-to-end latency.

CPU/wall and CPU/op use rusage including some setup/teardown outside timed
wall. Process peaks span the child lifetime, not a time-attributed storage wait.
Buffers are preallocated but not explicitly prefaulted. Host background load,
cache/memory traffic and initial page population remain confounders; no claim
of an isolated idle host or precise exclusive system CPU is made. Start/end
load, proc/stat and diskstats snapshots remain in the private originals, omitted
from this scoped public export. Some command
capture labels are buffered after their subprocess output; commands and exit
statuses are retained in order, not necessarily immediately before their output.

## perf capability and method

Installed perf: 7.2.6-1. `perf_event_paranoid=2`, `kptr_restrict=2`.
Unprivileged `perf stat -a -e cycles -- true` failed with the access-policy
message; `sudo -n true` failed with `a password is required`. Exact outputs are
in `perf-access.txt`. Local terminal authentication was not shared with the
agent session. No sysctl/security changes, and no user-only substitute profile.

After operator authentication, both profiles were recorded with privilege after
the canonical controls. They used prewarm/100% source residency, P4/o1000, the
same binary and revision, and system-wide `cycles` at 99 Hz with `dwarf,16384`
callgraphs. Only perf was privileged; the workload dropped back to the invoking
UID/GID/groups. These are diagnostics, not throughput repetitions. Reviewed
commands and excerpts are under [perf/](perf/README.md); raw system-wide data and
unrelated process reports are deliberately not published.

## Blocking perf observations

The benchmark userspace chain contains `os.(*File).ReadAt`,
`internal/poll.(*FD).Pread`, `syscall.pread` and syscall wrappers, followed by
unresolved kernel addresses. See the [scoped excerpt](perf/blocking/benchmark-excerpt.txt).
This identifies the userspace entry path, not the kernel functions consuming CPU.

## Normal io_uring perf observations

The [scoped excerpt](perf/normal-uring/benchmark-excerpt.txt) contains Ring's
`submitLoop`, `submitBatch`, `enterSubmit` and syscall wrappers leading into
unresolved kernel addresses. The source identifies this syscall as io_uring_enter;
the kernel issue functions themselves were not resolved.

The unstripped `/usr/lib/modules/7.2.3-1-cachyos/build/vmlinux` and both recordings
share build ID `a816ef0fc034196a61b0ac54dcfc3d06802bb809`. The operator supplied
the matching vmlinux explicitly to perf report, but kernel frames remained
unresolved. The reports warn: `Kernel address maps (/proc/{kallsyms,modules}) were restricted.`
Matching symbols alone did not supply the runtime address maps needed here.
No unknown address is labeled as filemap, copy-to-user or an io_uring issue
function. Kernel-stack attribution remains **INCONCLUSIVE**. Near-one CPU/wall
is still consistent with serialized issue/execution, not proof of inline reads.

## Optional async observations

Not run: the required useful kernel/user attribution from P1/P2 was unavailable.
The experiment is frozen; no further profile or security change is requested.

## Which previous explanations were strengthened/weakened

The simpler admission-control alternative is strengthened. A claim that async
completion with a locked issuer alone bounds native task population is weakened
by the default-async nonresident-start result. No new attribution claim about
inline execution is justified by the unresolved kernel frames. Userspace
callchains narrow the observed entry paths but do not establish their kernel cost.

## Remaining uncertainty / exact relevance to #31908

This is enough to update the draft's bounded-blocking caveat with contemporary
same-revision evidence and foreground default-async population costs. It does
not support recommending io_uring integration. I attempted the requested
system-wide comparison; userspace was usable but reliable kernel attribution
could not be obtained under existing observability restrictions. That limitation
is the final perf conclusion, not a reason to tune or change security settings.
No additional experiment is required before the narrowly framed discussion.
