# Historical investigation notes

The [summary](../summary.md) and [evidence review](../evidence-review.md) are the
current interpretation. These notes preserve the experiments, mistaken hypotheses
and corrections; they are not all current run instructions.

- [Phase 4: attribution](io-uring-phase-4-attribution.md)
- [Phase 5: forced async](io-uring-phase-5-force-async.md)
- [Phase 6: invalid bounded-worker curve](io-uring-phase-6-iowq-scaling.md)
- [Phase 6B: worker-account controls](io-uring-phase-6b-worker-control.md)
- [Phase 6C: issuer contexts](io-uring-phase-6c-issuer-context.md)
- [Phase 7: stable-issuer scaling](io-uring-phase-7-single-issuer-scaling.md)

[Phase 8 reproduction](../reproduction.md) remains the supported entry point.
[Provenance](../provenance.md) lists missing early documents; they were not
silently imported or recreated.

Keep these qualifications when reading the historical material:

- Phase 4 user-only perf data did not establish kernel CPU attribution. The
  final privileged attempt also left kernel symbols unresolved.
- Early observers sampled the wrong process. Missing worker names did not
  establish absence of workers.
- Phase 6 is an invalid worker-count curve: its bounded account setting did not
  control the relevant population. Later account controls narrowed the claim.
- Phase 6C historical wait-TID instrumentation is invalid; submit-TID evidence
  is separate. Derived topology reads the underlying result/task data rather
  than relying on erroneous summary-parser output.
- Environment C's default heavy observation has insufficient samples for a
  quantitative worker census.
- C's singleton-batch test and race-toolchain failures were gate portability
  defects, not performance results.
- Some early throughput prose mislabeled decimal-rescaled values as GiB/s.
  Use the current summary's MiB/s or raw bytes/s; GiB/s = MiB/s / 1024.
- Generic raw JSON text claiming no per-I/O allocation is inaccurate for uring
  operation bookkeeping. Original records remain intact.
