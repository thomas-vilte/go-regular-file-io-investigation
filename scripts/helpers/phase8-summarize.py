#!/usr/bin/env python3
"""Summarize validated Phase 8 artifacts without dropping repetitions."""
import csv
import json
from pathlib import Path
from statistics import median
import sys


SLOW_MODES = ["blocking", "normal"]
RESIDENT_MODES = ["blocking", "normal", "U1", "U2", "U4", "U8", "U16", "default"]


def value(obj, *keys):
    for key in keys:
        if not isinstance(obj, dict):
            return None
        obj = obj.get(key)
    return obj


def residency(r, name):
    return value(r, "dataset", name, "resident_ppm")


def read_run(root, part, mode, rep):
    base = root / "runs" / f"r{rep}-{part}-{mode}"
    check = base.with_suffix(".check.txt")
    if not check.exists() or not check.read_text().startswith("phase8check: ok"):
        raise ValueError(f"not validated: {base}")
    r = json.loads(base.with_suffix(".json").read_text())
    if r.get("status") != "ok":
        raise ValueError(f"failed: {base}")
    before = value(r, "process", "rusage_before")
    end = value(r, "process", "rusage_end")
    wall = r["duration_ns"] / 1e9
    user = (end["UserNS"] - before["UserNS"]) / 1e9
    system = (end["SystemNS"] - before["SystemNS"]) / 1e9
    process = user + system
    work = r["work"]
    peak = value(r, "runtime", "peak_observed_by_internal_sampler") or {}
    io = r.get("io_uring", {})
    before_measurement_ppm = residency(r, "residency_before_measurement")
    return {
        "part": part, "mode": mode, "repetition": rep,
        "MiB_s": work["throughput_bytes_per_s"] / 2**20,
        "wall_s": wall, "user_cpu_s": user, "system_cpu_s": system,
        "process_cpu_s": process, "effective_cores": process / wall,
        "cpu_us_op": process * 1e6 / work["operations"],
        "residency_before_ppm": residency(r, "residency_before"),
        "residency_after_dontneed_ppm": residency(r, "residency_after_dontneed"),
        "residency_after_prewarm_ppm": residency(r, "residency_after_prewarm"),
        "residency_before_measurement_ppm": before_measurement_ppm,
        # This is the existing harness label boundary, not a claim of a cold
        # device cache. It lets the summary state when Part A missed its
        # intended mostly-nonresident starting condition.
        "slow_start_mostly_nonresident": before_measurement_ppm is not None and before_measurement_ppm <= 50_000,
        "peak_process_tasks": value(r, "process", "external_observer", "max_observed_threads"),
        "peak_go_threads": peak.get("/sched/threads/total:threads"),
        "peak_not_in_go": peak.get("/sched/goroutines/not-in-go:goroutines"),
        "peak_runnable": peak.get("/sched/goroutines/runnable:goroutines"),
        "voluntary_cs": end["VoluntaryCS"] - before["VoluntaryCS"],
        "involuntary_cs": end["InvoluntaryCS"] - before["InvoluntaryCS"],
        "average_submit_batch": io.get("average_submit_batch_size"),
        "max_submit_batch": io.get("submit_batch_size_max"),
        "average_cq_drain": io.get("cqes_per_completion_drain"),
        "max_cq_drain": io.get("completion_batch_size_max"),
        "submit_enter_calls": io.get("submit_enter_calls"),
        "wait_enter_calls": io.get("wait_enter_calls"),
        "enter_op": io.get("total_enter_calls_per_operation"),
        "force_async": io.get("force_async"),
        "lock_submitter_thread": io.get("lock_submitter_thread"),
        "record_issuer_tids": io.get("record_issuer_tids"),
        "worker_config": json.dumps(io.get("iowq_worker_config"), sort_keys=True),
    }


def summary_row(rows, mode, part, metrics=None):
    if metrics is None:
        metrics = [
        "MiB_s", "effective_cores", "cpu_us_op", "peak_process_tasks",
        "peak_go_threads", "peak_not_in_go", "peak_runnable", "voluntary_cs",
        "involuntary_cs", "enter_op",
        ]
    out = {"row_type": "summary", "part": part, "mode": mode, "repetition": ""}
    for metric in metrics:
        values = [r[metric] for r in rows]
        if any(v is None for v in values):
            out.update({f"{metric}_median": None, f"{metric}_min": None, f"{metric}_max": None})
        else:
            out.update({f"{metric}_median": median(values), f"{metric}_min": min(values), f"{metric}_max": max(values)})
    return out


def write_csv(path, rows):
    fields = []
    for row in rows:
        for key in row:
            if key not in fields:
                fields.append(key)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def summarize(root):
    root = Path(root)
    slow = [read_run(root, "slow", mode, rep) for mode in SLOW_MODES for rep in range(1, 4)]
    resident = [read_run(root, "resident", mode, rep) for mode in RESIDENT_MODES for rep in range(1, 4)]
    per_run = [{"row_type": "run", **row} for row in slow + resident]
    write_csv(root / "per-run.csv", per_run)

    slow_out = [{"row_type": "run", **row} for row in slow]
    slow_metrics = [
        "MiB_s", "residency_before_ppm", "residency_after_dontneed_ppm",
        "residency_before_measurement_ppm", "peak_process_tasks",
        "peak_go_threads", "peak_not_in_go", "peak_runnable", "effective_cores",
        "voluntary_cs", "involuntary_cs",
    ]
    for mode in SLOW_MODES:
        group = [r for r in slow if r["mode"] == mode]
        row = summary_row(group, mode, "slow", slow_metrics)
        row["slow_start_precondition"] = (
            "all_repetitions_mostly_nonresident"
            if all(r["slow_start_mostly_nonresident"] for r in group)
            else "not_reproduced_all_runs"
        )
        slow_out.append(row)
    write_csv(root / "slow-thread-pressure.csv", slow_out)

    blocking = median(r["MiB_s"] for r in resident if r["mode"] == "blocking")
    forced_default = median(r["MiB_s"] for r in resident if r["mode"] == "default")
    curve = []
    for mode in RESIDENT_MODES:
        row = summary_row([r for r in resident if r["mode"] == mode], mode, "resident")
        row["unbounded_worker_max"] = mode[1:] if mode.startswith("U") else ""
        row["throughput_over_blocking"] = row["MiB_s_median"] / blocking
        row["throughput_over_forced_default"] = row["MiB_s_median"] / forced_default
        curve.append(row)
    write_csv(root / "resident-curve.csv", curve)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: phase8-summarize.py <artifact-dir>")
    summarize(sys.argv[1])
