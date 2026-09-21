#!/usr/bin/env bash
# Portable cross-host mechanism reproduction. It makes no privileged changes.
set -euo pipefail

[[ $# -eq 2 ]] || { echo "usage: GO_TOOL=/path/to/go $0 <new-output-dir> <experiment-owned-data-file>" >&2; exit 2; }
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"
out=$1
input=$2
readonly runner=scripts/phase8-cross-host-reproduction.sh

if [[ -n ${GO_TOOL:-} ]]; then
  go_tool=$GO_TOOL
else
  go_tool=$(command -v go || true)
fi
[[ -n $go_tool && -x $go_tool ]] || { echo "GO_TOOL is not executable and no go was found on PATH" >&2; exit 1; }
go_tool=$(readlink -f "$go_tool")
command -v python3 >/dev/null || { echo "python3 is required for artifact summaries" >&2; exit 1; }
command -v git >/dev/null || { echo "git is required to record the harness revision" >&2; exit 1; }
ambient_cgo_enabled=${CGO_ENABLED-<unset>}
go_cgo_enabled=$("$go_tool" env CGO_ENABLED)
go_cc=$("$go_tool" env CC)
# Resolve a quoted compiler command without evaluating shell code. The
# preflight below validates the complete CC setting used by Go.
cc_executable=$(python3 -c 'import shlex,sys; words=shlex.split(sys.argv[1]); print(words[0] if words else "")' "$go_cc")
cc_resolved=$(command -v "$cc_executable" 2>/dev/null || true)

# A commit cannot name itself. Resolve the commit that introduced the runner
# and require it in HEAD's ancestry, while recording the actual execution SHA.
required_revision=$(git log --diff-filter=A --format=%H -- "$runner" | tail -n 1)
[[ -n $required_revision ]] || { echo "Phase 8 runner must be committed before use" >&2; exit 1; }
git merge-base --is-ancestor "$required_revision" HEAD || { echo "required Phase 8 runner revision is not an ancestor of HEAD" >&2; exit 1; }
harness_revision=$(git rev-parse HEAD)
git diff --quiet
git diff --cached --quiet
[[ ! -e $out ]] || { echo "output directory already exists: $out" >&2; exit 2; }

# DONTNEED is permitted only for inputs under this repository's ignored data/
# directory; iobaseline independently enforces the same rule.
data_root=$(realpath -m "$repo_root/data")
input_abs=$(realpath -m "$input")
case $input_abs in
  "$data_root"/*) ;;
  *) echo "input must be under experiment-owned $data_root for DONTNEED" >&2; exit 2 ;;
esac

mkdir -p "$out/runs"
input_created=false
if [[ ! -e $input_abs ]]; then
  mkdir -p "$(dirname "$input_abs")"
  "$go_tool" build -buildvcs=false -o "$out/genfile" ./cmd/genfile
  "$out/genfile" -output "$input_abs" -size=$((512 * 1024 * 1024)) -seed=31908 >"$out/dataset-generation.txt"
  input_created=true
fi
[[ -f $input_abs ]] || { echo "input is not a regular file: $input_abs" >&2; exit 1; }
input_size=$(stat -c %s "$input_abs")
(( input_size >= 512 * 1024 * 1024 )) || { echo "input must be at least 512 MiB" >&2; exit 1; }

online_cpus=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 1)
[[ $online_cpus =~ ^[1-9][0-9]*$ ]] || online_cpus=1
slow_gomax=$online_cpus
(( slow_gomax > 4 )) && slow_gomax=4
resident_gomax=$online_cpus
(( resident_gomax > 8 )) && resident_gomax=8
# PHASE8_GOMAXPROCS applies to both parts. Per-part values take precedence so
# a host can override min(4, CPUs) and min(8, CPUs) independently.
if [[ -n ${PHASE8_GOMAXPROCS:-} ]]; then
  [[ $PHASE8_GOMAXPROCS =~ ^[1-9][0-9]*$ ]] || { echo "PHASE8_GOMAXPROCS must be a positive integer" >&2; exit 2; }
  slow_gomax=$PHASE8_GOMAXPROCS
  resident_gomax=$PHASE8_GOMAXPROCS
fi
if [[ -n ${PHASE8_SLOW_GOMAXPROCS:-} ]]; then
  [[ $PHASE8_SLOW_GOMAXPROCS =~ ^[1-9][0-9]*$ ]] || { echo "PHASE8_SLOW_GOMAXPROCS must be a positive integer" >&2; exit 2; }
  slow_gomax=$PHASE8_SLOW_GOMAXPROCS
fi
if [[ -n ${PHASE8_RESIDENT_GOMAXPROCS:-} ]]; then
  [[ $PHASE8_RESIDENT_GOMAXPROCS =~ ^[1-9][0-9]*$ ]] || { echo "PHASE8_RESIDENT_GOMAXPROCS must be a positive integer" >&2; exit 2; }
  resident_gomax=$PHASE8_RESIDENT_GOMAXPROCS
fi

{
  date -u +%FT%TZ
  printf 'repo_root=%s\nrequired_revision=%s\nharness_revision=%s\n' "$repo_root" "$required_revision" "$harness_revision"
  printf 'git_status_short:\n'
  git status --short
  printf 'go_executable=%s\n' "$go_tool"
  "$go_tool" version
  printf 'ambient_CGO_ENABLED=%s\n' "$ambient_cgo_enabled"
  printf 'go_env_CGO_ENABLED=%s\ngo_env_CC=%s\nresolved_CC=%s\n' "$go_cgo_enabled" "$go_cc" "${cc_resolved:-unavailable}"
  uname -a
  printf 'online_cpus=%s\nslow_gomaxprocs=%s\nresident_gomaxprocs=%s\n' "$online_cpus" "$slow_gomax" "$resident_gomax"
  printf 'PHASE8_GOMAXPROCS=%s\nPHASE8_SLOW_GOMAXPROCS=%s\nPHASE8_RESIDENT_GOMAXPROCS=%s\n' "${PHASE8_GOMAXPROCS:-}" "${PHASE8_SLOW_GOMAXPROCS:-}" "${PHASE8_RESIDENT_GOMAXPROCS:-}"
  printf '\n[lscpu]\n'; lscpu 2>&1 || true
  printf '\n[free -h]\n'; free -h 2>&1 || true
  printf '\n[meminfo]\n'; grep -E '^(MemTotal|MemAvailable|SwapTotal|SwapFree|HugePages_Total):' /proc/meminfo 2>&1 || true
  printf '\n[dataset stat]\n'; stat "$input_abs" 2>&1 || true
  printf '\n[findmnt dataset]\n'; findmnt -T "$input_abs" 2>&1 || true
  printf '\n[df -T dataset]\n'; df -T "$input_abs" 2>&1 || true
  printf '\n[lsblk]\n'; lsblk -o NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS,ROTA,MODEL 2>&1 || true
  printf '\n[cgroup]\n'; cat /proc/self/cgroup 2>&1 || true
  printf '\n[cgroup v2 mounts]\n'; findmnt -t cgroup2 2>&1 || true
  printf '\n[io_uring_disabled]\n'; cat /proc/sys/kernel/io_uring_disabled 2>&1 || true
  printf '\n[security]\n'; grep -E '^(NoNewPrivs|Seccomp|Seccomp_filters):' /proc/self/status 2>&1 || true
  printf '\n[filesystem optional]\n'; command -v btrfs >/dev/null && btrfs filesystem usage "$(findmnt -T "$input_abs" -no TARGET)" 2>&1 || true
} >"$out/environment.txt"

if command -v sha256sum >/dev/null; then
  digest=$(sha256sum "$input_abs" | awk '{print $1}')
elif command -v shasum >/dev/null; then
  digest=$(shasum -a 256 "$input_abs" | awk '{print $1}')
else
  echo "no SHA-256 command available" >&2
  exit 1
fi
printf 'path=%s\ncreated_by_runner=%s\nsize_bytes=%s\nsha256=%s\n' "$input_abs" "$input_created" "$input_size" "$digest" >"$out/dataset.txt"

{
  printf 'go_executable=%s\n' "$go_tool"
  "$go_tool" version
  printf '\n[race detector prerequisites]\n'
  printf 'ambient_CGO_ENABLED=%s\n' "$ambient_cgo_enabled"
  printf 'go_env_CGO_ENABLED=%s\n' "$go_cgo_enabled"
  printf 'go_env_CC=%s\n' "$go_cc"
  printf 'resolved_CC=%s\n' "${cc_resolved:-unavailable}"
  printf '\n[perf]\n'
  if command -v perf >/dev/null; then perf --version; else echo 'unavailable'; fi
  printf '\n[perf_event_paranoid]\n'; cat /proc/sys/kernel/perf_event_paranoid 2>&1 || true
  printf '\n[kptr_restrict]\n'; cat /proc/sys/kernel/kptr_restrict 2>&1 || true
} >"$out/capabilities.txt"

race_preflight_log="$out/race-preflight.txt"
if [[ -z $cc_resolved ]]; then
  printf 'Go selected CC=%q, but its executable was not found on PATH.\n' "$go_cc" >"$race_preflight_log"
  echo "Phase 8 requires a usable C compiler for its mandatory Go race gate; install gcc and libc6-dev on Ubuntu. See $race_preflight_log" >&2
  exit 1
fi
race_preflight_dir="$out/race-preflight"
mkdir "$race_preflight_dir"
printf 'module phase8racepreflight\n\ngo 1.27.1\n' >"$race_preflight_dir/go.mod"
printf 'package phase8racepreflight\n/*\n#include <stdlib.h>\n*/\nimport "C"\n\nfunc Value() C.int { return 0 }\n' >"$race_preflight_dir/cgo.go"
printf 'package phase8racepreflight\n\nimport "testing"\n\nfunc TestValue(t *testing.T) { _ = Value() }\n' >"$race_preflight_dir/cgo_test.go"
if ! (cd "$race_preflight_dir" && CGO_ENABLED=1 "$go_tool" test -count=1 -race -buildvcs=false .) >"$race_preflight_log" 2>&1; then
  echo "Phase 8 requires CGO_ENABLED=1 and a usable C compiler/development headers for its mandatory Go race gate; install gcc and libc6-dev on Ubuntu. See $race_preflight_log" >&2
  exit 1
fi

"$go_tool" build -buildvcs=false -o "$out/iobaseline" ./cmd/iobaseline
"$go_tool" build -buildvcs=false -o "$out/phase6check" ./cmd/phase6check
"$go_tool" build -buildvcs=false -o "$out/iouringprobe" ./cmd/iouringprobe
"$go_tool" test -count=1 -buildvcs=false ./... >"$out/tests-normal.txt" 2>&1
"$go_tool" test -count=1 -buildvcs=false -v ./internal/uring >"$out/tests-kernel.txt" 2>&1
CGO_ENABLED=1 "$go_tool" test -count=1 -race -buildvcs=false ./internal/uring ./cmd/iobaseline >"$out/tests-race.txt" 2>&1
"$out/iouringprobe" -harness-revision="$harness_revision" >"$out/setup-probe.json"
python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get("status")=="available" and not r.get("close_error") else 1)' "$out/setup-probe.json" || {
  echo "io_uring setup unavailable; see $out/setup-probe.json" >&2
  exit 1
}

# A small real READ gate follows setup. It checks data, completion accounting,
# and buffer lifetime before the canonical matrix.
"$out/iobaseline" -backend=uring -operation=readat -file="$input_abs" -file-access=shared_fd \
  -buffer-bytes=4096 -concurrency=32 -max-outstanding=4 -gomaxprocs="$slow_gomax" \
  -operations=128 -seed=31908 -cache-precondition=prewarm \
  -cache-state=residency_verified_mostly_resident -runtime-sample-interval=10ms \
  -uring-record-issuer-tids=false -harness-revision="$harness_revision" >"$out/smoke.json" 2>"$out/smoke.stderr.txt"
"$out/phase6check" -file "$out/smoke.json" >"$out/smoke.check.txt" 2>&1

orders=(
  'slow-blocking resident-U1 resident-U8 slow-normal resident-normal resident-U2 resident-U16 resident-blocking resident-default resident-U4'
  'resident-U16 slow-normal resident-U1 resident-default slow-blocking resident-U4 resident-normal resident-U2 resident-U8 resident-blocking'
  'resident-default resident-U4 slow-blocking resident-U16 resident-U8 slow-normal resident-U1 resident-normal resident-U2 resident-blocking'
)
printf '%s\n' "${orders[@]}" >"$out/planned-order.txt"

run_one() {
  local rep=$1 part=$2 mode=$3 gomax=$4
  local base="$out/runs/r${rep}-${part}-${mode}"
  local cmd=("$out/iobaseline" -operation=readat -file="$input_abs" -file-access=shared_fd
    -buffer-bytes=1048576 -concurrency=1000 -max-outstanding=1000 -gomaxprocs="$gomax"
    -duration=5s -warmup=0s -seed=31908 -runtime-sample-interval=10ms
    -uring-record-issuer-tids=false -harness-revision="$harness_revision")
  if [[ $part == slow ]]; then
    cmd+=(-cache-precondition=dontneed -cache-state=residency_after_dontneed_observed -allow-fadvise-dontneed)
  else
    cmd+=(-cache-precondition=prewarm -cache-state=residency_verified_mostly_resident)
  fi
  case $mode in
    blocking) cmd+=(-backend=blocking) ;;
    normal) cmd+=(-backend=uring) ;;
    default) cmd+=(-backend=uring -uring-force-async -uring-lock-submitter-thread) ;;
    U1|U2|U4|U8|U16) cmd+=(-backend=uring -uring-force-async -uring-lock-submitter-thread "-uring-unbounded-workers=${mode#U}") ;;
    *) echo "invalid Phase 8 mode: $mode" >&2; return 1 ;;
  esac
  printf '%q ' "${cmd[@]}" >"$base.command.txt"
  printf '\n' >>"$base.command.txt"
  printf 'rep=%s part=%s mode=%s\n' "$rep" "$part" "$mode" | tee -a "$out/execution-order.txt"
  [[ $part != slow ]] || cat /proc/diskstats >"$base.diskstats-before.txt"
  "${cmd[@]}" >"$base.json" 2>"$base.stderr.txt"
  [[ $part != slow ]] || cat /proc/diskstats >"$base.diskstats-after.txt"
  "$out/phase6check" -file "$base.json" -phase8-part="$part" -phase8-mode="$mode" \
    -phase8-gomaxprocs="$gomax" -revision="$harness_revision" >"$base.check.txt" 2>&1
}

for index in 0 1 2; do
  rep=$((index + 1))
  read -r -a order <<<"${orders[$index]}"
  for item in "${order[@]}"; do
    part=${item%%-*}
    mode=${item#*-}
    if [[ $part == slow ]]; then
      run_one "$rep" "$part" "$mode" "$slow_gomax"
    else
      run_one "$rep" "$part" "$mode" "$resident_gomax"
    fi
  done
done

python3 scripts/helpers/phase8-summarize.py "$out"
echo "Phase 8 canonical reproduction complete. Run the four separate worker observations from the runbook."
