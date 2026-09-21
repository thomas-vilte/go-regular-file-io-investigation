# Rebuildable input

No dataset is included. The experiment-owned source file was 536870912 bytes
(512 MiB). The generator and workload use seed 31908. The C generation log is
retained in the first failed-gate directory; the later successful run correctly
records `created_by_runner=false` because it reused that file.

For a future independently authorized reproduction, the unchanged Phase 8 runner
generates a missing file itself. The equivalent generator command is:

```bash
"$GO_TOOL" run -buildvcs=false ./cmd/genfile \
  -output=./data/readat-512MiB.bin -size=536870912 -seed=31908
```

Create the experiment-owned `data/` directory first. Do not use `-force` on an
existing file. This command was not executed during export preparation.

Recorded Environment C SHA-256 from `environment-c/phase8/dataset.txt`:

```
ac61f5b292b9d59c17b3d0fb45ec6fd75970e021e7bfa324ad5332b5df163583
```

The selected Phase 7 archive does not contain a standalone B dataset digest.
Do not assign C's digest to B without evidence. Size, generator and documented
seed permit reconstruction, but no new dataset was generated or measured here.
