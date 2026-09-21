// phase6check rejects Phase 6 result JSON that does not meet the resident and
// io_uring reconciliation gates. It intentionally does not summarize results.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	path := flag.String("file", "", "iobaseline JSON result")
	expectedBoundedWorkers := flag.Int("expect-bounded-workers", -1, "require this configured bounded io-wq maximum")
	expectedUnboundedWorkers := flag.Int("expect-unbounded-workers", -1, "require this configured unbounded io-wq maximum")
	phase7Mode := flag.String("phase7-mode", "", "strict Phase 7 mode: blocking, normal, default, or U1..U64")
	revision := flag.String("revision", "", "expected actual harness revision for Phase 7")
	phase8Part := flag.String("phase8-part", "", "strict Phase 8 part: slow or resident")
	phase8Mode := flag.String("phase8-mode", "", "strict Phase 8 mode")
	phase8GOMAXPROCS := flag.Int("phase8-gomaxprocs", 0, "expected Phase 8 GOMAXPROCS")
	flag.Parse()
	if *path == "" {
		fatal("-file is required")
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		fatal(err.Error())
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		fatal(err.Error())
	}
	if result["status"] != "ok" {
		fatal(fmt.Sprintf("status=%v error=%v", result["status"], result["error"]))
	}
	if *phase7Mode != "" && *phase8Part != "" {
		fatal("phase7-mode and phase8-part are mutually exclusive")
	}
	if *phase7Mode != "" {
		if err := validatePhase7(result, *phase7Mode, *revision); err != nil {
			fatal(err.Error())
		}
	}
	dataset := object(result, "dataset")
	if *phase8Part != "" {
		if err := validatePhase8(result, *phase8Part, *phase8Mode, *revision, *phase8GOMAXPROCS); err != nil {
			fatal(err.Error())
		}
	} else {
		requireResident(dataset, "residency_after_prewarm")
		requireResident(dataset, "residency_before_measurement")
	}
	if uring, ok := result["io_uring"].(map[string]any); ok {
		if number(uring["operations_created"]) != number(uring["operations_admitted"])+number(uring["operations_cleaned_pre_publish"]) {
			fatal("operation admission accounting mismatch")
		}
		requireEqual(uring, "operations_admitted", "operations_queued")
		requireEqual(uring, "operations_published", "submissions")
		requireEqual(uring, "operations_published", "terminal_completions")
		requireEqual(uring, "pins_created", "pins_released")
		requireEqual(uring, "operations_published", "submit_entries_consumed")
		requireEqual(uring, "terminal_completions", "completion_cqes_drained_total")
		if number(uring["submit_entries_requested"]) < number(uring["submit_entries_consumed"]) {
			fatal("submission consumption exceeds requests")
		}
		if number(uring["io_uring_enter_calls"]) != number(uring["submit_enter_calls"])+number(uring["wait_enter_calls"]) {
			fatal("enter accounting mismatch")
		}
		if err, exists := uring["close_error"]; exists && err != "" {
			fatal(fmt.Sprintf("ring close error: %v", err))
		}
		requireZero(uring, "operation_table_entries_at_end")
		requireZero(uring, "ring_outstanding_at_end")
		requireZero(uring, "pins_remaining_at_end")
		requireZero(uring, "unknown_completion_ids")
		requireZero(uring, "duplicate_cqes")
		if *expectedBoundedWorkers >= 1 || *expectedUnboundedWorkers >= 1 {
			iowq := object(uring, "iowq_worker_config")
			requireWorkerLimit(iowq, "bounded", *expectedBoundedWorkers)
			requireWorkerLimit(iowq, "unbounded", *expectedUnboundedWorkers)
		}
	} else if *expectedBoundedWorkers >= 1 || *expectedUnboundedWorkers >= 1 {
		fatal("missing io_uring result for requested bounded workers")
	}
	if *phase8Part != "" {
		fmt.Printf("phase8check: ok %s\n", *path)
	} else {
		fmt.Printf("phase6check: ok %s\n", *path)
	}
}

func requireWorkerLimit(iowq map[string]any, account string, expected int) {
	if expected < 1 {
		return
	}
	requested, ok := iowq[account+"_worker_limit_requested"].(bool)
	if !ok || !requested {
		fatal(account + " worker limit was not recorded as requested")
	}
	if number(iowq["requested_"+account+"_worker_max"]) != uint64(expected) {
		fatal(fmt.Sprintf("requested %s worker maximum=%v, want %d", account, iowq["requested_"+account+"_worker_max"], expected))
	}
	if number(iowq["post_set_"+account+"_worker_max"]) != uint64(expected) {
		fatal(fmt.Sprintf("post-set %s worker maximum=%v, want %d", account, iowq["post_set_"+account+"_worker_max"], expected))
	}
}

func object(v map[string]any, name string) map[string]any {
	x, ok := v[name].(map[string]any)
	if !ok {
		fatal("missing object " + name)
	}
	return x
}

func requireResident(dataset map[string]any, name string) {
	if err := residentExactly(dataset, name); err != nil {
		fatal(err.Error())
	}
}

func residentExactly(dataset map[string]any, name string) error {
	v := object(dataset, name)
	if number(v["resident_ppm"]) != 1_000_000 {
		return fmt.Errorf("%s resident_ppm=%v, want 1000000", name, v["resident_ppm"])
	}
	return nil
}

func requireEqual(values map[string]any, a, b string) {
	if number(values[a]) != number(values[b]) {
		fatal(fmt.Sprintf("%s=%v != %s=%v", a, values[a], b, values[b]))
	}
}

func requireZero(values map[string]any, name string) {
	if number(values[name]) != 0 {
		fatal(fmt.Sprintf("%s=%v, want 0", name, values[name]))
	}
}

func number(v any) uint64 {
	x, ok := v.(float64)
	if !ok || x < 0 || x != float64(uint64(x)) {
		fatal(fmt.Sprintf("expected non-negative integer, got %v", v))
	}
	return uint64(x)
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "phase6check:", message)
	os.Exit(1)
}
