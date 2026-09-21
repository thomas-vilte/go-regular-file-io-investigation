# Reproduction tools

Current reproduction: [phase8-cross-host-reproduction.sh](phase8-cross-host-reproduction.sh).
The filename stays unchanged because its Git introduction is part of the revision
gate. See [the runbook](../docs/reproduction.md). The runner invokes the relocated
summary helper; no workload or measurement policy changed.

Current helpers in [helpers/](helpers/):

- `phase8-summarize.py`: aggregation and validation of Phase 8 artifacts.
- `phase7-summarize.py`: aggregation of the retained Phase 7 results.
- `test_phase8_summary.py`, `test_phase7_summary.py`: synthetic aggregation tests.
- `phase4-diskstats.sh`: read-only device-counter helper.

Run Python tools with `python3`; no Python dependency packages are required.
The pre-existing leading `1` before the Phase 7 helper's shebang is recorded in
the cleanup note and left unchanged here. Python parses it as a harmless standalone
expression; direct executable invocation is not the documented path.

[Legacy notes](legacy/README.md) describe the excluded host-specific runners.
No temporary export-builder, debug script or generated executable is shipped.
