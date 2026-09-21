"""Synthetic artifact test; it never executes an I/O workload."""
import csv
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("summary", Path(__file__).with_name("phase8-summarize.py"))
summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(summary)


class SummaryTest(unittest.TestCase):
    def test_all_repetitions_and_ratios(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "runs").mkdir()
            modes = {"slow": ["blocking", "normal"], "resident": ["blocking", "normal", "U1", "U2", "U4", "U8", "U16", "default"]}
            for part, names in modes.items():
                for mode in names:
                    for rep in range(1, 4):
                        base = root / "runs" / f"r{rep}-{part}-{mode}"
                        residency = {"residency_before_measurement": {"resident_ppm": 1_000_000}}
                        if part == "slow":
                            residency.update({"residency_before": {"resident_ppm": 900_000}, "residency_after_dontneed": {"resident_ppm": 100_000}})
                        else:
                            residency["residency_after_prewarm"] = {"resident_ppm": 1_000_000}
                        result = {
                            "status": "ok", "duration_ns": 5e9,
                            "work": {"operations": 100, "throughput_bytes_per_s": rep * 2**20},
                            "dataset": residency,
                            "process": {"rusage_before": {"UserNS": 1e9, "SystemNS": 1e9, "VoluntaryCS": 10, "InvoluntaryCS": 20},
                                        "rusage_end": {"UserNS": 2e9, "SystemNS": 5e9, "VoluntaryCS": 30, "InvoluntaryCS": 25},
                                        "external_observer": {"max_observed_threads": 9}},
                            "runtime": {"peak_observed_by_internal_sampler": {}},
                        }
                        base.with_suffix(".json").write_text(json.dumps(result))
                        base.with_suffix(".check.txt").write_text("phase8check: ok synthetic\n")
            summary.summarize(root)
            with (root / "resident-curve.csv").open() as f:
                curve = list(csv.DictReader(f))
            self.assertEqual(len(curve), 8)
            self.assertEqual(float(curve[0]["MiB_s_median"]), 2)
            self.assertEqual(float(curve[0]["throughput_over_blocking"]), 1)
            with (root / "slow-thread-pressure.csv").open() as f:
                slow = list(csv.DictReader(f))
            self.assertEqual(len(slow), 8)
            self.assertEqual(float(slow[-1]["MiB_s_median"]), 2)
            (root / "runs" / "r3-resident-U16.check.txt").unlink()
            with self.assertRaises(ValueError):
                summary.summarize(root)


if __name__ == "__main__":
    unittest.main()
