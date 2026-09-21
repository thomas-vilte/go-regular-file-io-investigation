package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	"example.com/go-iouring-investigation/bench/internal/uring"
)

func TestRecordIssuerTIDsConfiguration(t *testing.T) {
	c := config{File: "input", Operation: "readat", FileAccess: "shared_fd", Backend: "uring", BufferBytes: 4096, Concurrency: 1, GOMAXPROCS: 1, Duration: 1, CachePrecondition: "none"}
	if ringOptions(c).RecordIssuerTIDs {
		t.Fatal("default must be disabled")
	}
	for _, enabled := range []bool{false, true} {
		c.URingRecordIssuerTIDs = enabled
		if err := validate(c); err != nil {
			t.Fatal(err)
		}
		if ringOptions(c).RecordIssuerTIDs != enabled {
			t.Fatal("option lost")
		}
		r := baseResult(c, 4096, 1)
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var decoded result
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Config["uring_record_issuer_tids"] != enabled {
			t.Fatal("JSON option lost")
		}
	}
	c.Backend = "blocking"
	if err := validate(c); err == nil {
		t.Fatal("blocking accepted recording")
	}
	r := result{}
	addURingResult(&r, &uring.Ring{}, 0)
	if r.URing["record_issuer_tids"] != false || r.URing["submit_enter_tid_count"] != 0 || r.URing["wait_enter_tid_count"] != 0 {
		t.Fatalf("disabled ring JSON: %+v", r.URing)
	}
}

func TestValidateBackends(t *testing.T) {
	base := config{File: "input", Operation: "readat", FileAccess: "shared_fd", Backend: "blocking", BufferBytes: 4096, Concurrency: 1, GOMAXPROCS: 1, Duration: 1, CachePrecondition: "none"}
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	base.Backend = "uring"
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	base.URingForceAsync = true
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	base.Backend = "blocking"
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "uring-force-async") {
		t.Fatalf("force-async/blocking validation error=%v", err)
	}
	base.Backend = "uring"
	base.URingBoundedWorkers = 4
	base.URingBoundedWorkersSet = true
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	base.URingForceAsync = false
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "uring-force-async") {
		t.Fatalf("bounded workers without force-async validation error=%v", err)
	}
	base.Backend = "blocking"
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "backend=uring") {
		t.Fatalf("bounded workers/blocking validation error=%v", err)
	}
	base.Backend = "uring"
	base.URingForceAsync = true
	base.URingBoundedWorkers = 0
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("zero bounded workers validation error=%v", err)
	}
	base.URingBoundedWorkers = -1
	base.URingBoundedWorkersSet = false
	base.URingUnboundedWorkers = 4
	base.URingUnboundedWorkersSet = true
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	base.URingForceAsync = false
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "uring-force-async") {
		t.Fatalf("unbounded workers without force-async validation error=%v", err)
	}
	base.Backend = "blocking"
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "backend=uring") {
		t.Fatalf("unbounded workers/blocking validation error=%v", err)
	}
	base.Backend = "uring"
	base.URingForceAsync = true
	base.URingUnboundedWorkers = 0
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("zero unbounded workers validation error=%v", err)
	}
	base.URingUnboundedWorkers = -1
	base.URingUnboundedWorkersSet = false
	base.URingLockSubmitterThread = true
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
	base.URingForceAsync = false
	base.Backend = "blocking"
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "lock-submitter-thread") {
		t.Fatalf("locked submitter/blocking validation error=%v", err)
	}
	base.Backend = "uring"
	base.Operation = "read"
	if err := validate(base); err == nil || !strings.Contains(err.Error(), "backend=uring") {
		t.Fatalf("uring/read validation error=%v", err)
	}
}

func TestSeedOffsetsDeterministic(t *testing.T) {
	a, b := uint64(31908), uint64(31908)
	for i := 0; i < 32; i++ {
		if x, y := nextOffset(&a, 128, 4096), nextOffset(&b, 128, 4096); x != y {
			t.Fatalf("offset %d differs: %d != %d", i, x, y)
		}
	}
}

func TestBoundedWorkerOptionPropagation(t *testing.T) {
	if got := boundedWorkerOption(config{URingBoundedWorkers: 4}); got != nil {
		t.Fatalf("unset option propagated as %d", *got)
	}
	if got := boundedWorkerOption(config{URingBoundedWorkersSet: true, URingBoundedWorkers: -1}); got != nil {
		t.Fatalf("negative option propagated as %d", *got)
	}
	got := boundedWorkerOption(config{URingBoundedWorkersSet: true, URingBoundedWorkers: 4})
	if got == nil || *got != 4 {
		t.Fatalf("bounded option=%v, want 4", got)
	}
	if got := unboundedWorkerOption(config{URingUnboundedWorkers: 4}); got != nil {
		t.Fatalf("unset unbounded option propagated as %d", *got)
	}
	if got := unboundedWorkerOption(config{URingUnboundedWorkersSet: true, URingUnboundedWorkers: -1}); got != nil {
		t.Fatalf("negative unbounded option propagated as %d", *got)
	}
	got = unboundedWorkerOption(config{URingUnboundedWorkersSet: true, URingUnboundedWorkers: 4})
	if got == nil || *got != 4 {
		t.Fatalf("unbounded option=%v, want 4", got)
	}
	opts := ringOptions(config{URingForceAsync: true, URingLockSubmitterThread: true})
	if !opts.ForceAsync || !opts.LockSubmitterThread {
		t.Fatalf("ring options did not propagate diagnostics: %+v", opts)
	}
}

func TestBlockingSmallWorkload(t *testing.T) {
	f := benchmarkTestFile(t, 64<<10)
	defer f.Close()
	r := runChild(config{File: f.Name(), Operation: "readat", FileAccess: "shared_fd", Backend: "blocking", BufferBytes: 4096, Concurrency: 4, MaxOutstanding: 2, GOMAXPROCS: 1, Operations: 16, Seed: 31908, CachePrecondition: "none"})
	if r.Status != "ok" || r.Work["operations"] != uint64(16) {
		t.Fatalf("result status=%s error=%s work=%+v", r.Status, r.Error, r.Work)
	}
}

func TestURingSmallWorkloadMatchesBlockingBytes(t *testing.T) {
	f := benchmarkTestFile(t, 64<<10)
	defer f.Close()
	for _, forceAsync := range []bool{false, true} {
		name := "normal"
		if forceAsync {
			name = "force_async"
		}
		t.Run(name, func(t *testing.T) {
			ring, err := uring.NewWithOptions(4, uring.Options{ForceAsync: forceAsync, RecordIssuerTIDs: forceAsync})
			if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOSYS) {
				t.Skipf("io_uring unavailable: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := ring.Close(); err != nil {
					t.Fatal(err)
				}
			}()

			blocking := worker{file: f, buf: make([]byte, 4096), seed: 31908, slots: 16, operation: "readat", backend: "blocking", limiter: newOutstandingLimiter(1)}
			async := worker{file: f, buf: make([]byte, 4096), seed: 31908, slots: 16, operation: "readat", backend: "uring", ring: ring, limiter: newOutstandingLimiter(1)}
			blocking.execute(phaseCommand{operations: 1})
			async.execute(phaseCommand{operations: 1})
			if blocking.err != nil || async.err != nil {
				t.Fatalf("blocking=%v uring=%v", blocking.err, async.err)
			}
			if !bytes.Equal(blocking.buf, async.buf) {
				t.Fatal("same seed/offset returned different bytes")
			}
			stats := ring.Stats()
			if stats.OperationsCreated != stats.OperationsAdmitted+stats.OperationsCleanedBeforePublication ||
				stats.OperationsAdmitted != stats.OperationsQueued ||
				stats.OperationsPublished != stats.Submissions ||
				stats.Submissions != stats.Completions ||
				stats.PinsCreated != stats.PinsReleased ||
				stats.OperationTable != 0 || stats.PinnedOperations != 0 || stats.UnknownIDs != 0 || stats.DuplicateCQEs != 0 || ring.Outstanding() != 0 {
				t.Fatalf("unreconciled: %+v outstanding=%d", stats, ring.Outstanding())
			}
			result := result{}
			addURingResult(&result, ring, 1)
			if result.URing["record_issuer_tids"] != forceAsync {
				t.Fatal("JSON recording flag does not match selected mode")
			}
			if got, ok := result.URing["force_async"].(bool); !ok || got != forceAsync {
				t.Fatalf("force_async metric=%v, want %t", result.URing["force_async"], forceAsync)
			}
		})
	}
}

func TestURingReconciliationRequiresBalancedPins(t *testing.T) {
	values := map[string]any{
		"operations_created":             uint64(1),
		"operations_admitted":            uint64(1),
		"operations_queued":              uint64(1),
		"operations_published":           uint64(1),
		"operations_cleaned_pre_publish": uint64(0),
		"pins_created":                   uint64(1),
		"pins_released":                  uint64(0),
		"submissions":                    uint64(1),
		"terminal_completions":           uint64(1),
		"submit_entries_consumed":        uint64(1),
		"submit_entries_requested":       uint64(1),
		"completion_cqes_drained_total":  uint64(1),
		"ring_outstanding_at_end":        uint64(0),
		"operation_table_entries_at_end": uint64(0),
		"pins_remaining_at_end":          uint64(0),
		"unknown_completion_ids":         uint64(0),
		"duplicate_cqes":                 uint64(0),
	}
	if err := checkURingReconciliation(values); err == nil {
		t.Fatal("reconciliation accepted an unreleased pin")
	}
	values["pins_released"] = uint64(1)
	if err := checkURingReconciliation(values); err != nil {
		t.Fatalf("reconciliation rejected balanced counters: %v", err)
	}
}

func TestURingReconciliationRequiresDrainedCQECounter(t *testing.T) {
	values := map[string]any{
		"operations_created":             uint64(1),
		"operations_admitted":            uint64(1),
		"operations_queued":              uint64(1),
		"operations_published":           uint64(1),
		"operations_cleaned_pre_publish": uint64(0),
		"pins_created":                   uint64(1),
		"pins_released":                  uint64(1),
		"submissions":                    uint64(1),
		"terminal_completions":           uint64(1),
		"submit_entries_consumed":        uint64(1),
		"submit_entries_requested":       uint64(1),
		"completion_cqes_drained_total":  uint64(0),
		"ring_outstanding_at_end":        uint64(0),
		"operation_table_entries_at_end": uint64(0),
		"pins_remaining_at_end":          uint64(0),
		"unknown_completion_ids":         uint64(0),
		"duplicate_cqes":                 uint64(0),
	}
	if err := checkURingReconciliation(values); err == nil {
		t.Fatal("reconciliation accepted an incomplete CQE drain counter")
	}
	values["completion_cqes_drained_total"] = uint64(1)
	if err := checkURingReconciliation(values); err != nil {
		t.Fatalf("reconciliation rejected complete CQE drain accounting: %v", err)
	}
}

func benchmarkTestFile(t *testing.T, size int) *os.File {
	t.Helper()
	b := make([]byte, size)
	for i := range b {
		b[i] = byte((i*17 + 3) % 251)
	}
	f, err := os.CreateTemp(t.TempDir(), "iobaseline")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return f
}
