# Scoped perf evidence: kernel attribution inconclusive

Two operator-authenticated system-wide recordings were made on Environment B
with perf 7.2.6-1, `perf_event_paranoid=2`, `kptr_restrict=2`. No security setting
was changed. The agent's earlier unprivileged access failure is retained in
[perf-access.txt](../perf-access.txt); it does not mean the subsequent privileged
recordings failed to collect samples.

| Record | Command | Benchmark-only excerpt | Diagnostic JSON |
| --- | --- | --- | --- |
| Blocking | [record.command.txt](blocking/record.command.txt) | [benchmark-excerpt.txt](blocking/benchmark-excerpt.txt) | [result.json](blocking/result.json) |
| Normal uring | [record.command.txt](normal-uring/record.command.txt) | [benchmark-excerpt.txt](normal-uring/benchmark-excerpt.txt) | [result.json](normal-uring/result.json) |

These are resident P4/o1000 diagnostics, not canonical throughput repetitions.
Both JSON records pass status, revision, 100% prewarm/pre-measurement residency
and applicable reconciliation checks. Their instrumented throughput must not be
mixed into the 24 uninstrumented nonresident-start records.

Commands preserve the recorded options, with only operator paths and UID/GID
replaced by `${REPO}`, `${OPERATOR_UID}` and `${OPERATOR_GID}`. perf used
`-a -F 99 -e cycles --call-graph dwarf,16384`; `setpriv` returned the workload
to the operator identity while perf remained privileged. No CPU affinity or
kernel setting changed. Commands are provenance, not requests to execute again.

The blocking excerpt reaches `os.(*File).ReadAt`, `internal/poll.(*FD).Pread`,
`syscall.pread` and syscall wrappers. The normal-uring excerpt reaches Ring's
`submitLoop`, `submitBatch`, `enterSubmit` and syscall wrappers. Frames on the
kernel side are unresolved addresses. In the source, enterSubmit invokes
io_uring_enter; the report does not provide named kernel issue/read/copy frames.

The local unstripped vmlinux is
`/usr/lib/modules/7.2.3-1-cachyos/build/vmlinux`. Its
[build ID](vmlinux-build-id.txt) matches both existing recordings:
[blocking](blocking/kernel-build-id.txt), [normal](normal-uring/kernel-build-id.txt).
All report `a816ef0fc034196a61b0ac54dcfc3d06802bb809`. These metadata checks
were repeated read-only during packaging, without recording new samples.

The operator reports explicitly passing that vmlinux to perf report. The
resulting private `report-symbolized.txt` files still contain unresolved kernel
frames, from which the excerpts are taken. The exact vmlinux report invocation
was not saved as a command file; it is not invented here. Original report stderr
preserves: `Kernel address maps (/proc/{kallsyms,modules}) were restricted.`
Recording also warned that it could not record the kernel reference relocation
symbol. An on-disk matching build ID did not overcome missing runtime mappings.

The excerpts contain only the first iobaseline symbol block in each symbolized
report, keeping addresses/order and original percentages. Those percentages use
the original **system-wide denominator**, not a scoped benchmark CPU total.
Do not sum them into a kernel cost breakdown. Unwind artifacts may remain;
successful DWARF symbolization is not evidence of perfect complete callchains.

Raw perf.data, full reports/scripts and background process lists stay private.
No unrelated process names or paths were carried into the excerpts. Scope and
original/public hashes are recorded in the [manifest](../../FILE-MANIFEST.json).
No address has been relabeled with a guessed kernel symbol. Optional async U4
was not profiled. **Kernel-stack attribution remains inconclusive; no further
profile is needed before discussion.**
