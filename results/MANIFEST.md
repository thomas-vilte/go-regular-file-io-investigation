# Results inventory

Start with the [results guide](README.md). The canonical sets are
[Environment B / Phase 7](environment-b/phase7/) and
[Environment C / Phase 8](environment-c/phase8/). Their raw JSON is authoritative;
CSV files are summaries, not replacements. Earlier evidence and failed gates are
retained separately below. The [project summary](../docs/summary.md) and
[evidence review](../docs/evidence-review.md) explain the conclusions.

All paths below are relative to this export. Raw measurements are authoritative; CSV is secondary. No historical original was modified. `FILE-MANIFEST.json` lists every selected member, original/public hashes, redaction categories and derived-topology inputs.

## Selected groups

### results/pre-upstream-controls

Final pre-upstream controls, not a new general benchmark phase. Purpose: answer
the bounded-blocking and kernel/userspace attribution objections before
maintainer discussion. Original: private `pre-upstream-controls/` directory.
Starting private HEAD: `605280a3bad0cf070e465135c4bc73b60d245f83`.
Implementation/execution: `ec82557a9e295fcff895234212e76ad01b8cec19`.

Selected: all 24 canonical JSON with only the operator-authorized `dataset.path`
redaction to `data/readat-512MiB.bin` (every other byte unchanged), both CSVs,
run order, commands/checks/stderr, correctness/race/vet logs, reviewed host and
capability metadata, final public summary, and scoped perf command/report
derivatives. Individual origins, input/output hashes and selection rules are in
FILE-MANIFEST.json. The private summary's earlier "perf pending" status is
superseded by the operator's completed recordings and the current public summary.

Omitted: binaries, raw system-wide perf.data, full perf reports/scripts,
global process snapshots, broad host inventory and per-run system diskstats.
No unrelated process names/paths are included in the perf excerpts. Private
artifacts remain unchanged. Operator paths in commands are replaced by
`${REPO}`; exact flags, settings and revisions remain. See REDACTIONS.md.

Limitations: three runs per condition; nonresident-start, not sustained cold
I/O; sampled process peaks and broader rusage windows; no current client
latency; no application-level win. Privileged perf userspace frames were usable,
but kernel frames remained unresolved despite a matching vmlinux. Build IDs were
checked from existing files; no new measurements were made during publication.

### results/environment-b/phase7

Purpose: All 30 canonical records and run order; worker-throughput curve

Original: `phase7-artifacts.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `f895b400d7a8ab200d9dfbb01dbd777fd44bd00e`.

Selected: 125 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Binaries and all Phase 7 observations omitted; C and Phase 6C provide selected topology evidence.

Limitations: Three repetitions; CPU/window and sampled-peak caveats. Environment capture lacks a standalone dataset digest.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/environment-c/phase8

Purpose: All 30 canonical records, gates, metadata, scoped worker identity

Original: `phase8-artifacts-complete.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `3130f513681116d00d61aa4dc68e7a1bf560970b`.

Selected: 796 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Binaries, global before/after ps, redundant PID files, generated race-preflight source; no dataset.

Limitations: Canonical resident P=8; identity diagnostics P=4. Default heavy observer insufficient. Source residency snapshots are not sustained cold I/O.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/earlier-evidence/phase2-b

Purpose: All 36 throughput/latency records, including bounded blocking

Original: `phase2-matrix.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `57034821f397487e358327b0a2f5479cc334888a`.

Selected: 36 files, individually enumerated in FILE-MANIFEST.json.

Omitted: No source or additional files in this selected archive.

Limitations: Throughput and latency are separate experiments. Do not compare earlier queue-exclusive service tails as application tails.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/earlier-evidence/phase3

Purpose: All six throughput/latency records; enter-frequency falsification

Original: `private repository top-level phase3-o1000-*.json` (private archive basename/location label, not a public URL).

Execution revision: `eddac7c354d6d6d9c73415f1547883531d718853`.

Selected: 6 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Smoke omitted; no archive needed.

Limitations: Historical sequential comparison to Phase 2, not randomized concurrent comparison.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/earlier-evidence/phase6-invalid-control

Purpose: Complete 33-run invalid control, not a valid worker-count curve

Original: `phase6-artifacts.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `f69cccb803640887c782241b943ec5f4b851aeb4`.

Selected: 101 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Binaries and invalid worker observations omitted; invalidity documented, not hidden.

Limitations: Bounded setting did not control relevant population; no post-set verification; old observer wrong process.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/earlier-evidence/phase6b

Purpose: Four account-control records; query/set/post-set verification

Original: `phase6b-gate.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `cd23a8ad32b209e0edce3b5b91ceeceb0323f307`.

Selected: 4 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Three tiny unknown-revision gate results, binaries and observations omitted.

Limitations: One canonical run per condition, host-specific account behavior.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/earlier-evidence/phase6c

Purpose: All 12 canonical lock/unlock controls

Original: `phase6c-artifacts.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `5530125f167090cf134038f3ab882623c374b07d`.

Selected: 12 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Old observers/binaries omitted in favor of later topology below.

Limitations: Historical wait-TID instrumentation invalid. Hot-path gettid perturbs performance.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/earlier-evidence/phase6c-topology

Purpose: Later two scoped lock/unlock identity observations

Original: `phase6c-complete.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `0fdc1b2eb7fd728363cc05e81737a3c0658518fa`.

Selected: 329 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Global ps, binaries and redundant PID files omitted.

Limitations: Original summary parser has empty enter sets/false correlations. Derived topology reads result.json directly. Wait field remains invalid. Archive name does not mean complete matrix.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/gate-failures/environment-c

Purpose: Test portability and race-prerequisite failure evidence

Original: `phase8-gate-failures.tar.gz` (private archive basename/location label, not a public URL).

Execution revision: `9e153d8e2dea763d567ae860904869354cb42e7a; b2bccf754f40da86fe7b2b57a229c01ab0b3b3db`.

Selected: 12 files, individually enumerated in FILE-MANIFEST.json.

Omitted: All generated ELF binaries omitted.

Limitations: Gate failures, not benchmark results. Singleton batches were valid; missing race prerequisites were tooling.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

### results/gate-failures/environment-a

Purpose: Concrete restricted-environment setup failure

Original: `private results/phase2/backend-integration-probe.json` (private archive basename/location label, not a public URL).

Execution revision: `As recorded in probe; do not infer a missing execution SHA`.

Selected: 1 files, individually enumerated in FILE-MANIFEST.json.

Omitted: Unrelated blocking results and later host probe omitted.

Limitations: Security failure, not an io_uring performance result.

Metadata redactions: see REDACTIONS.md and per-file entries. Numeric JSON values, settings, revisions and timestamps are preserved.

## Source archives

| Private archive basename | Original archive SHA-256 |
| --- | --- |
| phase7-artifacts.tar.gz | `f809624d9997570f94406cc4fb68368507e4be2481e6daf9bde24bd0698d0ae5` |
| phase8-artifacts-complete.tar.gz | `37a678aa0cd5c2be668d2e80d82a83ae72eaed00ce15c8d0c544fcc2a68f7932` |
| phase2-matrix.tar.gz | `1b2c082fc62fb423c6729c17e286942c2beeeecda99646f4c54cd6f14e807b3f` |
| phase6-artifacts.tar.gz | `3d648c722a44bdeecf2dc7dfcccd476b080de7e2c921ff6cac01dc965721008c` |
| phase6b-gate.tar.gz | `4c06ca490b66d101afe86f07d4ff48d5f07e1ad38d23778f6fa740294cfe2374` |
| phase6c-artifacts.tar.gz | `8acc10e36dc02ebff30a44567d410cebf6a844a0efd33a1547a18b581e063198` |
| phase6c-complete.tar.gz | `b535154bfefc577d8d4e3fcd03eabfc7546b2e8eb14ede2028c2f52709277455` |
| phase8-gate-failures.tar.gz | `1210467e84f5717c49fe419c6949a8b6b781dcf9a18539ff62eb05120885d7fe` |

No original archive is included. Public SHA256SUMS covers all files under results except itself; metadata and scoped derivatives have their own public hashes.

All 30 Phase 7 and 30 Phase 8 canonical JSON are included. Complete historical controls are retained within selected groups; there is no favorable-run selection. Other early local documents were not imported: see [provenance](../docs/provenance.md).
