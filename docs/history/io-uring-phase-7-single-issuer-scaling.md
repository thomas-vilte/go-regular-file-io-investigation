# Phase 7: single-issuer io-wq worker scaling

Status: tooling prepared; no Phase 7 performance measurements collected in the
restricted sandbox. This is intended as the last major single-host mechanism
experiment before broader reproduction and an upstream discussion, not a Go
runtime policy proposal.

## Evidence motivating the control

The user-reported Phase 6C results used three resident repetitions per point:

| Condition | Median MiB/s | Range | CPU/wall | Peak tasks | Submit TIDs | enter/op |
|---|---:|---|---:|---:|---|---:|
| Async defaults, unlocked | 9851.6 | 8582.1–10043.9 | 2.85 | 268 | 7 | 0.904 |
| Async defaults, locked | 10135.8 | 9598.9–10318.2 | 2.96 | 184 | 1 | 0.158 |
| Async U1, unlocked | 8832.1 | 8123.6–10172.8 | 2.32 | 15 | 6–7 | 1.464 |
| Async U1, locked | 7088.1 | 6731.8–7931.9 | 1.22 | 11 | 1 | 1.739 |

The separately reported observer found seven worker-owner suffixes matching
the seven submit-enter TIDs in unlocked U1, and one matching owner/TID in
locked U1. This supports migration multiplying relevant issuer/io-wq contexts
on this kernel/path. Locking with default maxima did not reduce throughput.

Phase 6 was an invalid scaling control: varying the bounded maximum left
hundreds of process tasks. Phase 6B verified post-set maxima and found that
unbounded=1 collapsed that population. Thus unbounded is the empirically
relevant account here. The configured maximum is not the observed worker
count, nor necessarily a process-wide ceiling. Phase 7 fixes the submitter
thread to stabilize the issuer context while varying only its unbounded
maximum. Exact CachyOS source classification remains unverified locally.

## Instrumentation audit and change

Starting HEAD was `5530125f167090cf134038f3ab882623c374b07d`, a descendant of
Phase 6C `0fdc1b2eb7fd728363cc05e81737a3c0658518fa`. History is preserved.

`-uring-record-issuer-tids` is now false by default and valid only with the
uring backend. Both enter paths guard the gettid call and TID bookkeeping;
disabled mode performs no hot-path gettid, map insertion or TID-set locking.
The one-time setup and registration observations remain. JSON records
`config.uring_record_issuer_tids` and `io_uring.record_issuer_tids`; disabled
submit/wait sets are empty with count zero, meaning **not recorded**, not
that there were no issuers. Enabled sets retain the 64-TID cap/truncation flag.

Audit found an existing instrumentation error: Phase 6C populated the wait
set from `enterSubmit`, immediately after recording the submit set. Its old
wait TIDs must not be used for CQ-consumer attribution. This revision moves
that recording to the actual GETEVENTS call site. The supplied submit-TID to
worker-owner correlation does not rely on the erroneous wait field. Enabled
unlocked gettid observations are nearby userspace samples, not an atomic
kernel trace of the subsequent syscall: preemption/migration between calls
is possible. No extra thread locking is introduced to hide that limitation.

The existing observer explicitly enables recording, because it runs separately
for identity. Canonical runs explicitly disable it. Phase 6C had two gettid
calls per submit enter; its performance should not be directly combined with
this revision's measurements. All controls are rerun on the same revision.

## Matrix and acceptance

Thirty fresh processes: U=1,2,4,8,16,32,64,default, each three times, plus
blocking and normal uring controls three times each. All curve points enable
force-async and submitter locking. Normal uring disables both and leaves worker
maxima unchanged. Default means no worker-limit set call. Every explicit U
preserves the initial queried bounded maximum and requires the post-set pair
to match exactly before starting I/O.

All runs: 512 MiB existing input; shared-fd random ReadAt; 1 MiB buffers;
1000 clients; 1000 max outstanding; P=4; 5 seconds; zero warmup; seed 31908;
prewarm; no latency collection. Both after-prewarm and immediately-before-
measurement mincore snapshots must be exactly 1,000,000 resident ppm.

The fixed schedule is:

```text
rep1 blocking U1 U8 normal U2 U32 U4 default U16 U64
rep2 U16 normal U1 U64 blocking U4 default U2 U32 U8
rep3 default U4 U32 blocking U8 U1 U64 normal U2 U16
```

The Bash runner handles its own arrays (no interactive zsh word splitting),
refuses existing output directories and tracked dirty changes, builds with
`/operator-home/go-upstream-go1.27.1/bin/go`, and verifies its version. The required
Phase 7 implementation revision is resolved as the Git commit that introduced
the runner (`git log --diff-filter=A` for this path); it must be an ancestor
of actual HEAD. Both SHAs are recorded. This avoids an impossible self-SHA
embedded inside its own commit and permits later descendants.

After each process, `phase6check -phase7-mode=...` validates status, residency,
reconciliation, workload, actual revision, recording disabled, correct control
flags, and the verified worker pair. Any failure stops the runner and leaves
all JSON/stderr/commands already produced. No fallback or retry drops a run.

## Environment B commands

First the correctness gate; stop if any command fails. Inspect verbose output
to ensure kernel-backed tests actually execute on Environment B rather than
skip. Race detection does not model kernel access to pinned/mapped memory.

```sh
cd /operator-home/bench
/operator-home/go-upstream-go1.27.1/bin/go test -count=1 -buildvcs=false -v ./...
/operator-home/go-upstream-go1.27.1/bin/go test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline
scripts/phase7-single-issuer-worker-curve.sh phase7-artifacts ./data/readat-512MiB.bin
```

Requires Bash, Python 3 standard library, Git and existing Linux user tools;
no dependency installation, root or security change. After the curve succeeds,
take separate identity observations (these are not throughput repetitions):

```sh
scripts/phase4-observe-workers.sh uring phase7-artifacts/observe-U1 ./data/readat-512MiB.bin --prewarm --force-async --lock-submitter-thread --unbounded-workers=1
scripts/phase4-observe-workers.sh uring phase7-artifacts/observe-U4 ./data/readat-512MiB.bin --prewarm --force-async --lock-submitter-thread --unbounded-workers=4
scripts/phase4-observe-workers.sh uring phase7-artifacts/observe-U16 ./data/readat-512MiB.bin --prewarm --force-async --lock-submitter-thread --unbounded-workers=16
scripts/phase4-observe-workers.sh uring phase7-artifacts/observe-default ./data/readat-512MiB.bin --prewarm --force-async --lock-submitter-thread
```

Preserve actual sample counts and comm/TID evidence. If fewer than 100 samples
are obtained in five seconds, mark quantitative identity sampling inadequate;
do not assume every short-lived worker was seen. The harness lightweight
external /proc sampler is the canonical source for peak process tasks.

## Artifacts and interpretation

```text
phase7-artifacts/environment.txt             # required and actual revisions
phase7-artifacts/planned-order.txt
phase7-artifacts/execution-order.txt         # attempted runs, in order
phase7-artifacts/runs/r{1,2,3}-{mode}.json
phase7-artifacts/runs/r{1,2,3}-{mode}.command.txt
phase7-artifacts/runs/r{1,2,3}-{mode}.stderr.txt
phase7-artifacts/runs/r{1,2,3}-{mode}.check.txt
phase7-artifacts/per-run.csv
phase7-artifacts/curve.csv
phase7-artifacts/observe-*/{result.json,summary.txt,samples/,task-metadata/}
```

`per-run.csv` derives MiB/s, user/system/total CPU, wall, CPU/wall, CPU us/op,
task/runtime peaks, context switches, batching/enter metrics and worker config.
`curve.csv` reports median/min/max for throughput, CPU/wall, CPU us/op, task/Go
thread peaks, voluntary/involuntary CS and enter/op, plus throughput ratios to
the same-revision blocking and locked/default medians. Missing runtime metrics
are blank, not zero. All three repetitions are required; no composite score.

CPU uses the existing rusage delta window, which includes some setup/teardown
outside measured workload wall time. CPU/wall is an approximation to effective
cores, not exact worker CPU attribution. Peaks are sampled lower bounds.

Describe a saturation range only if repetitions support it. Compare throughput
with CPU saturation, task population and enter/op without changing batching.
Preserve noisy or nonmonotonic results and any scaling continuing through 64.
Three runs reveal obvious instability but provide limited statistical power.
No observed knee on this four-CPU, btrfs/zstd, resident-read host is a Go runtime
worker policy. LockOSThread, IOSQE_ASYNC and worker maxima remain diagnostics.
No runtime integration or additional optimization follows automatically.
