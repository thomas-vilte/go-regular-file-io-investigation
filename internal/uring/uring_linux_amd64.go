// Package uring is a deliberately small, Linux/amd64 io_uring wrapper used by
// the external experiment. It is not a production abstraction.
package uring

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	sysIOUringSetup    = 425
	sysIOUringEnter    = 426
	sysIOUringRegister = 427
	sysGettid          = 186 // __NR_gettid from asm/unistd_64.h

	ioUringOffSqRing = 0
	ioUringOffCqRing = 0x08000000
	ioUringOffSqes   = 0x10000000

	ioUringFeatSingleMmap = 1 << 0
	ioUringEnterGetevents = 1 << 0
	ioUringOpRead         = 22
	ioUringSQEAsync       = 1 << 4 // IOSQE_ASYNC

	// IORING_REGISTER_IOWQ_MAX_WORKERS is 19 in linux/io_uring.h. Its arg
	// is [bounded, unbounded], each an unsigned int.
	ioUringRegisterIOWQMaxWorkers = 19
)

type sqOffsets struct {
	Head, Tail, RingMask, RingEntries, Flags, Dropped, Array uint32
	Resv1                                                    uint32
	Resv2                                                    uint64
}

type cqOffsets struct {
	Head, Tail, RingMask, RingEntries, Overflow, Cqes, Flags uint32
	Resv1                                                    uint32
	Resv2                                                    uint64
}

type params struct {
	SqEntries, CqEntries, Flags, SqThreadCPU, SqThreadIdle, Features, WqFd uint32
	Resv                                                                   [3]uint32
	SqOff                                                                  sqOffsets
	CqOff                                                                  cqOffsets
}

// sqe is exactly the first 64 bytes of struct io_uring_sqe. This experiment
// only uses opcode, fd, off, addr, len and user_data.
type sqe struct {
	Opcode      uint8
	Flags       uint8
	Ioprio      uint16
	FD          int32
	Off         uint64
	Addr        uint64
	Len         uint32
	RWFlags     uint32
	UserData    uint64
	BufIndex    uint16
	Personality uint16
	SpliceFDIn  int32
	Addr3       uint64
	Pad2        uint64
}

type cqe struct {
	UserData uint64
	Res      int32
	Flags    uint32
}

// Stats contains counters reconciled by the experiment at shutdown.
type Stats struct {
	OperationsCreated                  uint64
	OperationsAdmitted                 uint64
	OperationsQueued                   uint64
	OperationsPublished                uint64
	OperationsCleanedBeforePublication uint64
	PinsCreated                        uint64
	PinsReleased                       uint64
	Submissions                        uint64
	Completions                        uint64
	SQFullWaits                        uint64
	EnterCalls                         uint64
	EnterSubmitted                     uint64
	SubmitEnterCalls                   uint64
	WaitEnterCalls                     uint64
	SubmitEntriesRequested             uint64
	SubmitEntriesConsumed              uint64
	SubmitBatches                      uint64
	SubmitBatchEntriesTotal            uint64
	SubmitBatchSizeMax                 uint64
	CompletionDrainBatches             uint64
	CompletionCQEsDrainedTotal         uint64
	CompletionBatchSizeMax             uint64
	PartialSubmitReturns               uint64
	ZeroSubmitReturns                  uint64
	SubmitBatchSizeBuckets             [11]uint64
	CompletionBatchSizeBuckets         [11]uint64
	MaxSQOccupancy                     uint64
	MaxOperationTable                  uint64
	OperationTable                     uint64
	PinnedOperations                   uint64
	UnknownIDs                         uint64
	DuplicateCQEs                      uint64
}

type operationState uint8

const (
	opPinnedLocal operationState = iota
	opAdmitted
	opQueued
	opPublished
	opTerminal
	opReleased
)

type operation struct {
	id           uint64
	fd           int
	buf          []byte
	offset       int64
	pinner       runtime.Pinner
	serviceStart int64
	done         chan completion
	state        operationState // protected by Ring.mu after admission
}

type completion struct {
	n         int
	err       error
	serviceNS uint64
}

type fatalHolder struct{ err error }

// Options selects explicitly requested diagnostic behavior. It is not an
// adaptive policy: the normal zero value preserves the original ring behavior.
type Options struct {
	ForceAsync          bool
	BoundedWorkers      *uint32 // nil leaves the kernel maximum unchanged
	UnboundedWorkers    *uint32 // nil leaves the kernel maximum unchanged
	LockSubmitterThread bool    // diagnostic only; does not change ring flags
	RecordIssuerTIDs    bool    // opt-in syscall-boundary observations
}

const maxRecordedTIDs = 64

// tidSet records a bounded diagnostic sample of distinct OS thread IDs. A
// truncation bit makes a cap observable rather than silently changing the
// meaning of the reported count.
type tidSet struct {
	mu        sync.Mutex
	values    map[int]struct{}
	truncated bool
}

func (s *tidSet) add(tid int) {
	if tid <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[int]struct{})
	}
	if _, ok := s.values[tid]; ok {
		return
	}
	if len(s.values) == maxRecordedTIDs {
		s.truncated = true
		return
	}
	s.values[tid] = struct{}{}
}

func (s *tidSet) snapshot() (values []int, truncated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values = make([]int, 0, len(s.values))
	for tid := range s.values {
		values = append(values, tid)
	}
	sort.Ints(values)
	return values, s.truncated
}

func currentTID() int {
	tid, _, errno := syscall.Syscall(sysGettid, 0, 0, 0)
	if errno != 0 {
		return 0
	}
	return int(tid)
}

// The callback is deliberately evaluated inside the guard: disabled recording
// performs neither gettid nor set bookkeeping. Tests inject a counting reader.
func recordIssuerTID(enabled bool, set *tidSet, gettid func() int) {
	if enabled {
		set.add(gettid())
	}
}

// IOWQWorkerConfig records registration API results, not a measured worker
// count. A zero-value requested field means no maximum was changed.
type IOWQWorkerConfig struct {
	BoundedWorkerLimitRequested            bool
	UnboundedWorkerLimitRequested          bool
	IOWQWorkerLimitQueryAttempted          bool
	IOWQWorkerLimitRegistrationAttempted   bool
	IOWQWorkerLimitRegistrationSupported   bool
	PreviousBoundedWorkerMax               *uint32
	PreviousUnboundedWorkerMax             *uint32
	RequestedBoundedWorkerMax              *uint32
	RequestedUnboundedWorkerMax            *uint32
	RegistrationReturnedBoundedWorkerMax   *uint32
	RegistrationReturnedUnboundedWorkerMax *uint32
	PostSetBoundedWorkerMax                *uint32
	PostSetUnboundedWorkerMax              *uint32
	QueryError                             string
	RegistrationError                      string
	PostSetQueryError                      string
}

type iowqRegisterFunc func(fd int, values *[2]uint32) error

// Ring has a single submission owner and a single completion consumer. An ID
// maps a CQE to a Go-owned operation; no Go pointer crosses user_data.
type Ring struct {
	fd                  int
	params              params
	forceAsync          bool
	lockSubmitterThread bool
	recordIssuerTIDs    bool
	iowq                IOWQWorkerConfig
	sqRing              []byte
	cqRing              []byte
	sqes                []byte

	submitCh chan *operation
	workCh   chan struct{}
	stopCh   chan struct{}
	doneCh   chan struct{}
	space    *sync.Cond

	mu          sync.Mutex // protects operations and lifecycle
	ops         map[uint64]*operation
	completed   map[uint64]struct{}
	retiredIDs  []uint64
	retiredNext int
	nextID      uint64
	outstanding uint64
	closed      bool
	closeMu     sync.Mutex

	stats Stats

	setupTID          int
	setupRegisterTIDs tidSet
	submitEnterTIDs   tidSet
	waitEnterTIDs     tidSet

	consumerErr atomic.Value // fatalHolder
	wg          sync.WaitGroup
}

func init() {
	if unsafe.Sizeof(params{}) != 120 || unsafe.Sizeof(sqe{}) != 64 || unsafe.Sizeof(cqe{}) != 16 {
		panic("io_uring ABI layout mismatch")
	}
}

func New(entries uint32) (*Ring, error) {
	return NewWithOptions(entries, Options{})
}

// NewWithOptions creates a ring for an explicitly selected diagnostic mode.
// ForceAsync sets IOSQE_ASYNC on every READ SQE; it changes neither ownership
// nor submission/completion scheduling.
func NewWithOptions(entries uint32, options Options) (*Ring, error) {
	if entries == 0 {
		return nil, errors.New("io_uring entries must be positive")
	}
	for _, requested := range []struct {
		name  string
		value *uint32
	}{
		{"bounded", options.BoundedWorkers},
		{"unbounded", options.UnboundedWorkers},
	} {
		if requested.value == nil {
			continue
		}
		if *requested.value == 0 {
			return nil, fmt.Errorf("io_uring %s worker maximum must be positive", requested.name)
		}
		if !options.ForceAsync {
			return nil, fmt.Errorf("io_uring %s worker maximum requires force-async mode", requested.name)
		}
	}
	var p params
	setupTID := currentTID()
	fd, _, errno := syscall.Syscall(sysIOUringSetup, uintptr(entries), uintptr(unsafe.Pointer(&p)), 0)
	if errno != 0 {
		return nil, errno
	}
	r := &Ring{fd: int(fd), params: p, forceAsync: options.ForceAsync, lockSubmitterThread: options.LockSubmitterThread, recordIssuerTIDs: options.RecordIssuerTIDs, setupTID: setupTID, submitCh: make(chan *operation, entries), workCh: make(chan struct{}, 1), stopCh: make(chan struct{}), doneCh: make(chan struct{}), ops: make(map[uint64]*operation), completed: make(map[uint64]struct{})}
	r.setupRegisterTIDs.add(setupTID)
	r.space = sync.NewCond(&sync.Mutex{})
	if err := r.mapRings(); err != nil {
		_ = syscall.Close(r.fd)
		return nil, err
	}
	// Query after the mappings exist but before any submitter or completion
	// goroutine can accept an operation. Passing [0, 0] is documented to
	// return current maxima without changing either one.
	if err := r.configureIOWQWorkers(options.BoundedWorkers, options.UnboundedWorkers, registerIOWQMaxWorkers); err != nil {
		r.unmap()
		_ = syscall.Close(r.fd)
		return nil, err
	}
	r.wg.Add(2)
	go r.submitLoop()
	go r.completeLoop()
	return r, nil
}

func (r *Ring) ForceAsync() bool { return r.forceAsync }

func (r *Ring) LockSubmitterThread() bool { return r.lockSubmitterThread }

func (r *Ring) RecordIssuerTIDs() bool { return r.recordIssuerTIDs }

func (r *Ring) IOWQWorkerConfig() IOWQWorkerConfig { return r.iowq }

// IssuerTIDs returns bounded, sorted sets captured directly before ring setup,
// register, submit-side enter, and CQ-wait enter syscalls. They identify Go
// task contexts issuing ring operations; they are not kernel worker counts.
func (r *Ring) IssuerTIDs() (setup int, setupRegister, submitEnter, waitEnter []int, truncated map[string]bool) {
	setupRegister, setupRegisterTruncated := r.setupRegisterTIDs.snapshot()
	submitEnter, submitEnterTruncated := r.submitEnterTIDs.snapshot()
	waitEnter, waitEnterTruncated := r.waitEnterTIDs.snapshot()
	return r.setupTID, setupRegister, submitEnter, waitEnter, map[string]bool{
		"setup_register": setupRegisterTruncated,
		"submit_enter":   submitEnterTruncated,
		"wait_enter":     waitEnterTruncated,
	}
}

func registerIOWQMaxWorkers(fd int, values *[2]uint32) error {
	_, _, errno := syscall.Syscall6(sysIOUringRegister, uintptr(fd), uintptr(ioUringRegisterIOWQMaxWorkers), uintptr(unsafe.Pointer(values)), 2, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func (r *Ring) configureIOWQWorkers(requestedBounded, requestedUnbounded *uint32, register iowqRegisterFunc) error {
	r.iowq.BoundedWorkerLimitRequested = requestedBounded != nil
	r.iowq.UnboundedWorkerLimitRequested = requestedUnbounded != nil
	r.iowq.IOWQWorkerLimitQueryAttempted = true
	values := [2]uint32{}
	r.setupRegisterTIDs.add(currentTID())
	if err := register(r.fd, &values); err != nil {
		r.iowq.QueryError = err.Error()
		r.iowq.IOWQWorkerLimitRegistrationSupported = false
		if requestedBounded != nil || requestedUnbounded != nil {
			return fmt.Errorf("query io-wq worker maxima: %w", err)
		}
		return nil
	}
	r.iowq.IOWQWorkerLimitRegistrationSupported = true
	r.iowq.PreviousBoundedWorkerMax = uint32Pointer(values[0])
	r.iowq.PreviousUnboundedWorkerMax = uint32Pointer(values[1])
	if requestedBounded == nil && requestedUnbounded == nil {
		return nil
	}

	requested := values
	if requestedBounded != nil {
		requested[0] = *requestedBounded
	}
	if requestedUnbounded != nil {
		requested[1] = *requestedUnbounded
	}
	r.iowq.RequestedBoundedWorkerMax = uint32Pointer(requested[0])
	r.iowq.RequestedUnboundedWorkerMax = uint32Pointer(requested[1])
	r.iowq.IOWQWorkerLimitRegistrationAttempted = true
	r.setupRegisterTIDs.add(currentTID())
	if err := register(r.fd, &requested); err != nil {
		r.iowq.RegistrationError = err.Error()
		return fmt.Errorf("set io-wq worker maxima: %w", err)
	}
	// The kernel overwrites the argument with the values that applied before
	// this registration. Retain them separately from our requested maximum.
	r.iowq.RegistrationReturnedBoundedWorkerMax = uint32Pointer(requested[0])
	r.iowq.RegistrationReturnedUnboundedWorkerMax = uint32Pointer(requested[1])

	// A successful set returns the *previous* maxima in requested. Query again
	// before any READ can be admitted so the benchmark never labels a run with
	// an unverified worker configuration.
	postSet := [2]uint32{}
	r.setupRegisterTIDs.add(currentTID())
	if err := register(r.fd, &postSet); err != nil {
		r.iowq.PostSetQueryError = err.Error()
		return fmt.Errorf("verify io-wq worker maxima after registration: %w", err)
	}
	r.iowq.PostSetBoundedWorkerMax = uint32Pointer(postSet[0])
	r.iowq.PostSetUnboundedWorkerMax = uint32Pointer(postSet[1])
	if postSet != [2]uint32{r.iowqRequestedBounded(), r.iowqRequestedUnbounded()} {
		return fmt.Errorf("io-wq worker maxima verification failed: got [%d %d], want [%d %d]", postSet[0], postSet[1], r.iowqRequestedBounded(), r.iowqRequestedUnbounded())
	}
	return nil
}

func (r *Ring) iowqRequestedBounded() uint32 { return *r.iowq.RequestedBoundedWorkerMax }

func (r *Ring) iowqRequestedUnbounded() uint32 { return *r.iowq.RequestedUnboundedWorkerMax }

func uint32Pointer(v uint32) *uint32 { return &v }

func (r *Ring) mapRings() error {
	sqSize := int(r.params.SqOff.Array + r.params.SqEntries*4)
	cqSize := int(r.params.CqOff.Cqes + r.params.CqEntries*uint32(unsafe.Sizeof(cqe{})))
	if r.params.Features&ioUringFeatSingleMmap != 0 {
		size := sqSize
		if cqSize > size {
			size = cqSize
		}
		m, err := syscall.Mmap(r.fd, ioUringOffSqRing, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			return err
		}
		r.sqRing, r.cqRing = m, m
	} else {
		var err error
		r.sqRing, err = syscall.Mmap(r.fd, ioUringOffSqRing, sqSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			return err
		}
		r.cqRing, err = syscall.Mmap(r.fd, ioUringOffCqRing, cqSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			_ = syscall.Munmap(r.sqRing)
			return err
		}
	}
	var err error
	r.sqes, err = syscall.Mmap(r.fd, ioUringOffSqes, int(r.params.SqEntries*uint32(unsafe.Sizeof(sqe{}))), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		r.unmap()
		return err
	}
	return nil
}

func (r *Ring) unmap() {
	if r.sqes != nil {
		_ = syscall.Munmap(r.sqes)
		r.sqes = nil
	}
	if r.sqRing != nil {
		_ = syscall.Munmap(r.sqRing)
	}
	if r.cqRing != nil && len(r.cqRing) > 0 && (len(r.sqRing) == 0 || unsafe.Pointer(&r.cqRing[0]) != unsafe.Pointer(&r.sqRing[0])) {
		_ = syscall.Munmap(r.cqRing)
	}
	r.sqRing, r.cqRing = nil, nil
}

// ReadAt pins buf until a terminal CQE and returns os.File.ReadAt-compatible
// EOF behavior for short successful reads.
func (r *Ring) ReadAt(fd int, buf []byte, offset int64) (int, error, uint64) {
	if len(buf) == 0 {
		return 0, nil, 0
	}
	if err := r.fatal(); err != nil {
		return 0, err, 0
	}
	op := &operation{fd: fd, buf: buf, offset: offset, done: make(chan completion, 1)}
	atomic.AddUint64(&r.stats.OperationsCreated, 1)
	op.pinner.Pin(&buf[0])
	op.state = opPinnedLocal
	atomic.AddUint64(&r.stats.PinsCreated, 1)
	atomic.AddUint64(&r.stats.PinnedOperations, 1)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.releaseBeforePublication(op)
		return 0, errors.New("io_uring ring closed"), 0
	}
	r.nextID++
	if r.nextID == 0 {
		r.mu.Unlock()
		r.releaseBeforePublication(op)
		return 0, errors.New("operation ID overflow"), 0
	}
	op.id = r.nextID
	r.ops[op.id] = op
	op.state = opAdmitted
	atomic.AddUint64(&r.stats.OperationsAdmitted, 1)
	// The table becomes the persistent owner before this operation can block
	// trying to enqueue. Its ownership lasts through the terminal CQE.
	op.state = opQueued
	atomic.AddUint64(&r.stats.OperationsQueued, 1)
	r.updateMaxOps(uint64(len(r.ops)))
	r.mu.Unlock()

	// The send is bounded by the configured ring capacity. It cannot be lost:
	// Close is only permitted once all submitted operations have terminated.
	r.submitCh <- op
	c := <-op.done
	return c.n, c.err, c.serviceNS
}

// releaseBeforePublication handles the only paths where the kernel has never
// been able to see the buffer address. The caller is still the sole owner.
func (r *Ring) releaseBeforePublication(op *operation) {
	if op.state != opPinnedLocal {
		panic(fmt.Sprintf("io_uring: pre-publication cleanup in state %d", op.state))
	}
	op.pinner.Unpin()
	op.state = opReleased
	atomic.AddUint64(&r.stats.OperationsCleanedBeforePublication, 1)
	atomic.AddUint64(&r.stats.PinsReleased, 1)
	atomic.AddUint64(&r.stats.PinnedOperations, ^uint64(0))
}

func (r *Ring) submitLoop() {
	defer r.wg.Done()
	if r.lockSubmitterThread {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}
	pending := make([]*operation, 0, r.params.SqEntries)
	for {
		if len(pending) == 0 {
			select {
			case op := <-r.submitCh:
				if op == nil {
					return
				}
				pending = append(pending, op)
			case <-r.stopCh:
				return
			}
		}

		// There is deliberately no timer or blocking receive here. A batch is
		// formed only from work that is already immediately available.
		for len(pending) < int(r.params.SqEntries) {
			select {
			case op := <-r.submitCh:
				if op != nil {
					pending = append(pending, op)
				}
			default:
				goto submit
			}
		}

	submit:
		published, err := r.submitBatch(pending)
		if err != nil {
			// SQ.tail may already expose every published operation. An enter
			// error is never permission to remove it from Ring.ops or unpin it.
			panic(fmt.Sprintf("io_uring enter after SQ publication: %v", err))
		}
		if published == 0 {
			r.waitForSQSpace()
			continue
		}
		pending = pending[published:]
	}
}

func (r *Ring) waitForSQSpace() {
	r.space.L.Lock()
	for r.loadSq(r.params.SqOff.Tail)-r.loadSq(r.params.SqOff.Head) >= r.params.SqEntries {
		atomic.AddUint64(&r.stats.SQFullWaits, 1)
		r.space.Wait()
	}
	r.space.L.Unlock()
}

// submitBatch prepares and publishes only the prefix that fits in the SQ. The
// return value is the number made kernel-visible; every such operation remains
// owned by Ring.ops until a terminal CQE, even if enter consumes only a prefix.
func (r *Ring) submitBatch(pending []*operation) (int, error) {
	head := r.loadSq(r.params.SqOff.Head)
	tail := r.loadSq(r.params.SqOff.Tail)
	available := r.params.SqEntries - (tail - head)
	if available == 0 {
		return 0, nil
	}
	n := len(pending)
	if uint32(n) > available {
		n = int(available)
	}
	batch := pending[:n]
	for i, op := range batch {
		idx := (tail + uint32(i)) & r.loadSq(r.params.SqOff.RingMask)
		s := (*sqe)(unsafe.Pointer(&r.sqes[int(idx)*int(unsafe.Sizeof(sqe{}))]))
		*s = sqe{Opcode: ioUringOpRead, Flags: readSQEFlags(r.forceAsync), FD: int32(op.fd), Off: uint64(op.offset), Addr: uint64(uintptr(unsafe.Pointer(&op.buf[0]))), Len: uint32(len(op.buf)), UserData: op.id}
		*(*uint32)(unsafe.Pointer(&r.sqRing[int(r.params.SqOff.Array)+int(idx)*4])) = idx
		op.serviceStart = time.Now().UnixNano()
	}

	// Do this for every SQE before the single release-store that exposes the
	// batch. A CQE may race immediately after that store.
	r.mu.Lock()
	for _, op := range batch {
		if r.ops[op.id] != op || op.state != opQueued {
			r.mu.Unlock()
			panic(fmt.Sprintf("io_uring: publish operation %d in invalid state", op.id))
		}
	}
	for _, op := range batch {
		op.state = opPublished
		r.outstanding++
	}
	r.mu.Unlock()

	atomic.StoreUint32((*uint32)(unsafe.Pointer(&r.sqRing[r.params.SqOff.Tail])), tail+uint32(n))
	atomic.AddUint64(&r.stats.OperationsPublished, uint64(n))
	atomic.AddUint64(&r.stats.Submissions, uint64(n))
	atomic.AddUint64(&r.stats.SubmitBatches, 1)
	atomic.AddUint64(&r.stats.SubmitBatchEntriesTotal, uint64(n))
	r.updateMax(&r.stats.SubmitBatchSizeMax, uint64(n))
	r.recordBatch(&r.stats.SubmitBatchSizeBuckets, n)
	r.updateMaxSQ(tail + uint32(n) - head)

	if err := r.enterSubmit(uint32(n)); err != nil {
		return n, err
	}
	select {
	case r.workCh <- struct{}{}:
	default:
	}
	return n, nil
}

func readSQEFlags(forceAsync bool) uint8 {
	if forceAsync {
		return ioUringSQEAsync
	}
	return 0
}

func (r *Ring) enterSubmit(remaining uint32) error {
	for remaining > 0 {
		recordIssuerTID(r.recordIssuerTIDs, &r.submitEnterTIDs, currentTID)
		atomic.AddUint64(&r.stats.EnterCalls, 1)
		atomic.AddUint64(&r.stats.SubmitEnterCalls, 1)
		atomic.AddUint64(&r.stats.SubmitEntriesRequested, uint64(remaining))
		n, _, errno := syscall.Syscall6(sysIOUringEnter, uintptr(r.fd), uintptr(remaining), 0, 0, 0, 0)
		if errno != 0 {
			return errno
		}
		var err error
		remaining, err = r.accountSubmitResult(remaining, n)
		if err != nil {
			return err
		}
	}
	return nil
}

// accountSubmitResult is kept separate so partial-submit accounting has pure
// unit coverage even where the kernel syscall is unavailable.
func (r *Ring) accountSubmitResult(remaining uint32, n uintptr) (uint32, error) {
	if n > uintptr(remaining) {
		return remaining, fmt.Errorf("io_uring_enter consumed %d of %d", n, remaining)
	}
	if n == 0 {
		atomic.AddUint64(&r.stats.ZeroSubmitReturns, 1)
		return remaining, errors.New("io_uring_enter consumed zero published SQEs")
	}
	consumed := uint32(n)
	atomic.AddUint64(&r.stats.EnterSubmitted, uint64(consumed))
	atomic.AddUint64(&r.stats.SubmitEntriesConsumed, uint64(consumed))
	if consumed < remaining {
		atomic.AddUint64(&r.stats.PartialSubmitReturns, 1)
	}
	return remaining - consumed, nil
}

func (r *Ring) completeLoop() {
	defer r.wg.Done()
	defer close(r.doneCh)
	for {
		if r.reap() > 0 {
			continue
		}
		r.mu.Lock()
		outstanding, done := r.outstanding, r.closed && r.outstanding == 0
		r.mu.Unlock()
		if done {
			return
		}
		if outstanding == 0 {
			select {
			case <-r.workCh:
				continue
			case <-r.stopCh:
				return
			}
		}
		atomic.AddUint64(&r.stats.EnterCalls, 1)
		atomic.AddUint64(&r.stats.WaitEnterCalls, 1)
		recordIssuerTID(r.recordIssuerTIDs, &r.waitEnterTIDs, currentTID)
		_, _, errno := syscall.Syscall6(sysIOUringEnter, uintptr(r.fd), 0, 1, ioUringEnterGetevents, 0, 0)
		if errno != 0 && errno != syscall.EINTR {
			r.setFatal(errno)
			return
		}
	}
}

func (r *Ring) reap() int {
	head := r.loadCq(r.params.CqOff.Head)
	tail := r.loadCq(r.params.CqOff.Tail)
	count := 0
	for head != tail {
		idx := head & r.loadCq(r.params.CqOff.RingMask)
		q := *(*cqe)(unsafe.Pointer(&r.cqRing[int(r.params.CqOff.Cqes)+int(idx)*int(unsafe.Sizeof(cqe{}))]))
		r.finish(q)
		head++
		count++
	}
	if count > 0 {
		atomic.StoreUint32((*uint32)(unsafe.Pointer(&r.cqRing[r.params.CqOff.Head])), head)
		atomic.AddUint64(&r.stats.CompletionDrainBatches, 1)
		atomic.AddUint64(&r.stats.CompletionCQEsDrainedTotal, uint64(count))
		r.updateMax(&r.stats.CompletionBatchSizeMax, uint64(count))
		r.recordBatch(&r.stats.CompletionBatchSizeBuckets, count)
		r.space.L.Lock()
		r.space.Broadcast()
		r.space.L.Unlock()
	}
	return count
}

func (r *Ring) finish(q cqe) {
	r.mu.Lock()
	op, ok := r.ops[q.UserData]
	if !ok {
		if _, duplicate := r.completed[q.UserData]; duplicate {
			atomic.AddUint64(&r.stats.DuplicateCQEs, 1)
		} else {
			atomic.AddUint64(&r.stats.UnknownIDs, 1)
		}
		r.mu.Unlock()
		panic(fmt.Sprintf("io_uring: unknown or duplicate CQE operation ID %d", q.UserData))
	}
	if op.state != opPublished {
		r.mu.Unlock()
		panic(fmt.Sprintf("io_uring: completion for operation %d in state %d", q.UserData, op.state))
	}
	if r.outstanding == 0 {
		// Preserve the table entry and its Pinner when reporting an internal
		// invariant failure. Removing it here was the leak path fixed by this
		// revision.
		r.mu.Unlock()
		panic("io_uring: completion with no kernel outstanding operation")
	}
	delete(r.ops, q.UserData)
	// Keep a bounded retirement window to classify nearby duplicate CQEs. A
	// later duplicate that falls outside it is still detected as an unknown ID
	// and remains fatal, without making the operation table grow unboundedly.
	if len(r.completed) < int(r.params.CqEntries) {
		r.retiredIDs = append(r.retiredIDs, q.UserData)
	} else {
		old := r.retiredIDs[r.retiredNext]
		delete(r.completed, old)
		r.retiredIDs[r.retiredNext] = q.UserData
		r.retiredNext = (r.retiredNext + 1) % len(r.retiredIDs)
	}
	r.completed[q.UserData] = struct{}{}
	r.outstanding--
	op.state = opTerminal
	r.mu.Unlock()
	atomic.AddUint64(&r.stats.Completions, 1)
	n := int(q.Res)
	var err error
	if q.Res < 0 {
		n = 0
		err = syscall.Errno(-q.Res)
	} else if n < len(op.buf) {
		err = io.EOF
	}
	serviceNS := uint64(time.Now().UnixNano() - op.serviceStart)
	op.pinner.Unpin()
	op.state = opReleased
	atomic.AddUint64(&r.stats.PinsReleased, 1)
	atomic.AddUint64(&r.stats.PinnedOperations, ^uint64(0))
	op.done <- completion{n: n, err: err, serviceNS: serviceNS}
}

func (r *Ring) loadSq(off uint32) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(&r.sqRing[off])))
}
func (r *Ring) loadCq(off uint32) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(&r.cqRing[off])))
}
func (r *Ring) updateMaxSQ(v uint32) {
	r.updateMax(&r.stats.MaxSQOccupancy, uint64(v))
}

func (r *Ring) updateMax(counter *uint64, v uint64) {
	for {
		old := atomic.LoadUint64(counter)
		if v <= old || atomic.CompareAndSwapUint64(counter, old, v) {
			return
		}
	}
}

// recordBatch uses fixed buckets so instrumentation adds no per-operation
// allocation or tracing. Bucket order: 1, 2, 3-4, 5-8, ..., 257-512, 513+.
func (r *Ring) recordBatch(buckets *[11]uint64, n int) {
	i := batchBucket(n)
	atomic.AddUint64(&buckets[i], 1)
}

func batchBucket(n int) int {
	switch {
	case n <= 1:
		return 0
	case n == 2:
		return 1
	case n <= 4:
		return 2
	case n <= 8:
		return 3
	case n <= 16:
		return 4
	case n <= 32:
		return 5
	case n <= 64:
		return 6
	case n <= 128:
		return 7
	case n <= 256:
		return 8
	case n <= 512:
		return 9
	default:
		return 10
	}
}

func (r *Ring) updateMaxOps(v uint64) {
	for {
		old := atomic.LoadUint64(&r.stats.MaxOperationTable)
		if v <= old || atomic.CompareAndSwapUint64(&r.stats.MaxOperationTable, old, v) {
			return
		}
	}
}

func (r *Ring) Stats() Stats {
	r.mu.Lock()
	table := uint64(len(r.ops))
	r.mu.Unlock()
	return Stats{
		OperationsCreated:                  atomic.LoadUint64(&r.stats.OperationsCreated),
		OperationsAdmitted:                 atomic.LoadUint64(&r.stats.OperationsAdmitted),
		OperationsQueued:                   atomic.LoadUint64(&r.stats.OperationsQueued),
		OperationsPublished:                atomic.LoadUint64(&r.stats.OperationsPublished),
		OperationsCleanedBeforePublication: atomic.LoadUint64(&r.stats.OperationsCleanedBeforePublication),
		PinsCreated:                        atomic.LoadUint64(&r.stats.PinsCreated),
		PinsReleased:                       atomic.LoadUint64(&r.stats.PinsReleased),
		Submissions:                        atomic.LoadUint64(&r.stats.Submissions),
		Completions:                        atomic.LoadUint64(&r.stats.Completions),
		SQFullWaits:                        atomic.LoadUint64(&r.stats.SQFullWaits),
		EnterCalls:                         atomic.LoadUint64(&r.stats.EnterCalls),
		EnterSubmitted:                     atomic.LoadUint64(&r.stats.EnterSubmitted),
		SubmitEnterCalls:                   atomic.LoadUint64(&r.stats.SubmitEnterCalls),
		WaitEnterCalls:                     atomic.LoadUint64(&r.stats.WaitEnterCalls),
		SubmitEntriesRequested:             atomic.LoadUint64(&r.stats.SubmitEntriesRequested),
		SubmitEntriesConsumed:              atomic.LoadUint64(&r.stats.SubmitEntriesConsumed),
		SubmitBatches:                      atomic.LoadUint64(&r.stats.SubmitBatches),
		SubmitBatchEntriesTotal:            atomic.LoadUint64(&r.stats.SubmitBatchEntriesTotal),
		SubmitBatchSizeMax:                 atomic.LoadUint64(&r.stats.SubmitBatchSizeMax),
		CompletionDrainBatches:             atomic.LoadUint64(&r.stats.CompletionDrainBatches),
		CompletionCQEsDrainedTotal:         atomic.LoadUint64(&r.stats.CompletionCQEsDrainedTotal),
		CompletionBatchSizeMax:             atomic.LoadUint64(&r.stats.CompletionBatchSizeMax),
		PartialSubmitReturns:               atomic.LoadUint64(&r.stats.PartialSubmitReturns),
		ZeroSubmitReturns:                  atomic.LoadUint64(&r.stats.ZeroSubmitReturns),
		SubmitBatchSizeBuckets:             loadBatchBuckets(&r.stats.SubmitBatchSizeBuckets),
		CompletionBatchSizeBuckets:         loadBatchBuckets(&r.stats.CompletionBatchSizeBuckets),
		MaxSQOccupancy:                     atomic.LoadUint64(&r.stats.MaxSQOccupancy),
		MaxOperationTable:                  atomic.LoadUint64(&r.stats.MaxOperationTable),
		OperationTable:                     table,
		PinnedOperations:                   atomic.LoadUint64(&r.stats.PinnedOperations),
		UnknownIDs:                         atomic.LoadUint64(&r.stats.UnknownIDs),
		DuplicateCQEs:                      atomic.LoadUint64(&r.stats.DuplicateCQEs),
	}
}

func loadBatchBuckets(source *[11]uint64) (out [11]uint64) {
	for i := range out {
		out[i] = atomic.LoadUint64(&source[i])
	}
	return out
}

func (r *Ring) setFatal(err error) {
	if err == nil || r.consumerErr.Load() != nil {
		return
	}
	r.consumerErr.Store(fatalHolder{err: err})
}

func (r *Ring) fatal() error {
	if v := r.consumerErr.Load(); v != nil {
		return v.(fatalHolder).err
	}
	return nil
}

func (r *Ring) Outstanding() uint64 { r.mu.Lock(); defer r.mu.Unlock(); return r.outstanding }

func (r *Ring) Close() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return nil
	}
	if fatal := r.fatal(); fatal != nil {
		return fatal
	}
	r.mu.Lock()
	if len(r.ops) != 0 || r.outstanding != 0 {
		err := fmt.Errorf("close with operations=%d outstanding=%d", len(r.ops), r.outstanding)
		r.mu.Unlock()
		return err
	}
	r.closed = true
	r.mu.Unlock()
	close(r.stopCh)
	r.space.L.Lock()
	r.space.Broadcast()
	r.space.L.Unlock()
	<-r.doneCh
	r.wg.Wait()
	r.unmap()
	return syscall.Close(r.fd)
}
