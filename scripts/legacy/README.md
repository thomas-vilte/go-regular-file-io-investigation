# Historical runner exclusions

The primary runnable entry point is `phase8-cross-host-reproduction.sh`. The
following original files were deliberately not copied:

- `phase4-profile.sh`
- `phase4-observe-workers.sh`
- `phase6-worker-curve.sh`
- `phase7-single-issuer-worker-curve.sh`

They contain private home-path defaults and/or ancestry checks tied to omitted
private history. They were not silently edited into new portable implementations.
Their original hashes are in the [source manifest](../../results/SOURCE-MANIFEST.json). Commands referencing
them in historical documents are historical instructions, not executable steps
for this clean snapshot. The portable Phase 8 runner covers the cross-host matrix;
scoped worker observations are included as evidence, not reproduced by a new
observer invented for this export.

The included diskstats helper, summary scripts and tests are unchanged technical
source. No source implementation or benchmark policy was modified for publication.
