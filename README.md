# Go regular-file I/O investigation

I investigated how highly concurrent regular-file reads interact with Go's
scheduler and native threads, using an external io_uring prototype as a
comparison mechanism. This is not a proposed runtime implementation.
Blocking remained faster in the resident throughput benchmarks.

Licensed under the BSD 3-Clause License. See [LICENSE](LICENSE).

The interesting result was the difference in task populations and execution
parallelism, not a general io_uring speed advantage. I'm not sure this belongs
in the runtime; I'd like maintainer feedback on the problem and on simpler
alternatives, especially bounded blocking.

Start with the [summary](docs/summary.md), the
[evidence review](docs/evidence-review.md), then
[reproduction instructions](docs/reproduction.md).
The [raw results](results/README.md) include the negative results and all canonical
repetitions.
The [final pre-upstream controls](results/pre-upstream-controls/summary.md) add
same-revision bounded blocking and the attempted perf attribution, including its
unresolved kernel symbols. The empirical investigation is now frozen.

## Reproduction

The portable entry point is the [Phase 8 runner](scripts/phase8-cross-host-reproduction.sh).
See its [runbook](docs/reproduction.md) for the full
matrix and gates. It needs Linux/amd64, Go 1.27.1, Bash, Python 3, Git, Linux
utilities, and a working C compiler/libc headers for the mandatory race gate.
The prototype itself does not use cgo or liburing.

Use a clean, committed checkout and an independently authorized Linux host.
The runner records the actual checkout revision and refuses tracked local changes:

```bash
GO_TOOL=/path/to/go1.27.1/bin/go \
  scripts/phase8-cross-host-reproduction.sh \
  phase8-artifacts \
  ./data/readat-512MiB.bin
```

This repository has an independent public snapshot history, not the private
investigation history. A new execution SHA is not a historical benchmark SHA.
[Source and result provenance](docs/provenance.md) maps the snapshot to the
historical execution revisions.

The output directory must be new. The runner can generate the experiment-owned
dataset and will stop on correctness/capability failures. Do not replace a failed
run or weaken security policy to get past a gate. The dataset is not distributed;
[generation details and its recorded digest](results/DATASET.md) are included.

Historical host-specific runners are intentionally omitted rather than patched.
The old observer commands in historical documents are not part of this snapshot's
reproduction path; scoped observation evidence is supplied in results. See
[legacy notes](scripts/legacy/README.md).

## Project map

```
cmd/        benchmark and analysis commands
internal/   experimental io_uring implementation
scripts/    reproduction and helper tooling
docs/       summary, evidence review and historical investigation
results/    reviewed raw experimental evidence
```
