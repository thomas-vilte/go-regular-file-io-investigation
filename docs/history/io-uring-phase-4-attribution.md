# Phase 4: attribution plan for the remaining throughput gap

Phase 3 established a host-specific result on the canonical workload:

```text
blocking ReadAt o1000: about 8.0 GiB/s, about 951 process threads
batched io_uring o1000: about 5.5 GiB/s, about 8 process threads
```

Batching reduced `io_uring_enter` calls per operation from about 1.9 to about
0.0052 without materially changing throughput. Thus enter syscall frequency is
not supported as the dominant explanation for this gap. The architectural
thread-count result and this host-specific throughput result remain separate.

This phase is attribution only. The Phase 3 ring algorithm, blocking backend,
logical workload, DONTNEED preparation, and admission semantics are unchanged.

## Canonical profile workload

Every initial paired run uses exactly:

```text
backend: blocking or uring
ReadAt random, 1 MiB buffers
1000 logical clients, max-outstanding=1000, GOMAXPROCS=4
seed=31908, duration=5s, warmup=0
experiment-owned 512 MiB file
POSIX_FADV_DONTNEED and mincore verification immediately before measurement
```

Throughput/counter profiling uses three fresh processes per backend. Latency is
not part of the initial profiling suite. The output JSON remains the authority
for the actual residency observation; `DONTNEED` alone is never labeled cold.

## Observation plan

`scripts/phase4-profile.sh` builds the authoritative revision, records the
environment, takes full `/proc/diskstats`/mount snapshots before and after each
run, and stores three process-level runs per requested backend. If unprivileged
`perf stat` accepts `task-clock`, it requests task-clock, context-switches,
CPU migrations, page faults, cycles, instructions, and branch counters.

Otherwise it records the unavailable capability and runs the identical harness.
When an external `time` executable is available at `/usr/bin/time` or
`/bin/time`, it also records `time -v`; when neither exists it writes an
explicit marker and uses the harness's process rusage fields instead. Harness
JSON always supplies process rusage, runtime metrics, thread peaks, and
io_uring batch counters. The script does not run `sudo`, alter sysctls, or
write the input other than the already-authorized `POSIX_FADV_DONTNEED`
request.

`scripts/phase4-observe-workers.sh` runs one canonical process and takes
25-ms snapshots of the root process, its `/proc/<pid>/task` details, its cgroup,
`/proc/<pid>/sched`, and system-wide `ps -eLo` task lists. Its purpose is to
look for io-wq workers and establish whether they are process threads, separate
tasks, and/or visibly related to the benchmark. Names alone are insufficient:
PID/PPID/TID snapshots and cgroup membership must support any claim.

`scripts/helpers/phase4-diskstats.sh` is also available for manually bracketing a run.
Diskstat deltas remain device-level observations; on btrfs with `compress=zstd`, they cannot be equated with logical bytes requested by `ReadAt`.

## Revision gate and execution identity

The required Phase 4 tooling revision is:

```text
b35bd95d1c4379151f714da4127bb17dd905ee9f
```

The scripts require it to be an ancestor of `HEAD` with:

```sh
git merge-base --is-ancestor "$required_revision" HEAD
```

They then record the actual execution revision with `git rev-parse HEAD` and
pass that exact value through `-harness-revision`. Environment B may therefore
use any descendant containing the required tooling revision; it must not be
restricted to the earlier Phase 3 revision `eddac7c...`.

## Partial CPU-accounting rule

`perf stat` around the benchmark command and its rusage report process/child
accounting, not automatically CPU of io-wq tasks. Whether io-wq worker CPU is
included in that accounting is an empirical question. Phase 4 records it in
this table rather than guessing:

| worker `comm` | PID/PPID/TID evidence | cgroup | observed CPU | in process perf | in rusage |
| --- | --- | --- | --- | --- | --- |
| pending Environment B observation | N/A | N/A | N/A | unknown | unknown |

If system-wide profiling is forbidden, total worker-inclusive CPU remains
unresolved. Global CPU values are not presented as benchmark CPU without a
separate accounting argument.

## Required result tables

The report filled after Environment B runs uses these fields. Unavailable data
is written `N/A`, not estimated.

| backend | repetition | MiB/s | wall s | user CPU s | system CPU s | process CPU s | task-clock | cycles | instructions | IPC | context switches | voluntary CS | involuntary CS | CPU migrations | minor faults | major faults | peak process threads | peak not-in-go | peak runnable |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| pending |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |

| uring repetition | submit batches | avg submit batch | max submit batch | CQ drain batches | avg CQ drain | max CQ drain | total enters | enters/op | peak runnable |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| pending |  |  |  |  |  |  |  |  |  |

Large CQ-drain batches and runnable peaks are reported side by side. They do
not establish that drain bursts cause scheduler pressure; only a repeated
association in comparable runs would justify a later, separately authorized
batch-cap experiment.

## Local capability probe: not Environment B data

The current Codex sandbox was inspected without policy changes:

```text
kernel: Linux 7.2.3-1-cachyos x86_64
uid: 1000 (OPERATOR)
io_uring_disabled=0; io_uring_setup -> EPERM
NoNewPrivs=1; Seccomp=2; Seccomp_filters=1
perf: not installed; perf_event_paranoid=2; kptr_restrict=2
bpftrace: not installed; bpftool present
cgroup v2: present
tracefs/debugfs tracing: unreadable
```

No profiling suite was run here. This is a capability limitation, not a Phase
4 measurement.

## Environment B commands

From any descendant of the required Phase 4 tooling revision:

```sh
uname -a
perf --version
cat /proc/sys/kernel/perf_event_paranoid
cat /proc/sys/kernel/kptr_restrict
id
grep -E '^(NoNewPrivs|Seccomp|Seccomp_filters):' /proc/self/status
cat /proc/sys/kernel/io_uring_disabled

scripts/phase4-profile.sh both phase4-artifacts/counters ./data/readat-512MiB.bin
scripts/phase4-observe-workers.sh blocking phase4-artifacts/workers-blocking ./data/readat-512MiB.bin
scripts/phase4-observe-workers.sh uring phase4-artifacts/workers-uring ./data/readat-512MiB.bin
```

If `perf record` is allowed by the reported policy, run one fresh canonical
process per backend using the exact command saved by `phase4-profile.sh`.
If record is unavailable, preserve its error and do not change security
settings. Store raw perf data, CSV, time output, JSON, worker snapshots, and
diskstats under ignored `phase4-artifacts/`.

## Interpretation boundary

Possible conclusions remain conditional: userspace process time, io_uring/io-wq
kernel work, btrfs/compression behavior, device saturation, or repeated CQ
wakeup-burst association. On this four-CPU btrfs/NVMe host, none generalizes to
all Linux systems without additional controls. No tuning follows this phase
until the raw attribution evidence is reviewed.
