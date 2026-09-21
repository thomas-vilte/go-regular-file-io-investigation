#!/usr/bin/env bash
# Snapshot block-device and mount state before or after a Phase 4 run.
set -euo pipefail

if [[ $# != 3 ]]; then
  echo "usage: $0 <experiment-owned-file> <output-file> <before|after>" >&2
  exit 2
fi

input=$1
out=$2
label=$3
case $label in before|after) ;; *) echo "label must be before or after" >&2; exit 2;; esac
[[ -f $input ]] || { echo "input unavailable: $input" >&2; exit 1; }
[[ ! -e $out ]] || { echo "refusing to overwrite: $out" >&2; exit 1; }

{
  echo "label=$label"
  date -u +%FT%TZ
  findmnt -T "$input" -no TARGET,SOURCE,FSTYPE,OPTIONS || true
  cat /proc/diskstats
  lsblk -o NAME,KNAME,PKNAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS || true
} >"$out"
