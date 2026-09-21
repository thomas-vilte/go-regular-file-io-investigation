package main

import "testing"

func phase7Fixture() map[string]any {
	return map[string]any{
		"environment": map[string]any{"harness_revision": "test", "go_version": "go1.27.1"},
		"config":      map[string]any{"operation": "readat_random", "file_access": "shared_fd", "buffer_bytes": float64(1048576), "concurrency": float64(1000), "max_outstanding": float64(1000), "gomaxprocs": float64(4), "duration_target_ns": float64(5e9), "warmup_ns": float64(0), "fixed_operations": float64(0), "offset_seed": float64(31908), "latency_mode": false, "uring_record_issuer_tids": false, "uring_bounded_workers_set": false, "runtime_sample_interval_ns": float64(1e7), "backend": "uring", "uring_force_async": true, "uring_lock_submitter_thread": true, "uring_unbounded_workers_set": true, "uring_unbounded_workers": float64(4)},
		"dataset":     map[string]any{"size_bytes": float64(512 << 20), "cache_precondition": "prewarm", "cache_state_declared": "residency_verified_mostly_resident"},
		"io_uring":    map[string]any{"force_async": true, "lock_submitter_thread": true, "record_issuer_tids": false, "submit_enter_tid_count": float64(0), "wait_enter_tid_count": float64(0), "submit_enter_distinct_tids": []any{}, "wait_enter_distinct_tids": []any{}, "iowq_worker_config": map[string]any{"query_error": "", "registration_error": "", "post_set_query_error": "", "iowq_worker_limit_registration_supported": true, "bounded_worker_limit_requested": false, "unbounded_worker_limit_requested": true, "iowq_worker_limit_registration_attempted": true, "previous_bounded_worker_max": float64(16), "previous_unbounded_worker_max": float64(63206), "requested_bounded_worker_max": float64(16), "requested_unbounded_worker_max": float64(4), "post_set_bounded_worker_max": float64(16), "post_set_unbounded_worker_max": float64(4)}},
	}
}

func TestPhase7ControlValidation(t *testing.T) {
	if err := validatePhase7(phase7Fixture(), "U4", "test"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		object, key string
		value       any
	}{
		{"config", "uring_record_issuer_tids", true},
		{"io_uring", "record_issuer_tids", true},
		{"config", "uring_lock_submitter_thread", false},
		{"iowq_worker_config", "post_set_bounded_worker_max", float64(1)},
		{"iowq_worker_config", "post_set_unbounded_worker_max", float64(8)},
		{"iowq_worker_config", "post_set_query_error", "EPERM"},
	} {
		r := phase7Fixture()
		var m map[string]any
		if change.object == "iowq_worker_config" {
			m = r["io_uring"].(map[string]any)[change.object].(map[string]any)
		} else {
			m = r[change.object].(map[string]any)
		}
		m[change.key] = change.value
		if err := validatePhase7(r, "U4", "test"); err == nil {
			t.Errorf("accepted mutation %s", change.key)
		}
	}
}

func TestPhase7Controls(t *testing.T) {
	for _, mode := range []string{"blocking", "normal", "default"} {
		r := phase7Fixture()
		c := r["config"].(map[string]any)
		c["uring_unbounded_workers_set"] = false
		force := mode == "default"
		c["uring_force_async"], c["uring_lock_submitter_thread"] = force, force
		if mode == "blocking" {
			c["backend"] = "blocking"
			delete(r, "io_uring")
		} else {
			i := r["io_uring"].(map[string]any)
			i["force_async"], i["lock_submitter_thread"] = force, force
			m := i["iowq_worker_config"].(map[string]any)
			m["unbounded_worker_limit_requested"], m["iowq_worker_limit_registration_attempted"] = false, false
		}
		if err := validatePhase7(r, mode, "test"); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}
