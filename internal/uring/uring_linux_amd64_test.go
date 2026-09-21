package uring

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"
)

func testFile(t *testing.T) (*os.File, []byte) {
	t.Helper()
	data := make([]byte, 4<<20)
	for i := range data {
		data[i] = byte((i*31 + 17) % 251)
	}
	f, err := os.CreateTemp(t.TempDir(), "uring-data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return f, data
}

func testRing(t *testing.T, entries uint32, forceAsync bool) *Ring {
	t.Helper()
	r, err := NewWithOptions(entries, Options{ForceAsync: forceAsync})
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOSYS) {
		t.Skipf("io_uring unavailable in this environment: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return r
}

func forEachMode(t *testing.T, run func(t *testing.T, forceAsync bool)) {
	t.Helper()
	for _, forceAsync := range []bool{false, true} {
		name := "normal"
		if forceAsync {
			name = "force_async"
		}
		t.Run(name, func(t *testing.T) { run(t, forceAsync) })
	}
}

func requireReconciled(t *testing.T, r *Ring) {
	t.Helper()
	stats := r.Stats()
	if stats.OperationsCreated != stats.OperationsAdmitted+stats.OperationsCleanedBeforePublication ||
		stats.OperationsAdmitted != stats.OperationsQueued ||
		stats.OperationsPublished != stats.Submissions ||
		stats.Submissions != stats.Completions ||
		stats.SubmitEntriesConsumed != stats.OperationsPublished ||
		stats.SubmitEntriesRequested < stats.SubmitEntriesConsumed ||
		stats.EnterCalls != stats.SubmitEnterCalls+stats.WaitEnterCalls ||
		stats.PinsCreated != stats.PinsReleased ||
		stats.OperationTable != 0 ||
		stats.PinnedOperations != 0 ||
		stats.UnknownIDs != 0 ||
		stats.DuplicateCQEs != 0 ||
		r.Outstanding() != 0 {
		t.Fatalf("unreconciled stats: %+v outstanding=%d", stats, r.Outstanding())
	}
}

func TestBatchBucket(t *testing.T) {
	cases := []struct{ n, want int }{
		{1, 0}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {8, 3}, {9, 4},
		{16, 4}, {17, 5}, {32, 5}, {33, 6}, {64, 6}, {65, 7},
		{128, 7}, {129, 8}, {256, 8}, {257, 9}, {512, 9}, {513, 10}, {1024, 10},
	}
	for _, tc := range cases {
		if got := batchBucket(tc.n); got != tc.want {
			t.Errorf("batchBucket(%d)=%d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestPartialSubmitAccounting(t *testing.T) {
	r := &Ring{}
	remaining, err := r.accountSubmitResult(4, 2)
	if err != nil || remaining != 2 {
		t.Fatalf("partial result remaining=%d err=%v", remaining, err)
	}
	remaining, err = r.accountSubmitResult(remaining, 2)
	if err != nil || remaining != 0 {
		t.Fatalf("final result remaining=%d err=%v", remaining, err)
	}
	stats := r.Stats()
	if stats.SubmitEntriesConsumed != 4 || stats.EnterSubmitted != 4 || stats.PartialSubmitReturns != 1 || stats.ZeroSubmitReturns != 0 {
		t.Fatalf("partial accounting: %+v", stats)
	}
	if _, err := r.accountSubmitResult(2, 0); err == nil {
		t.Fatal("zero submit return was accepted")
	}
	if _, err := r.accountSubmitResult(2, 3); err == nil {
		t.Fatal("oversized submit return was accepted")
	}
}

// This ring is entirely test-owned memory, never mmaped or registered with the
// kernel. fd=-1 makes enter fail after the actual production publication path.
// Thus tests can inspect published ownership and simulate consumption/CQEs
// deterministically, without a production hook or a kernel timing assumption.
func publicationTestRing(t *testing.T, entries uint32) *Ring {
	t.Helper()
	r := &Ring{fd: -1, ops: make(map[uint64]*operation), completed: make(map[uint64]struct{})}
	r.params = params{SqEntries: entries, CqEntries: entries,
		SqOff: sqOffsets{Head: 0, Tail: 4, RingMask: 8, Array: 16},
		CqOff: cqOffsets{Head: 0, Tail: 4, RingMask: 8, Cqes: 16}}
	r.sqRing = make([]byte, 16+entries*4)
	r.cqRing = make([]byte, 16+entries*uint32(unsafe.Sizeof(cqe{})))
	r.sqes = make([]byte, entries*uint32(unsafe.Sizeof(sqe{})))
	r.space = sync.NewCond(&sync.Mutex{})
	*(*uint32)(unsafe.Pointer(&r.sqRing[8])) = entries - 1
	*(*uint32)(unsafe.Pointer(&r.cqRing[8])) = entries - 1
	t.Cleanup(func() {
		// Even a failed assertion must not trigger a Pinner finalizer. Safe only
		// for this fixture: no kernel ever had access to its SQ or buffers.
		for _, op := range r.ops {
			op.pinner.Unpin()
		}
	})
	return r
}

func queueTestOperations(r *Ring, n int) []*operation {
	r.mu.Lock()
	defer r.mu.Unlock()
	ops := make([]*operation, n)
	for i := range ops {
		r.nextID++
		op := &operation{id: r.nextID, fd: -1, buf: make([]byte, 4096), offset: int64(r.nextID * 4096), done: make(chan completion, 1), state: opQueued}
		op.pinner.Pin(&op.buf[0])
		r.ops[op.id] = op
		ops[i] = op
	}
	atomic.AddUint64(&r.stats.OperationsCreated, uint64(n))
	atomic.AddUint64(&r.stats.OperationsAdmitted, uint64(n))
	atomic.AddUint64(&r.stats.OperationsQueued, uint64(n))
	atomic.AddUint64(&r.stats.PinsCreated, uint64(n))
	atomic.AddUint64(&r.stats.PinnedOperations, uint64(n))
	return ops
}

func requirePublishedOwnership(t *testing.T, r *Ring, ops []*operation) {
	t.Helper()
	for _, op := range ops {
		if r.ops[op.id] != op || op.state != opPublished {
			t.Fatalf("operation %d lost published ownership: state=%d", op.id, op.state)
		}
	}
	if r.Outstanding() != uint64(len(ops)) || r.Stats().PinsReleased != 0 {
		t.Fatalf("ownership accounting: %+v outstanding=%d", r.Stats(), r.Outstanding())
	}
}

func completeTestOperations(t *testing.T, r *Ring, ops []*operation) {
	t.Helper()
	tail := r.loadCq(r.params.CqOff.Tail)
	// Reverse order ensures completions resolve by ID, not submission order.
	for i := range ops {
		op := ops[len(ops)-1-i]
		idx := (tail + uint32(i)) & (r.params.CqEntries - 1)
		q := (*cqe)(unsafe.Pointer(&r.cqRing[16+int(idx)*int(unsafe.Sizeof(cqe{}))]))
		*q = cqe{UserData: op.id, Res: int32(len(op.buf))}
	}
	*(*uint32)(unsafe.Pointer(&r.cqRing[4])) = tail + uint32(len(ops))
	if got := r.reap(); got != len(ops) {
		t.Fatalf("reaped %d, want %d", got, len(ops))
	}
	for _, op := range ops {
		select {
		case c := <-op.done:
			if c.err != nil || c.n != len(op.buf) || op.state != opReleased {
				t.Fatalf("terminal operation %d: %+v state=%d", op.id, c, op.state)
			}
		default:
			t.Fatalf("operation %d not notified", op.id)
		}
	}
}

func requirePublicationAccounting(t *testing.T, r *Ring, count uint64) {
	t.Helper()
	s := r.Stats()
	if s.OperationsPublished != count || s.SubmitBatchEntriesTotal != count ||
		s.CompletionCQEsDrainedTotal != count || s.SubmitBatches == 0 || s.SubmitBatches > count ||
		s.SubmitBatchSizeMax == 0 || s.SubmitBatchSizeMax > count {
		t.Fatalf("publication/drain accounting: %+v", s)
	}
}

func TestPublicationBatchOwnershipAndWraparound(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		r := publicationTestRing(t, 2)
		r.forceAsync = forceAsync
		// Start at the last SQ slot: the first two-entry batch wraps to slot 0.
		*(*uint32)(unsafe.Pointer(&r.sqRing[0])) = 1
		*(*uint32)(unsafe.Pointer(&r.sqRing[4])) = 1
		ops := queueTestOperations(r, 3)
		n, err := r.submitBatch(ops)
		if n != 2 || err == nil {
			t.Fatalf("published=%d err=%v; want prefix 2 and invalid-fd enter error", n, err)
		}
		if s := r.Stats(); r.loadSq(r.params.SqOff.Tail) != 3 || s.SubmitBatches != 1 || s.SubmitBatchSizeMax != 2 || s.SubmitBatchSizeBuckets[1] != 1 {
			t.Fatalf("two-entry publication: %+v", r.Stats())
		}
		requirePublishedOwnership(t, r, ops[:2])
		if err := r.Close(); err == nil {
			t.Fatal("Close accepted published operations")
		}
		if ops[2].state != opQueued || r.ops[ops[2].id] != ops[2] {
			t.Fatal("non-fitting operation did not retain queued ownership")
		}
		for i, op := range ops[:2] {
			idx := (1 + i) & 1
			s := *(*sqe)(unsafe.Pointer(&r.sqes[idx*int(unsafe.Sizeof(sqe{}))]))
			array := *(*uint32)(unsafe.Pointer(&r.sqRing[16+idx*4]))
			if array != uint32(idx) || s.UserData != op.id || s.FD != int32(op.fd) || s.Opcode != ioUringOpRead || s.Flags != readSQEFlags(forceAsync) || s.Off != uint64(op.offset) || s.Len != uint32(len(op.buf)) || s.Addr != uint64(uintptr(unsafe.Pointer(&op.buf[0]))) {
				t.Fatalf("incorrect SQ slot %d: %+v array=%d", idx, s, array)
			}
		}
		if n, err := r.submitBatch(ops[2:]); n != 0 || err != nil || r.loadSq(r.params.SqOff.Tail) != 3 {
			t.Fatalf("full SQ changed publication: n=%d err=%v", n, err)
		}
		// Model partial and zero results without allowing either to release any
		// of the already published/pinned operations.
		if left, err := r.accountSubmitResult(2, 1); err != nil || left != 1 {
			t.Fatalf("partial left=%d err=%v", left, err)
		}
		requirePublishedOwnership(t, r, ops[:2])
		if left, err := r.accountSubmitResult(1, 0); err == nil || left != 1 {
			t.Fatalf("zero left=%d err=%v", left, err)
		}
		requirePublishedOwnership(t, r, ops[:2])
		if _, err := r.accountSubmitResult(1, 1); err != nil {
			t.Fatal(err)
		}
		*(*uint32)(unsafe.Pointer(&r.sqRing[0])) = 3
		completeTestOperations(t, r, ops[:2])
		if n, err := r.submitBatch(ops[2:]); n != 1 || err == nil {
			t.Fatalf("remaining prefix n=%d err=%v", n, err)
		}
		if _, err := r.accountSubmitResult(1, 1); err != nil {
			t.Fatal(err)
		}
		completeTestOperations(t, r, ops[2:])
		requirePublicationAccounting(t, r, 3)
		requireReconciled(t, r)
	})
}

func TestSingletonPublicationReconciles(t *testing.T) {
	r := publicationTestRing(t, 2)
	for i := 0; i < 32; i++ {
		ops := queueTestOperations(r, 1)
		if n, err := r.submitBatch(ops); n != 1 || err == nil {
			t.Fatalf("singleton %d n=%d err=%v", i, n, err)
		}
		if _, err := r.accountSubmitResult(1, 1); err != nil {
			t.Fatal(err)
		}
		*(*uint32)(unsafe.Pointer(&r.sqRing[0])) = r.loadSq(r.params.SqOff.Tail)
		completeTestOperations(t, r, ops)
	}
	if s := r.Stats(); s.SubmitBatches != 32 || s.SubmitBatchSizeMax != 1 {
		t.Fatalf("singleton regression shape: %+v", s)
	}
	requirePublicationAccounting(t, r, 32)
	requireReconciled(t, r)
}

func TestPublicationValidatesEntireBatchBeforeTail(t *testing.T) {
	r := publicationTestRing(t, 2)
	ops := queueTestOperations(r, 2)
	ops[1].state = opAdmitted // Invalid second member must reject the whole batch.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("invalid second operation did not panic")
			}
		}()
		_, _ = r.submitBatch(ops)
	}()
	if r.loadSq(r.params.SqOff.Tail) != 0 || r.Outstanding() != 0 ||
		ops[0].state != opQueued || ops[1].state != opAdmitted ||
		r.Stats().OperationsPublished != 0 || r.Stats().EnterCalls != 0 || r.Stats().PinsReleased != 0 {
		t.Fatalf("invalid batch became visible or lost ownership: %+v", r.Stats())
	}
	for _, op := range ops {
		if r.ops[op.id] != op {
			t.Fatalf("invalid batch lost operation %d", op.id)
		}
	}
}

func TestReadSQEFlags(t *testing.T) {
	if got := readSQEFlags(false); got != 0 {
		t.Fatalf("normal SQE flags=%#x, want 0", got)
	}
	if got := readSQEFlags(true); got != ioUringSQEAsync {
		t.Fatalf("force-async SQE flags=%#x, want %#x", got, ioUringSQEAsync)
	}
}

func TestTIDSetBookkeeping(t *testing.T) {
	var set tidSet
	set.add(9)
	set.add(3)
	set.add(9)
	values, truncated := set.snapshot()
	if truncated || !slices.Equal(values, []int{3, 9}) {
		t.Fatalf("tid set values=%v truncated=%t", values, truncated)
	}
	for tid := 1; tid <= maxRecordedTIDs+1; tid++ {
		set.add(100 + tid)
	}
	values, truncated = set.snapshot()
	if !truncated || len(values) != maxRecordedTIDs {
		t.Fatalf("bounded tid set values=%d truncated=%t", len(values), truncated)
	}
}

func TestIssuerTIDRecordingGate(t *testing.T) {
	var set tidSet
	calls := 0
	reader := func() int { calls++; return 123 }
	recordIssuerTID(false, &set, reader)
	if calls != 0 || set.values != nil {
		t.Fatal("disabled recording invoked reader or allocated bookkeeping")
	}
	recordIssuerTID(true, &set, reader)
	recordIssuerTID(true, &set, reader)
	ids, truncated := set.snapshot()
	if calls != 2 || truncated || !slices.Equal(ids, []int{123}) {
		t.Fatalf("enabled: calls=%d ids=%v truncated=%t", calls, ids, truncated)
	}
	r := &Ring{}
	if r.RecordIssuerTIDs() || (Options{}).RecordIssuerTIDs {
		t.Fatal("recording must default to disabled")
	}
}

func TestRingIssuerRecordingModes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			r, err := NewWithOptions(2, Options{RecordIssuerTIDs: enabled, LockSubmitterThread: true})
			if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOSYS) {
				t.Skipf("io_uring unavailable: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			f, data := testFile(t)
			defer f.Close()
			buf := make([]byte, 4096)
			if n, err, _ := r.ReadAt(int(f.Fd()), buf, 0); err != nil || n != len(buf) || !bytes.Equal(buf, data[:len(buf)]) {
				t.Fatalf("read n=%d err=%v", n, err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			_, _, submit, wait, _ := r.IssuerTIDs()
			if r.RecordIssuerTIDs() != enabled {
				t.Fatal("mode lost")
			}
			if enabled && len(submit) != 1 {
				t.Fatalf("locked submit TIDs: %v", submit)
			}
			if !enabled && (len(submit) != 0 || len(wait) != 0) {
				t.Fatalf("disabled recorded TIDs: %v %v", submit, wait)
			}
			requireReconciled(t, r)
		})
	}
}

func TestConfigureIOWQWorkersQuerySetAndVerify(t *testing.T) {
	r := &Ring{fd: 7}
	var calls [][2]uint32
	err := r.configureIOWQWorkers(uint32Pointer(8), nil, func(fd int, values *[2]uint32) error {
		if fd != 7 {
			t.Fatalf("register fd=%d, want 7", fd)
		}
		calls = append(calls, *values)
		switch len(calls) {
		case 1:
			if *values != [2]uint32{} {
				t.Fatalf("query values=%v, want [0 0]", *values)
			}
			*values = [2]uint32{4, 99}
		case 2:
			if *values != [2]uint32{8, 99} {
				t.Fatalf("set values=%v, want [8 99]", *values)
			}
			// The kernel writes back the maxima that applied before this set.
			*values = [2]uint32{4, 99}
		case 3:
			if *values != [2]uint32{} {
				t.Fatalf("post-set query values=%v, want [0 0]", *values)
			}
			*values = [2]uint32{8, 99}
		default:
			t.Fatalf("unexpected register call %d", len(calls))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("register calls=%d, want 3", len(calls))
	}
	c := r.IOWQWorkerConfig()
	if !c.BoundedWorkerLimitRequested || !c.IOWQWorkerLimitQueryAttempted || !c.IOWQWorkerLimitRegistrationAttempted || !c.IOWQWorkerLimitRegistrationSupported {
		t.Fatalf("unexpected config: %+v", c)
	}
	requireWorkerLimit(t, "previous bounded", c.PreviousBoundedWorkerMax, 4)
	requireWorkerLimit(t, "previous unbounded", c.PreviousUnboundedWorkerMax, 99)
	requireWorkerLimit(t, "requested bounded", c.RequestedBoundedWorkerMax, 8)
	requireWorkerLimit(t, "requested unbounded", c.RequestedUnboundedWorkerMax, 99)
	requireWorkerLimit(t, "returned bounded", c.RegistrationReturnedBoundedWorkerMax, 4)
	requireWorkerLimit(t, "returned unbounded", c.RegistrationReturnedUnboundedWorkerMax, 99)
	requireWorkerLimit(t, "post-set bounded", c.PostSetBoundedWorkerMax, 8)
	requireWorkerLimit(t, "post-set unbounded", c.PostSetUnboundedWorkerMax, 99)
}

func TestConfigureIOWQWorkersDefaultQueryDoesNotSet(t *testing.T) {
	r := &Ring{fd: 11}
	calls := 0
	if err := r.configureIOWQWorkers(nil, nil, func(_ int, values *[2]uint32) error {
		calls++
		if *values != [2]uint32{} {
			t.Fatalf("default query values=%v, want [0 0]", *values)
		}
		*values = [2]uint32{3, 55}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("register calls=%d, want 1", calls)
	}
	c := r.IOWQWorkerConfig()
	if c.BoundedWorkerLimitRequested || c.IOWQWorkerLimitRegistrationAttempted || !c.IOWQWorkerLimitRegistrationSupported {
		t.Fatalf("unexpected default config: %+v", c)
	}
	requireWorkerLimit(t, "default bounded", c.PreviousBoundedWorkerMax, 3)
	requireWorkerLimit(t, "default unbounded", c.PreviousUnboundedWorkerMax, 55)
}

func TestConfigureIOWQWorkersSetsUnboundedOnly(t *testing.T) {
	r := &Ring{fd: 11}
	calls := 0
	if err := r.configureIOWQWorkers(nil, uint32Pointer(5), func(_ int, values *[2]uint32) error {
		calls++
		switch calls {
		case 1:
			*values = [2]uint32{3, 55}
		case 2:
			if *values != [2]uint32{3, 5} {
				t.Fatalf("unbounded-only set values=%v, want [3 5]", *values)
			}
			*values = [2]uint32{3, 55}
		case 3:
			*values = [2]uint32{3, 5}
		default:
			t.Fatalf("unexpected register call %d", calls)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c := r.IOWQWorkerConfig()
	if c.BoundedWorkerLimitRequested || !c.UnboundedWorkerLimitRequested {
		t.Fatalf("unexpected requested flags: %+v", c)
	}
	requireWorkerLimit(t, "unbounded-only requested bounded", c.RequestedBoundedWorkerMax, 3)
	requireWorkerLimit(t, "unbounded-only requested unbounded", c.RequestedUnboundedWorkerMax, 5)
	requireWorkerLimit(t, "unbounded-only post-set bounded", c.PostSetBoundedWorkerMax, 3)
	requireWorkerLimit(t, "unbounded-only post-set unbounded", c.PostSetUnboundedWorkerMax, 5)
}

func TestConfigureIOWQWorkersErrors(t *testing.T) {
	r := &Ring{fd: 1}
	if err := r.configureIOWQWorkers(nil, nil, func(_ int, _ *[2]uint32) error { return syscall.EPERM }); err != nil {
		t.Fatalf("default query error=%v, want non-fatal", err)
	}
	if r.IOWQWorkerConfig().IOWQWorkerLimitRegistrationSupported {
		t.Fatal("unsupported query recorded as supported")
	}

	r = &Ring{fd: 1}
	err := r.configureIOWQWorkers(uint32Pointer(2), nil, func(_ int, _ *[2]uint32) error { return syscall.EINVAL })
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("requested query error=%v, want EINVAL", err)
	}
	if r.IOWQWorkerConfig().IOWQWorkerLimitRegistrationAttempted {
		t.Fatal("set marked attempted after failed query")
	}

	r = &Ring{fd: 1}
	calls := 0
	err = r.configureIOWQWorkers(uint32Pointer(2), nil, func(_ int, values *[2]uint32) error {
		calls++
		if calls == 1 {
			*values = [2]uint32{3, 7}
			return nil
		}
		return syscall.EPERM
	})
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("set error=%v, want EPERM", err)
	}
	c := r.IOWQWorkerConfig()
	if !c.IOWQWorkerLimitRegistrationAttempted || c.RegistrationError == "" {
		t.Fatalf("failed set not recorded: %+v", c)
	}
}

func TestConfigureIOWQWorkersPostSetMismatchFails(t *testing.T) {
	r := &Ring{fd: 1}
	calls := 0
	err := r.configureIOWQWorkers(uint32Pointer(2), uint32Pointer(5), func(_ int, values *[2]uint32) error {
		calls++
		switch calls {
		case 1:
			*values = [2]uint32{3, 7}
		case 2:
			if *values != [2]uint32{2, 5} {
				t.Fatalf("set values=%v, want [2 5]", *values)
			}
			*values = [2]uint32{3, 7}
		case 3:
			*values = [2]uint32{2, 7}
		default:
			t.Fatalf("unexpected register call %d", calls)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("post-set mismatch error=%v", err)
	}
	c := r.IOWQWorkerConfig()
	requireWorkerLimit(t, "mismatched post-set bounded", c.PostSetBoundedWorkerMax, 2)
	requireWorkerLimit(t, "mismatched post-set unbounded", c.PostSetUnboundedWorkerMax, 7)
}

func TestUnboundedWorkerOptionsRequireForceAsync(t *testing.T) {
	if _, err := NewWithOptions(1, Options{BoundedWorkers: uint32Pointer(1)}); err == nil {
		t.Fatal("bounded workers without force-async were accepted")
	}
	if _, err := NewWithOptions(1, Options{ForceAsync: true, BoundedWorkers: uint32Pointer(0)}); err == nil {
		t.Fatal("zero bounded workers were accepted")
	}
	if _, err := NewWithOptions(1, Options{UnboundedWorkers: uint32Pointer(1)}); err == nil {
		t.Fatal("unbounded workers without force-async were accepted")
	}
	if _, err := NewWithOptions(1, Options{ForceAsync: true, UnboundedWorkers: uint32Pointer(0)}); err == nil {
		t.Fatal("zero unbounded workers were accepted")
	}
}

func requireWorkerLimit(t *testing.T, name string, got *uint32, want uint32) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s=%v, want %d", name, got, want)
	}
}

func TestReadAtMatchesFile(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		f, data := testFile(t)
		defer f.Close()
		r := testRing(t, 8, forceAsync)
		for _, tc := range []struct{ off, n int }{{0, 4096}, {12345, 65536}, {2 << 20, 1 << 20}} {
			got := make([]byte, tc.n)
			want := make([]byte, tc.n)
			wn, werr := f.ReadAt(want, int64(tc.off))
			n, err, _ := r.ReadAt(int(f.Fd()), got, int64(tc.off))
			if n != wn || !errors.Is(err, werr) || !bytes.Equal(got[:n], data[tc.off:tc.off+n]) {
				t.Fatalf("off=%d: got n=%d err=%v", tc.off, n, err)
			}
		}
		stats := r.Stats()
		if stats.Submissions != 3 {
			t.Fatalf("submissions=%d, want 3", stats.Submissions)
		}
		requireReconciled(t, r)
	})
}

func TestEOFAndSQPressure(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		f, data := testFile(t)
		defer f.Close()
		r := testRing(t, 2, forceAsync)
		buf := make([]byte, 4096)
		n, err, _ := r.ReadAt(int(f.Fd()), buf, int64(len(data)-1024))
		if n != 1024 || !errors.Is(err, io.EOF) {
			t.Fatalf("boundary read: n=%d err=%v", n, err)
		}
		if _, err, _ := r.ReadAt(int(f.Fd()), buf, int64(len(data))); !errors.Is(err, io.EOF) {
			t.Fatalf("EOF read err=%v", err)
		}

		var wg sync.WaitGroup
		errs := make(chan error, 32)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				b := make([]byte, 64<<10)
				n, err, _ := r.ReadAt(int(f.Fd()), b, int64((i*65536)%len(data)))
				if err != nil || n != len(b) {
					errs <- err
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent read: %v", err)
		}
		requireReconciled(t, r)
	})
}

func TestConcurrentPublicationReturnsCorrectBytes(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		f, data := testFile(t)
		defer f.Close()
		r := testRing(t, 32, forceAsync)
		const clients = 32
		start := make(chan struct{})
		errs := make(chan error, clients)
		var wg sync.WaitGroup
		for i := 0; i < clients; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				buf := make([]byte, 4096)
				off := int64(i * len(buf))
				n, err, _ := r.ReadAt(int(f.Fd()), buf, off)
				if err != nil || n != len(buf) || !bytes.Equal(buf, data[off:off+int64(n)]) {
					errs <- fmt.Errorf("client=%d n=%d err=%v", i, n, err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		// All callers starting together does not imply they enqueue together:
		// the submitter may consume each request before the next is queued.
		// Join both ring loops before inspecting completed drain counters.
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		requirePublicationAccounting(t, r, clients)
		requireReconciled(t, r)
	})
}

func TestRepeatedCreateDestroy(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		f, _ := testFile(t)
		defer f.Close()
		for i := 0; i < 3; i++ {
			r := testRing(t, 4, forceAsync)
			b := make([]byte, 4096)
			if n, err, _ := r.ReadAt(int(f.Fd()), b, 0); err != nil || n != len(b) {
				t.Fatalf("iteration %d: n=%d err=%v", i, n, err)
			}
			requireReconciled(t, r)
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// This is a lifecycle regression rather than a throughput benchmark. It
// mirrors the failing shape: many logical callers, only four admitted I/Os,
// repeated requests, and forced GC while buffers are kernel-owned. On
// 649af01, a CQE could race the post-enter outstanding increment, remove the
// operation table's final reference, and let runtime.Pinner panic.
func TestSustainedReadAtLifecycleWithForcedGC(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		f, data := testFile(t)
		defer f.Close()
		const (
			clients     = 32
			outstanding = 4
			requests    = 32
		)
		for cycle := 0; cycle < 3; cycle++ {
			r := testRing(t, outstanding, forceAsync)
			permits := make(chan struct{}, outstanding)
			stopGC := make(chan struct{})
			var gcWG sync.WaitGroup
			gcWG.Add(1)
			go func() {
				defer gcWG.Done()
				for {
					runtime.GC()
					select {
					case <-stopGC:
						return
					default:
						runtime.Gosched()
					}
				}
			}()

			errs := make(chan error, clients)
			var wg sync.WaitGroup
			for client := 0; client < clients; client++ {
				wg.Add(1)
				go func(client int) {
					defer wg.Done()
					buf := make([]byte, 4096)
					for request := 0; request < requests; request++ {
						permits <- struct{}{}
						off := int64(((client*requests + request) * len(buf)) % (len(data) - len(buf)))
						n, err, _ := r.ReadAt(int(f.Fd()), buf, off)
						<-permits
						if err != nil || n != len(buf) || !bytes.Equal(buf, data[off:off+int64(n)]) {
							errs <- fmt.Errorf("cycle=%d client=%d request=%d n=%d err=%v", cycle, client, request, n, err)
							return
						}
					}
				}(client)
			}
			wg.Wait()
			close(stopGC)
			gcWG.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			requireReconciled(t, r)
			if err := r.Close(); err != nil {
				t.Fatalf("cycle %d close: %v", cycle, err)
			}
		}
	})
}

func TestCloseRejectsActiveOperationsThenAllowsDrain(t *testing.T) {
	forEachMode(t, func(t *testing.T, forceAsync bool) {
		f, data := testFile(t)
		defer f.Close()
		r := testRing(t, 2, forceAsync)
		// Hold a table-owned request before enqueueing it. Unlike polling a
		// busy ring, this cannot race the final completion and a successful Close.
		op := queueTestOperations(r, 1)[0]
		op.fd = int(f.Fd())
		if err := r.Close(); err == nil {
			t.Fatal("Close succeeded with admitted operations")
		}
		r.submitCh <- op
		c := <-op.done
		if c.err != nil || c.n != len(op.buf) || !bytes.Equal(op.buf, data[op.offset:op.offset+int64(c.n)]) {
			t.Fatalf("read after rejected Close: n=%d err=%v", c.n, c.err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("Close after drain: %v", err)
		}
		requirePublicationAccounting(t, r, 1)
		requireReconciled(t, r)
	})
}
