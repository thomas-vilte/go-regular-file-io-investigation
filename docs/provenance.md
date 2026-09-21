# Source and result provenance

The Go source in this snapshot corresponds to investigation revision
`6f697ddebebf27576f7fe1f81647e7d05b3b497c`. It includes the Phase 8 portability
fixes. No runtime or I/O implementation changes accompanied the final controls.
[SOURCE-MANIFEST.json](../results/SOURCE-MANIFEST.json) records original and current
source hashes and the omitted historical runners. Scripts were relocated; the
Phase 8 runner's helper path was updated. The pre-existing first-line difference
in the Phase 7 Python helper is retained and documented in [scripts/](../scripts/README.md).

The repository has independent snapshot history, not the full investigation
history. A checkout commit identifies a reproduction's source; it must not be
substituted for the execution SHA inside an older result.

| Evidence | Execution SHA |
| --- | --- |
| B Phase 2 | `57034821f397487e358327b0a2f5479cc334888a` |
| B Phase 3 | `eddac7c354d6d6d9c73415f1547883531d718853` |
| B Phase 6 invalid control | `f69cccb803640887c782241b943ec5f4b851aeb4` |
| B Phase 6B | `cd23a8ad32b209e0edce3b5b91ceeceb0323f307` |
| B Phase 6C canonical | `5530125f167090cf134038f3ab882623c374b07d` |
| B Phase 6C later topology | `0fdc1b2eb7fd728363cc05e81737a3c0658518fa` |
| B Phase 7 | `f895b400d7a8ab200d9dfbb01dbd777fd44bd00e` |
| C Phase 8 | `3130f513681116d00d61aa4dc68e7a1bf560970b` |
| A selected setup EPERM probe | `649af01fc8b849ff5edf83d31d22c949e9302516` |
| B final pre-upstream controls | `ec82557a9e295fcff895234212e76ad01b8cec19` |

The final controls began at private HEAD
`605280a3bad0cf070e465135c4bc73b60d245f83`. Their 24 canonical runs and two
separate perf diagnostics are described in the [final summary](../results/pre-upstream-controls/summary.md).
The public summary supersedes an earlier pending-perf note, not the raw data.

Phase 8's implementation ancestor is
`dfc28d1823dcdb577194cf0b4091ff6cdbd98d57`. C stopped first at the
`9e153d8e2dea763d567ae860904869354cb42e7a` gate. The deterministic test correction
was `b2bccf754f40da86fe7b2b57a229c01ab0b3b3db`, followed by the race prerequisite
fix at `3130f513681116d00d61aa4dc68e7a1bf560970b`. Failed gates are preserved
as tooling/correctness evidence, not throughput results.

## Historical coverage

The early `current-go-io-model.md`, `baseline-results.md`,
`baseline-phase-1.5.md`, `io-uring-poc-phase-2.md`,
`io-uring-phase-2-validation.md` and `io-uring-phase-3-batching.md` lived outside
the original benchmark repository. They are not included here and are not
required to execute the current Phase 8 runner or inspect Phase 7/8 results.
The [evidence review](evidence-review.md) records their limitations. An unrelated
project's `io_uring.md` roadmap is not evidence for this investigation.

The [historical reading notes](history/README.md) identify stale prose, invalid
controls and instrumentation errors. Original erroneous fields remain alongside
corrections; they have not been rewritten to make the history appear successful.

## Result identity

[MANIFEST.md](../results/MANIFEST.md) identifies selected groups and omissions.
[FILE-MANIFEST.json](../results/FILE-MANIFEST.json) records result origins,
original/current hashes and scoped-derivative inputs.
[REDACTIONS.md](../results/REDACTIONS.md) records metadata transformations.
The 60 canonical Phase 7/8 JSON match the original archives byte-for-byte. In the
24 final-control JSON, only `dataset.path` is redacted; all other bytes match.
Measurement values, timestamps, revisions and reconciliation counters are intact.

Global process snapshots and raw system-wide perf recordings are omitted.
Scoped observations retain within-run PID/TID relationships; selected perf
excerpts do not identify unresolved kernel functions. Those are limitations,
not missing data filled by inference.

Verify result files from `results/` with `sha256sum --quiet -c SHA256SUMS`.
The root `SHA256SUMS` covers the technical snapshot, excluding itself, Git
metadata and ignored local editor files. Original source/result hashes remain
distinct from checksums of sanitized derivatives.
