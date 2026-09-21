package main

import "fmt"

// Strict configuration validation is independent of performance values.
func validatePhase7(r map[string]any, mode, revision string) error {
	obj := func(m map[string]any, key string) map[string]any { v, _ := m[key].(map[string]any); return v }
	c := obj(r, "config")
	env := obj(r, "environment")
	if revision == "" || env["harness_revision"] != revision || env["go_version"] != "go1.27.1" {
		return fmt.Errorf("Phase 7 revision/toolchain mismatch")
	}
	want := map[string]any{"operation": "readat_random", "file_access": "shared_fd", "buffer_bytes": float64(1048576), "concurrency": float64(1000), "max_outstanding": float64(1000), "gomaxprocs": float64(4), "duration_target_ns": float64(5e9), "warmup_ns": float64(0), "fixed_operations": float64(0), "offset_seed": float64(31908), "latency_mode": false, "uring_record_issuer_tids": false, "uring_bounded_workers_set": false, "runtime_sample_interval_ns": float64(1e7)}
	w := map[string]float64{"U1": 1, "U2": 2, "U4": 4, "U8": 8, "U16": 16, "U32": 32, "U64": 64}
	u, explicit := w[mode]
	if !explicit && mode != "blocking" && mode != "normal" && mode != "default" {
		return fmt.Errorf("invalid Phase 7 mode %q", mode)
	}
	force := explicit || mode == "default"
	want["backend"] = "uring"
	if mode == "blocking" {
		want["backend"] = "blocking"
	}
	want["uring_force_async"], want["uring_lock_submitter_thread"] = force, force
	want["uring_unbounded_workers_set"] = explicit
	if explicit {
		want["uring_unbounded_workers"] = u
	}
	for k, v := range want {
		if c[k] != v {
			return fmt.Errorf("Phase 7 config %s=%v, want %v", k, c[k], v)
		}
	}
	d := obj(r, "dataset")
	if d["size_bytes"] != float64(512<<20) || d["cache_precondition"] != "prewarm" || d["cache_state_declared"] != "residency_verified_mostly_resident" {
		return fmt.Errorf("Phase 7 dataset mismatch")
	}
	if mode == "blocking" {
		if _, exists := r["io_uring"]; exists {
			return fmt.Errorf("blocking has io_uring fields")
		}
		return nil
	}
	i := obj(r, "io_uring")
	for k, v := range map[string]any{"force_async": force, "lock_submitter_thread": force, "record_issuer_tids": false, "submit_enter_tid_count": float64(0), "wait_enter_tid_count": float64(0)} {
		if i[k] != v {
			return fmt.Errorf("Phase 7 io_uring %s=%v, want %v", k, i[k], v)
		}
	}
	for _, k := range []string{"submit_enter_distinct_tids", "wait_enter_distinct_tids"} {
		a, ok := i[k].([]any)
		if !ok || len(a) != 0 {
			return fmt.Errorf("Phase 7 nonempty/missing %s", k)
		}
	}
	m := obj(i, "iowq_worker_config")
	for _, k := range []string{"query_error", "registration_error", "post_set_query_error"} {
		if m[k] != "" {
			return fmt.Errorf("Phase 7 worker %s=%v", k, m[k])
		}
	}
	if m["iowq_worker_limit_registration_supported"] != true || m["bounded_worker_limit_requested"] != false || m["unbounded_worker_limit_requested"] != explicit || m["iowq_worker_limit_registration_attempted"] != explicit {
		return fmt.Errorf("Phase 7 worker configuration flags mismatch")
	}
	for _, k := range []string{"previous_bounded_worker_max", "previous_unbounded_worker_max"} {
		n, ok := m[k].(float64)
		if !ok || n <= 0 {
			return fmt.Errorf("missing worker default %s", k)
		}
	}
	if explicit {
		if m["requested_unbounded_worker_max"] != u || m["post_set_unbounded_worker_max"] != u || m["requested_bounded_worker_max"] != m["previous_bounded_worker_max"] || m["post_set_bounded_worker_max"] != m["previous_bounded_worker_max"] {
			return fmt.Errorf("Phase 7 post-set pair mismatch")
		}
	}
	return nil
}
