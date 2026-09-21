package main

import "fmt"

// validatePhase8 checks only the declared portable reproduction configuration.
// It deliberately does not impose a Go version or a storage-specific residency
// reduction threshold on a second host.
func validatePhase8(r map[string]any, part, mode, revision string, gomaxprocs int) error {
	if gomaxprocs < 1 {
		return fmt.Errorf("Phase 8 requires a positive GOMAXPROCS")
	}
	obj := func(m map[string]any, key string) map[string]any { v, _ := m[key].(map[string]any); return v }
	c := obj(r, "config")
	env := obj(r, "environment")
	if revision == "" || env["harness_revision"] != revision {
		return fmt.Errorf("Phase 8 harness revision mismatch")
	}
	if part != "slow" && part != "resident" {
		return fmt.Errorf("invalid Phase 8 part %q", part)
	}
	if mode == "" {
		return fmt.Errorf("Phase 8 mode is required")
	}
	want := map[string]any{
		"operation": "readat_random", "file_access": "shared_fd",
		"buffer_bytes": float64(1048576), "concurrency": float64(1000),
		"max_outstanding": float64(1000), "gomaxprocs": float64(gomaxprocs),
		"duration_target_ns": float64(5e9), "warmup_ns": float64(0),
		"fixed_operations": float64(0), "offset_seed": float64(31908),
		"latency_mode": false, "uring_record_issuer_tids": false,
		"uring_bounded_workers_set": false, "runtime_sample_interval_ns": float64(1e7),
	}

	force, locked, explicit := false, false, false
	workerMax := float64(0)
	switch mode {
	case "blocking":
		want["backend"] = "blocking"
	case "normal":
		want["backend"] = "uring"
	case "default":
		if part != "resident" {
			return fmt.Errorf("Phase 8 default mode is resident-only")
		}
		want["backend"], force, locked = "uring", true, true
	case "U1", "U2", "U4", "U8", "U16":
		if part != "resident" {
			return fmt.Errorf("Phase 8 worker modes are resident-only")
		}
		want["backend"], force, locked, explicit = "uring", true, true, true
		switch mode {
		case "U1":
			workerMax = 1
		case "U2":
			workerMax = 2
		case "U4":
			workerMax = 4
		case "U8":
			workerMax = 8
		case "U16":
			workerMax = 16
		}
	default:
		return fmt.Errorf("invalid Phase 8 %s mode %q", part, mode)
	}
	want["uring_force_async"] = force
	want["uring_lock_submitter_thread"] = locked
	want["uring_unbounded_workers_set"] = explicit
	if explicit {
		want["uring_unbounded_workers"] = workerMax
	}
	for k, v := range want {
		if c[k] != v {
			return fmt.Errorf("Phase 8 config %s=%v, want %v", k, c[k], v)
		}
	}
	d := obj(r, "dataset")
	if d["size_bytes"] == nil {
		return fmt.Errorf("Phase 8 missing dataset size")
	}
	if part == "slow" {
		if d["cache_precondition"] != "dontneed" || d["cache_state_declared"] != "residency_after_dontneed_observed" {
			return fmt.Errorf("Phase 8 slow dataset precondition mismatch")
		}
		if _, ok := d["residency_before_measurement"].(map[string]any); !ok {
			return fmt.Errorf("Phase 8 slow run lacks residency observation")
		}
		if advice := obj(d, "fadvise_dontneed"); advice["requested"] != true || advice["error"] != "" {
			return fmt.Errorf("Phase 8 DONTNEED was not applied: %v", advice)
		}
	} else {
		if d["cache_precondition"] != "prewarm" || d["cache_state_declared"] != "residency_verified_mostly_resident" {
			return fmt.Errorf("Phase 8 resident dataset precondition mismatch")
		}
		if err := residentExactly(d, "residency_after_prewarm"); err != nil {
			return err
		}
		if err := residentExactly(d, "residency_before_measurement"); err != nil {
			return err
		}
	}
	if mode == "blocking" {
		if _, exists := r["io_uring"]; exists {
			return fmt.Errorf("Phase 8 blocking has io_uring fields")
		}
		return nil
	}
	i := obj(r, "io_uring")
	for k, v := range map[string]any{
		"force_async": force, "lock_submitter_thread": locked,
		"record_issuer_tids": false, "submit_enter_tid_count": float64(0),
		"wait_enter_tid_count": float64(0),
	} {
		if i[k] != v {
			return fmt.Errorf("Phase 8 io_uring %s=%v, want %v", k, i[k], v)
		}
	}
	for _, k := range []string{"submit_enter_distinct_tids", "wait_enter_distinct_tids"} {
		a, ok := i[k].([]any)
		if !ok || len(a) != 0 {
			return fmt.Errorf("Phase 8 nonempty/missing %s", k)
		}
	}
	m := obj(i, "iowq_worker_config")
	for _, k := range []string{"query_error", "registration_error", "post_set_query_error"} {
		if m[k] != "" {
			return fmt.Errorf("Phase 8 worker %s=%v", k, m[k])
		}
	}
	if m["iowq_worker_limit_registration_supported"] != true ||
		m["bounded_worker_limit_requested"] != false ||
		m["unbounded_worker_limit_requested"] != explicit ||
		m["iowq_worker_limit_registration_attempted"] != explicit {
		return fmt.Errorf("Phase 8 worker configuration flags mismatch")
	}
	if explicit {
		if m["requested_unbounded_worker_max"] != workerMax || m["post_set_unbounded_worker_max"] != workerMax ||
			m["requested_bounded_worker_max"] != m["previous_bounded_worker_max"] ||
			m["post_set_bounded_worker_max"] != m["previous_bounded_worker_max"] {
			return fmt.Errorf("Phase 8 post-set pair mismatch")
		}
	}
	return nil
}
