# Experimental evidence

Start with the [manifest](MANIFEST.md) for revisions, selection rules and limitations.

| Set | Where to start | What it contains |
| --- | --- | --- |
| Final pre-upstream controls | [summary](pre-upstream-controls/summary.md), [admission curve](pre-upstream-controls/bounded-blocking.csv), [raw runs](pre-upstream-controls/runs/), [perf limitation](pre-upstream-controls/perf/README.md) | All 24 same-revision nonresident-start controls; scoped perf evidence, not raw system-wide profiles |
| Environment B, Phase 7 | [curve.csv](environment-b/phase7/curve.csv), [raw runs](environment-b/phase7/runs/) | All 30 resident worker-curve/control runs, including unfavorable and noisy repetitions |
| Environment C, Phase 8 | [resident curve](environment-c/phase8/resident-curve.csv), [nonresident-start comparison](environment-c/phase8/slow-thread-pressure.csv), [raw runs](environment-c/phase8/runs/) | All 30 canonical runs plus separate scoped identity observations |
| Earlier evidence | [earlier-evidence/](earlier-evidence/) | Bounded-blocking comparison, failed batching hypothesis, invalid worker control and topology diagnostics |
| Failed gates | [gate-failures/](gate-failures/) | Setup policy failure and test/provisioning portability failures, not performance results |

Raw JSON takes precedence over prose or CSV. Canonical performance and observer
results are different experiments. In particular, the default heavy observation
has insufficient samples for a quantitative worker census. Copies of original
erroneous fields remain accompanied by corrections, not silently repaired.

[REDACTIONS.md](REDACTIONS.md) records metadata changes; [FILE-MANIFEST.json](FILE-MANIFEST.json)
records original/current checksums and file provenance. Verify result files with:

```bash
cd results
sha256sum --quiet -c SHA256SUMS
```
