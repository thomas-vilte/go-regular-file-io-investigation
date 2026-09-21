"""Pure synthetic artifact test; does not execute any I/O workload."""
import csv
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('summary', Path(__file__).with_name('phase7-summarize.py'))
summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(summary)


class SummaryTest(unittest.TestCase):
    def test_all_repetitions_and_cpu_units(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'runs').mkdir()
            modes = ['blocking','normal','U1','U2','U4','U8','U16','U32','U64','default']
            for mode in modes:
                for rep in range(1, 4):
                    base = root / 'runs' / f'r{rep}-{mode}'
                    r = {'status':'ok', 'duration_ns':5e9,
                         'work':{'operations':100, 'throughput_bytes_per_s':rep*2**20},
                         'process':{'rusage_before':{'UserNS':1e9,'SystemNS':1e9,'VoluntaryCS':10,'InvoluntaryCS':20},
                                    'rusage_end':{'UserNS':2e9,'SystemNS':5e9,'VoluntaryCS':30,'InvoluntaryCS':25},
                                    'external_observer':{'max_observed_threads':9}},
                         'runtime':{'peak_observed_by_internal_sampler':{}}}
                    base.with_suffix('.json').write_text(json.dumps(r))
                    base.with_suffix('.check.txt').write_text('phase6check: ok synthetic\n')
            summary.summarize(root)
            with (root / 'curve.csv').open() as f:
                rows = list(csv.DictReader(f))
            self.assertEqual(len(rows), 10)
            self.assertEqual(float(rows[0]['MiB_s_median']), 2)
            self.assertEqual(float(rows[0]['MiB_s_min']), 1)
            self.assertEqual(float(rows[0]['MiB_s_max']), 3)
            self.assertEqual(float(rows[0]['effective_cores_median']), 1)
            self.assertEqual(float(rows[0]['cpu_us_op_median']), 50000)
            self.assertEqual(rows[0]['peak_go_threads_median'], '')
            (root / 'runs' / 'r3-U64.check.txt').unlink()
            with self.assertRaises(FileNotFoundError):
                summary.summarize(root)


if __name__ == '__main__':
    unittest.main()
