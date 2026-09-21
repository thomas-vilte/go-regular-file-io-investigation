1#!/usr/bin/env python3
"""Summarize validated Phase 7 runs; never silently drop a repetition."""
import csv
import json
from pathlib import Path
from statistics import median
import sys


def summarize(root):
    rows = []
    modes = ['blocking', 'normal', 'U1', 'U2', 'U4', 'U8', 'U16', 'U32', 'U64', 'default']
    for mode in modes:
        for rep in range(1, 4):
            base = root / 'runs' / f'r{rep}-{mode}'
            if not base.with_suffix('.check.txt').read_text().startswith('phase6check: ok'):
                raise ValueError(f'not validated: {base}')
            r = json.loads(base.with_suffix('.json').read_text())
            if r['status'] != 'ok':
                raise ValueError(f'failed: {base}')
            p = r['process']
            before, end = p['rusage_before'], p['rusage_end']
            user = (end['UserNS'] - before['UserNS']) / 1e9
            system = (end['SystemNS'] - before['SystemNS']) / 1e9
            wall = r['duration_ns'] / 1e9
            ops = r['work']['operations']
            peak = r['runtime']['peak_observed_by_internal_sampler']
            i = r.get('io_uring', {})
            row = dict(mode=mode, repetition=rep, MiB_s=r['work']['throughput_bytes_per_s']/2**20,
                       wall_s=wall, user_cpu_s=user, system_cpu_s=system, process_cpu_s=user+system,
                       effective_cores=(user+system)/wall, cpu_us_op=(user+system)*1e6/ops,
                       peak_tasks=p['external_observer']['max_observed_threads'],
                       peak_go_threads=peak.get('/sched/threads/total:threads'),
                       peak_not_in_go=peak.get('/sched/goroutines/not-in-go:goroutines'),
                       peak_runnable=peak.get('/sched/goroutines/runnable:goroutines'),
                       voluntary_cs=end['VoluntaryCS']-before['VoluntaryCS'],
                       involuntary_cs=end['InvoluntaryCS']-before['InvoluntaryCS'])
            for key in ['average_submit_batch_size','submit_batch_size_max','cqes_per_completion_drain',
                        'completion_batch_size_max','submit_enter_calls','wait_enter_calls',
                        'total_enter_calls_per_operation','force_async','lock_submitter_thread','record_issuer_tids']:
                row[key] = i.get(key)
            row['worker_config'] = json.dumps(i.get('iowq_worker_config'), sort_keys=True)
            rows.append(row)
    with (root / 'per-run.csv').open('w') as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    anchors = {m: median(r['MiB_s'] for r in rows if r['mode'] == m) for m in ['blocking', 'default']}
    aggregates = []
    keys = ['MiB_s', 'effective_cores', 'cpu_us_op', 'peak_tasks', 'peak_go_threads',
            'voluntary_cs', 'involuntary_cs', 'total_enter_calls_per_operation']
    for mode in modes:
        group = [r for r in rows if r['mode'] == mode]
        a = {'mode': mode}
        for key in keys:
            values = [r[key] for r in group]
            for name, fn in [('median', median), ('min', min), ('max', max)]:
                a[f'{key}_{name}'] = fn(values) if all(v is not None for v in values) else None
        a['throughput_over_blocking'] = a['MiB_s_median'] / anchors['blocking']
        a['throughput_over_forced_default'] = a['MiB_s_median'] / anchors['default']
        aggregates.append(a)
    with (root / 'curve.csv').open('w') as f:
        writer = csv.DictWriter(f, fieldnames=list(aggregates[0]))
        writer.writeheader()
        writer.writerows(aggregates)


if __name__ == '__main__':
    summarize(Path(sys.argv[1]))
