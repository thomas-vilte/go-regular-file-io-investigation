package main

import "testing"

func phase8Fixture(part, mode string) map[string]any {
	force := mode == "default" || mode == "U1" || mode == "U2" || mode == "U4" || mode == "U8" || mode == "U16"
	explicit := mode == "U1" || mode == "U2" || mode == "U4" || mode == "U8" || mode == "U16"
	backend := "uring"
	if mode == "blocking" {
		backend = "blocking"
	}
	worker := float64(4)
	config := map[string]any{
		"operation": "readat_random", "file_access": "shared_fd", "buffer_bytes": float64(1048576),
		"concurrency": float64(1000), "max_outstanding": float64(1000), "gomaxprocs": float64(4),
		"duration_target_ns": float64(5e9), "warmup_ns": float64(0), "fixed_operations": float64(0),
		"offset_seed": float64(31908), "latency_mode": false, "uring_record_issuer_tids": false,
		"uring_bounded_workers_set": false, "runtime_sample_interval_ns": float64(1e7),
		"backend": backend, "uring_force_async": force, "uring_lock_submitter_thread": force,
		"uring_unbounded_workers_set": explicit,
	}
	if explicit {
		config["uring_unbounded_workers"] = worker
	}
	dataset := map[string]any{"size_bytes": float64(512 << 20)}
	if part == "resident" {
		dataset["cache_precondition"] = "prewarm"
		dataset["cache_state_declared"] = "residency_verified_mostly_resident"
		dataset["residency_after_prewarm"] = map[string]any{"resident_ppm": float64(1_000_000)}
		dataset["residency_before_measurement"] = map[string]any{"resident_ppm": float64(1_000_000)}
	} else {
		dataset["cache_precondition"] = "dontneed"
		dataset["cache_state_declared"] = "residency_after_dontneed_observed"
		dataset["residency_before_measurement"] = map[string]any{"resident_ppm": float64(0)}
		dataset["fadvise_dontneed"] = map[string]any{"requested": true, "error": ""}
	}
	r := map[string]any{
		"environment": map[string]any{"harness_revision": "test"},
		"config":      config, "dataset": dataset,
	}
	if backend == "blocking" {
		return r
	}
	r["io_uring"] = map[string]any{
		"force_async": force, "lock_submitter_thread": force, "record_issuer_tids": false,
		"submit_enter_tid_count": float64(0), "wait_enter_tid_count": float64(0),
		"submit_enter_distinct_tids": []any{}, "wait_enter_distinct_tids": []any{},
		"iowq_worker_config": map[string]any{
			"query_error": "", "registration_error": "", "post_set_query_error": "",
			"iowq_worker_limit_registration_supported": true,
			"bounded_worker_limit_requested":           false,
			"unbounded_worker_limit_requested":         explicit,
			"iowq_worker_limit_registration_attempted": explicit,
			"previous_bounded_worker_max":              float64(16), "previous_unbounded_worker_max": float64(64),
		},
	}
	if explicit {
		m := r["io_uring"].(map[string]any)["iowq_worker_config"].(map[string]any)
		m["requested_bounded_worker_max"] = float64(16)
		m["post_set_bounded_worker_max"] = float64(16)
		m["requested_unbounded_worker_max"] = worker
		m["post_set_unbounded_worker_max"] = worker
	}
	return r
}

func TestPhase8ResidentValidation(t *testing.T) {
	if err := validatePhase8(phase8Fixture("resident", "U4"), "resident", "U4", "test", 4); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(map[string]any){
		func(r map[string]any) { r["config"].(map[string]any)["uring_record_issuer_tids"] = true },
		func(r map[string]any) {
			r["dataset"].(map[string]any)["residency_before_measurement"] = map[string]any{"resident_ppm": float64(999999)}
		},
		func(r map[string]any) { r["io_uring"].(map[string]any)["record_issuer_tids"] = true },
		func(r map[string]any) {
			r["io_uring"].(map[string]any)["iowq_worker_config"].(map[string]any)["post_set_unbounded_worker_max"] = float64(8)
		},
	} {
		r := phase8Fixture("resident", "U4")
		mutation(r)
		if err := validatePhase8(r, "resident", "U4", "test", 4); err == nil {
			t.Fatal("accepted invalid resident run")
		}
	}
}

func TestPhase8SlowValidation(t *testing.T) {
	if err := validatePhase8(phase8Fixture("slow", "normal"), "slow", "normal", "test", 4); err != nil {
		t.Fatal(err)
	}
	r := phase8Fixture("slow", "blocking")
	r["dataset"].(map[string]any)["fadvise_dontneed"] = map[string]any{"requested": true, "error": "EPERM"}
	if err := validatePhase8(r, "slow", "blocking", "test", 4); err == nil {
		t.Fatal("accepted failed DONTNEED")
	}
}
