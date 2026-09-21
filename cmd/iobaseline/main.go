// iobaseline is a Linux-only scenario harness for observing current Go file-I/O
// behavior. It deliberately has no io_uring implementation.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"example.com/go-iouring-investigation/bench/internal/uring"
)

const schemaVersion = 2

type config struct {
	File                     string
	Operation                string
	Backend                  string
	FileAccess               string
	BufferBytes              int
	Concurrency              int
	MaxOutstanding           int
	GOMAXPROCS               int
	Warmup                   time.Duration
	Duration                 time.Duration
	Operations               uint64
	Seed                     uint64
	CacheState               string
	CachePrecondition        string
	AllowFadviseDontneed     bool
	Latency                  bool
	URingForceAsync          bool
	URingBoundedWorkers      int
	URingBoundedWorkersSet   bool
	URingUnboundedWorkers    int
	URingUnboundedWorkersSet bool
	URingLockSubmitterThread bool
	URingRecordIssuerTIDs    bool
	SampleInterval           time.Duration
	HarnessRevision          string
	Child                    bool
}

type procStatus struct {
	Threads, VmRSSKiB, VoluntaryCS, InvoluntaryCS uint64
}

type rusage struct {
	UserNS, SystemNS, VoluntaryCS, InvoluntaryCS, MaxRSSKiB int64
}

type metricValue struct {
	Available bool       `json:"available"`
	Kind      string     `json:"kind,omitempty"`
	Uint64    *uint64    `json:"uint64,omitempty"`
	Float64   *float64   `json:"float64,omitempty"`
	Histogram *histogram `json:"histogram,omitempty"`
}

// Bucket boundaries include +/-Inf, which encoding/json cannot represent as
// floats. Preserve them as strings rather than silently dropping the metric.
type histogram struct {
	Buckets []string `json:"buckets_seconds"`
	Counts  []uint64 `json:"counts"`
}
type metricsSnapshot struct {
	Values map[string]metricValue `json:"values"`
}

type observer struct {
	Enabled    bool   `json:"enabled"`
	IntervalMS int64  `json:"interval_ms"`
	Samples    uint64 `json:"sample_count"`
	MaxThreads uint64 `json:"max_observed_threads"`
	MaxRSSKiB  uint64 `json:"max_observed_rss_kib"`
}

type result struct {
	SchemaVersion int            `json:"schema_version"`
	Status        string         `json:"status"`
	Error         string         `json:"error,omitempty"`
	StartedAtUTC  string         `json:"started_at_utc"`
	DurationNS    int64          `json:"duration_ns"`
	Config        map[string]any `json:"config"`
	Environment   map[string]any `json:"environment"`
	Dataset       map[string]any `json:"dataset"`
	Work          map[string]any `json:"work"`
	Latency       map[string]any `json:"latency"`
	URing         map[string]any `json:"io_uring,omitempty"`
	Runtime       struct {
		BeforeSpawn    metricsSnapshot   `json:"before_spawn"`
		Ready          metricsSnapshot   `json:"ready"`
		End            metricsSnapshot   `json:"end"`
		Peak           map[string]uint64 `json:"peak_observed_by_internal_sampler"`
		SchedulerDelta *histogram        `json:"scheduler_latency_histogram_delta,omitempty"`
	} `json:"runtime"`
	Process struct {
		Before       procStatus `json:"proc_status_before"`
		End          procStatus `json:"proc_status_end"`
		RusageBefore rusage     `json:"rusage_before"`
		RusageEnd    rusage     `json:"rusage_end"`
		Observer     observer   `json:"external_observer"`
	} `json:"process"`
	Limitations []string `json:"limitations"`
}

var metricNames = []string{
	"/sched/threads/total:threads", "/sched/goroutines:goroutines",
	"/sched/goroutines/not-in-go:goroutines", "/sched/goroutines/runnable:goroutines",
	"/sched/goroutines/running:goroutines", "/sched/goroutines/waiting:goroutines",
	"/sched/latencies:seconds", "/gc/cycles/total:gc-cycles",
	"/cpu/classes/gc/total:cpu-seconds", "/cpu/classes/user:cpu-seconds",
	"/memory/classes/heap/stacks:bytes", "/memory/classes/heap/objects:bytes",
}

func main() {
	c := parseFlags()
	if c.Child {
		emit(runChild(c))
		return
	}
	emit(runObserved(c))
}

func parseFlags() config {
	c := config{URingBoundedWorkers: -1, URingUnboundedWorkers: -1}
	flag.StringVar(&c.File, "file", "", "existing input file")
	flag.StringVar(&c.Operation, "operation", "readat", "operation: readat or read")
	flag.StringVar(&c.Backend, "backend", "blocking", "I/O backend: blocking or uring")
	flag.StringVar(&c.FileAccess, "file-access", "shared_fd", "shared_fd or per_worker_fd (read control only)")
	flag.IntVar(&c.BufferBytes, "buffer-bytes", 4096, "per-worker buffer size")
	flag.IntVar(&c.Concurrency, "concurrency", 1, "workers")
	flag.IntVar(&c.MaxOutstanding, "max-outstanding", 0, "maximum simultaneously executing ReadAt calls; zero means concurrency")
	flag.IntVar(&c.GOMAXPROCS, "gomaxprocs", runtime.GOMAXPROCS(0), "GOMAXPROCS")
	flag.DurationVar(&c.Warmup, "warmup", 0, "unmeasured warmup duration")
	flag.DurationVar(&c.Duration, "duration", 3*time.Second, "measurement duration; ignored if operations is non-zero")
	flag.Uint64Var(&c.Operations, "operations", 0, "total fixed operations; required for latency mode")
	flag.Uint64Var(&c.Seed, "seed", 1, "deterministic offset seed")
	flag.StringVar(&c.CacheState, "cache-state", "not_controlled", "caller-declared cache state")
	flag.StringVar(&c.CachePrecondition, "cache-precondition", "none", "none, prewarm, or dontneed (only experiment-owned files)")
	flag.BoolVar(&c.AllowFadviseDontneed, "allow-fadvise-dontneed", false, "permit POSIX_FADV_DONTNEED on an experiment-owned input file")
	flag.BoolVar(&c.Latency, "latency", false, "timestamp every I/O; use only in separate latency runs")
	flag.BoolVar(&c.URingForceAsync, "uring-force-async", false, "set IOSQE_ASYNC on every io_uring READ SQE (diagnostic only)")
	flag.BoolVar(&c.URingLockSubmitterThread, "uring-lock-submitter-thread", false, "lock the single io_uring submitter goroutine to one OS thread (diagnostic only)")
	flag.BoolVar(&c.URingRecordIssuerTIDs, "uring-record-issuer-tids", false, "record sampled issuer TIDs before enter syscalls (diagnostic only)")
	flag.Func("uring-bounded-workers", "set bounded io-wq workers with force-async; negative leaves kernel defaults unchanged", func(value string) error {
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		c.URingBoundedWorkers = n
		c.URingBoundedWorkersSet = true
		return nil
	})
	flag.Func("uring-unbounded-workers", "set unbounded io-wq workers with force-async; negative leaves kernel defaults unchanged", func(value string) error {
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		c.URingUnboundedWorkers = n
		c.URingUnboundedWorkersSet = true
		return nil
	})
	flag.DurationVar(&c.SampleInterval, "runtime-sample-interval", 10*time.Millisecond, "internal runtime metric sampling interval; zero disables")
	flag.StringVar(&c.HarnessRevision, "harness-revision", "unknown", "source revision recorded with this run")
	flag.BoolVar(&c.Child, "child", false, "internal: execute workload without the external observer")
	flag.Parse()
	return c
}

func validate(c config) error {
	if c.File == "" || (c.Operation != "readat" && c.Operation != "read") || c.BufferBytes <= 0 || c.Concurrency <= 0 || c.GOMAXPROCS <= 0 {
		return errors.New("file, readat/read, positive buffer/concurrency/gomaxprocs are required")
	}
	if c.FileAccess != "shared_fd" && c.FileAccess != "per_worker_fd" {
		return errors.New("file-access must be shared_fd or per_worker_fd")
	}
	if c.Backend != "blocking" && c.Backend != "uring" {
		return errors.New("backend must be blocking or uring")
	}
	if c.URingForceAsync && c.Backend != "uring" {
		return errors.New("uring-force-async requires backend=uring")
	}
	if c.URingLockSubmitterThread && c.Backend != "uring" {
		return errors.New("uring-lock-submitter-thread requires backend=uring")
	}
	if c.URingRecordIssuerTIDs && c.Backend != "uring" {
		return errors.New("uring-record-issuer-tids requires backend=uring")
	}
	for _, workerOption := range []struct {
		name  string
		set   bool
		value int
	}{
		{"uring-bounded-workers", c.URingBoundedWorkersSet, c.URingBoundedWorkers},
		{"uring-unbounded-workers", c.URingUnboundedWorkersSet, c.URingUnboundedWorkers},
	} {
		if !workerOption.set {
			continue
		}
		if workerOption.value == 0 {
			return fmt.Errorf("%s must be negative or at least one", workerOption.name)
		}
		if workerOption.value >= 1 {
			if c.Backend != "uring" {
				return fmt.Errorf("%s requires backend=uring", workerOption.name)
			}
			if !c.URingForceAsync {
				return fmt.Errorf("%s requires uring-force-async", workerOption.name)
			}
			if uint64(workerOption.value) > uint64(^uint32(0)) {
				return fmt.Errorf("%s exceeds uint32", workerOption.name)
			}
		}
	}
	if c.Backend == "uring" && c.Operation != "readat" {
		return errors.New("backend=uring supports only operation=readat")
	}
	if c.Operation == "readat" && c.FileAccess != "shared_fd" {
		return errors.New("readat control uses one shared file descriptor")
	}
	if c.Operations == 0 && c.Duration <= 0 {
		return errors.New("duration or operations must be positive")
	}
	if c.Latency && c.Operations == 0 {
		return errors.New("latency mode requires fixed -operations to preallocate samples")
	}
	if c.MaxOutstanding < 0 || (c.MaxOutstanding > 0 && c.MaxOutstanding > c.Concurrency) {
		return errors.New("max-outstanding must be zero or between one and concurrency")
	}
	if c.CachePrecondition != "none" && c.CachePrecondition != "prewarm" && c.CachePrecondition != "dontneed" {
		return errors.New("cache-precondition must be none, prewarm, or dontneed")
	}
	if c.CachePrecondition == "dontneed" && !c.AllowFadviseDontneed {
		return errors.New("dontneed requires -allow-fadvise-dontneed and an experiment-owned file")
	}
	return nil
}

func runObserved(c config) result {
	if err := validate(c); err != nil {
		return failed(c, err)
	}
	exe, err := os.Executable()
	if err != nil {
		return failed(c, err)
	}
	args := append(append([]string{}, os.Args[1:]...), "-child")
	cmd := exec.Command(exe, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return failed(c, err)
	}
	obs := observer{Enabled: true, IntervalMS: 10}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return failed(c, fmt.Errorf("child: %w", err))
			}
			var r result
			if err := json.Unmarshal(out.Bytes(), &r); err != nil {
				return failed(c, fmt.Errorf("decode child JSON: %w", err))
			}
			r.Process.Observer = obs
			return r
		case <-t.C:
			if s, err := readProcStatus(cmd.Process.Pid); err == nil {
				obs.Samples++
				if s.Threads > obs.MaxThreads {
					obs.MaxThreads = s.Threads
				}
				if s.VmRSSKiB > obs.MaxRSSKiB {
					obs.MaxRSSKiB = s.VmRSSKiB
				}
			}
		}
	}
}

func runChild(c config) result {
	if err := validate(c); err != nil {
		return failed(c, err)
	}
	runtime.GOMAXPROCS(c.GOMAXPROCS)
	f, err := os.Open(c.File)
	if err != nil {
		return failed(c, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return failed(c, err)
	}
	if st.Size() < int64(c.BufferBytes) {
		return failed(c, errors.New("input file is smaller than buffer"))
	}
	slots := uint64(st.Size() / int64(c.BufferBytes))
	if slots == 0 {
		return failed(c, errors.New("no complete read slots"))
	}
	r := baseResult(c, st.Size(), slots)
	applyCachePrecondition(&r, f, st.Size(), c)
	r.Runtime.BeforeSpawn = snapshotMetrics()
	r.Process.Before, _ = readProcStatus(os.Getpid())
	r.Process.RusageBefore = readRusage()
	maxOutstanding := c.MaxOutstanding
	if maxOutstanding == 0 {
		maxOutstanding = c.Concurrency
	}
	limiter := newOutstandingLimiter(maxOutstanding)
	var ring *uring.Ring
	if c.Backend == "uring" {
		ring, err = uring.NewWithOptions(uint32(maxOutstanding), ringOptions(c))
		if err != nil {
			return failed(c, fmt.Errorf("io_uring setup: %w", err))
		}
	}
	workers := make([]worker, c.Concurrency)
	for i := range workers {
		wf := f
		ownsFile := false
		if c.Operation == "read" && c.FileAccess == "per_worker_fd" {
			wf, err = os.Open(c.File)
			if err != nil {
				return failed(c, err)
			}
			ownsFile = true
		}
		workers[i] = worker{file: wf, buf: make([]byte, c.BufferBytes), seed: c.Seed + uint64(i+1)*0x9e3779b97f4a7c15, slots: slots, latency: c.Latency, limiter: limiter, operation: c.Operation, ownsFile: ownsFile, backend: c.Backend, ring: ring}
	}
	r.Dataset["residency_after_buffer_allocation"] = inspectResidency(f, st.Size())
	defer func() {
		for i := range workers {
			if workers[i].ownsFile {
				_ = workers[i].file.Close()
			}
		}
	}()
	var ready, complete sync.WaitGroup
	ready.Add(c.Concurrency)
	phase := make(chan phaseCommand)
	for i := range workers {
		go workers[i].run(phase, &ready, &complete)
	}
	ready.Wait()
	r.Runtime.Ready = snapshotMetrics()
	if c.Warmup > 0 {
		complete.Add(c.Concurrency)
		phase <- phaseCommand{until: time.Now().Add(c.Warmup), warm: true}
		for i := 1; i < c.Concurrency; i++ {
			phase <- phaseCommand{until: time.Now().Add(c.Warmup), warm: true}
		}
		complete.Wait()
	}
	for i := range workers {
		workers[i].operations = 0
		workers[i].bytes = 0
		workers[i].samples = workers[i].samples[:0]
		workers[i].endToEndSamples = workers[i].endToEndSamples[:0]
		workers[i].err = nil
	}
	// Reset the measurement snapshots after warmup. BeforeSpawn and the first
	// Ready snapshot remain in JSON so setup effects are still inspectable.
	measurementBegin := snapshotMetrics()
	r.Dataset["residency_before_measurement"] = inspectResidency(f, st.Size())
	r.Runtime.Ready = measurementBegin
	stopSampler := make(chan struct{})
	var samplerWG sync.WaitGroup
	if c.SampleInterval > 0 {
		samplerWG.Add(1)
		go samplePeaks(c.SampleInterval, stopSampler, &samplerWG, &r.Runtime.Peak)
	}
	start := time.Now()
	complete.Add(c.Concurrency)
	if c.Operations > 0 {
		for i := range workers {
			n := c.Operations / uint64(c.Concurrency)
			if uint64(i) < c.Operations%uint64(c.Concurrency) {
				n++
			}
			phase <- phaseCommand{operations: n, latency: c.Latency}
		}
	} else {
		end := start.Add(c.Duration)
		for range workers {
			phase <- phaseCommand{until: end}
		}
	}
	complete.Wait()
	elapsed := time.Since(start)
	for range workers {
		phase <- phaseCommand{stop: true}
	}
	if ring != nil {
		// A completion wakes its waiter before reap records the aggregate CQ
		// drain counter. Close waits for the completion loop, so snapshot only
		// after it has published that final drain accounting.
		if err := ring.Close(); err != nil {
			addURingResult(&r, ring, limiter.peak.Load())
			r.URing["close_error"] = err.Error()
		} else {
			addURingResult(&r, ring, limiter.peak.Load())
		}
	}
	close(stopSampler)
	samplerWG.Wait()
	r.DurationNS = elapsed.Nanoseconds()
	r.Runtime.End = snapshotMetrics()
	addResidencyAfterWorkload(&r, f, st.Size())
	r.Process.End, _ = readProcStatus(os.Getpid())
	r.Process.RusageEnd = readRusage()
	var operations, bytesRead uint64
	var serviceLat, endToEndLat []uint64
	var workErrors []string
	for i := range workers {
		operations += workers[i].operations
		bytesRead += workers[i].bytes
		if c.Latency {
			serviceLat = append(serviceLat, workers[i].samples...)
			endToEndLat = append(endToEndLat, workers[i].endToEndSamples...)
		}
		if workers[i].err != nil {
			workErrors = append(workErrors, workers[i].err.Error())
		}
	}
	r.Work["operations"] = operations
	r.Work["bytes"] = bytesRead
	r.Work["errors"] = workErrors
	r.Work["throughput_ops_per_s"] = float64(operations) / elapsed.Seconds()
	r.Work["throughput_bytes_per_s"] = float64(bytesRead) / elapsed.Seconds()
	r.Work["configured_max_outstanding"] = maxOutstanding
	r.Work["observed_peak_outstanding"] = limiter.peak.Load()
	if c.Latency {
		r.Latency["service"] = latencySummary(serviceLat)
		r.Latency["end_to_end"] = latencySummary(endToEndLat)
	}
	r.Runtime.SchedulerDelta = histogramDelta(measurementBegin.Values["/sched/latencies:seconds"], r.Runtime.End.Values["/sched/latencies:seconds"])
	if len(workErrors) != 0 {
		r.Error = "one or more workers failed"
		return r
	}
	if ring != nil {
		if err := checkURingReconciliation(r.URing); err != nil {
			r.Error = err.Error()
			return r
		}
	}
	r.Status = "ok"
	return r
}

func boundedWorkerOption(c config) *uint32 {
	if !c.URingBoundedWorkersSet || c.URingBoundedWorkers < 1 {
		return nil
	}
	v := uint32(c.URingBoundedWorkers)
	return &v
}

func unboundedWorkerOption(c config) *uint32 {
	if !c.URingUnboundedWorkersSet || c.URingUnboundedWorkers < 1 {
		return nil
	}
	v := uint32(c.URingUnboundedWorkers)
	return &v
}

func ringOptions(c config) uring.Options {
	return uring.Options{
		ForceAsync:          c.URingForceAsync,
		BoundedWorkers:      boundedWorkerOption(c),
		UnboundedWorkers:    unboundedWorkerOption(c),
		LockSubmitterThread: c.URingLockSubmitterThread,
		RecordIssuerTIDs:    c.URingRecordIssuerTIDs,
	}
}

type phaseCommand struct {
	until               time.Time
	operations          uint64
	warm, latency, stop bool
}
type worker struct {
	file              *os.File
	buf               []byte
	seed, slots       uint64
	latency           bool
	operation         string
	backend           string
	ownsFile          bool
	operations, bytes uint64
	samples           []uint64
	endToEndSamples   []uint64
	err               error
	limiter           *outstandingLimiter
	ring              *uring.Ring
}

func (w *worker) run(ch <-chan phaseCommand, ready, done *sync.WaitGroup) {
	ready.Done()
	for cmd := range ch {
		if cmd.stop {
			return
		}
		w.execute(cmd)
		done.Done()
	}
}
func (w *worker) execute(cmd phaseCommand) {
	var n uint64
	for {
		if cmd.operations > 0 {
			if n >= cmd.operations {
				return
			}
		} else if n&63 == 0 && time.Now().After(cmd.until) {
			return
		}
		off := nextOffset(&w.seed, w.slots, len(w.buf))
		var logicalStarted time.Time
		if cmd.latency {
			logicalStarted = time.Now()
		}
		w.limiter.acquire()
		// A goroutine can have waited on the semaphore past the duration.
		// Recheck after acquisition so the measured duration is not extended by
		// draining every logical client through a small permit count.
		if cmd.operations == 0 && time.Now().After(cmd.until) {
			w.limiter.release()
			return
		}
		var started time.Time
		if cmd.latency {
			started = time.Now()
		}
		var got int
		var err error
		if w.operation == "read" {
			got, err = w.file.Read(w.buf)
			if errors.Is(err, io.EOF) && got == 0 {
				if _, seekErr := w.file.Seek(0, io.SeekStart); seekErr != nil {
					w.limiter.release()
					w.err = seekErr
					return
				}
				got, err = w.file.Read(w.buf)
			}
		} else {
			if w.backend == "uring" {
				got, err, _ = w.ring.ReadAt(int(w.file.Fd()), w.buf, off)
			} else {
				got, err = w.file.ReadAt(w.buf, off)
			}
		}
		w.limiter.release()
		if err != nil && !errors.Is(err, io.EOF) {
			w.err = err
			return
		}
		if got != len(w.buf) {
			w.err = fmt.Errorf("short read: %d of %d", got, len(w.buf))
			return
		}
		if cmd.latency {
			w.samples = append(w.samples, uint64(time.Since(started)))
			w.endToEndSamples = append(w.endToEndSamples, uint64(time.Since(logicalStarted)))
		}
		w.operations++
		w.bytes += uint64(got)
		n++
	}
}

func nextOffset(seed *uint64, slots uint64, bufferBytes int) int64 {
	*seed ^= *seed >> 12
	*seed ^= *seed << 25
	*seed ^= *seed >> 27
	return int64((*seed*2685821657736338717)%slots) * int64(bufferBytes)
}

// outstandingLimiter bounds active blocking syscalls without introducing a
// second worker pool. The workload still has one logical goroutine per client.
type outstandingLimiter struct {
	sem     chan struct{}
	current atomic.Uint64
	peak    atomic.Uint64
}

func newOutstandingLimiter(limit int) *outstandingLimiter {
	return &outstandingLimiter{sem: make(chan struct{}, limit)}
}

func (l *outstandingLimiter) acquire() {
	l.sem <- struct{}{}
	n := l.current.Add(1)
	for {
		old := l.peak.Load()
		if n <= old || l.peak.CompareAndSwap(old, n) {
			return
		}
	}
}

func (l *outstandingLimiter) release() {
	l.current.Add(^uint64(0))
	<-l.sem
}

func addURingResult(r *result, ring *uring.Ring, maxLogicalOutstanding uint64) {
	s := ring.Stats()
	iowq := ring.IOWQWorkerConfig()
	setupTID, setupRegisterTIDs, submitEnterTIDs, waitEnterTIDs, tidTruncated := ring.IssuerTIDs()
	r.URing = map[string]any{
		"operations_created":                s.OperationsCreated,
		"operations_admitted":               s.OperationsAdmitted,
		"operations_queued":                 s.OperationsQueued,
		"operations_published":              s.OperationsPublished,
		"operations_cleaned_pre_publish":    s.OperationsCleanedBeforePublication,
		"pins_created":                      s.PinsCreated,
		"pins_released":                     s.PinsReleased,
		"submissions":                       s.Submissions,
		"terminal_completions":              s.Completions,
		"max_outstanding":                   maxLogicalOutstanding,
		"ring_outstanding_at_end":           ring.Outstanding(),
		"max_operation_table_size":          s.MaxOperationTable,
		"operation_table_entries_at_end":    s.OperationTable,
		"sq_full_waits":                     s.SQFullWaits,
		"io_uring_enter_calls":              s.EnterCalls,
		"submit_enter_calls":                s.SubmitEnterCalls,
		"wait_enter_calls":                  s.WaitEnterCalls,
		"submit_entries_requested":          s.SubmitEntriesRequested,
		"submit_entries_consumed":           s.SubmitEntriesConsumed,
		"submit_batches":                    s.SubmitBatches,
		"submit_batch_entries_total":        s.SubmitBatchEntriesTotal,
		"submit_batch_size_max":             s.SubmitBatchSizeMax,
		"submit_batch_size_buckets":         s.SubmitBatchSizeBuckets,
		"completion_drain_batches":          s.CompletionDrainBatches,
		"completion_cqes_drained_total":     s.CompletionCQEsDrainedTotal,
		"completion_batch_size_max":         s.CompletionBatchSizeMax,
		"completion_batch_size_buckets":     s.CompletionBatchSizeBuckets,
		"partial_submit_returns":            s.PartialSubmitReturns,
		"zero_submit_returns":               s.ZeroSubmitReturns,
		"average_submit_batch_size":         ratio(s.SubmitBatchEntriesTotal, s.SubmitBatches),
		"consumed_entries_per_submit_enter": ratio(s.SubmitEntriesConsumed, s.SubmitEnterCalls),
		"cqes_per_completion_drain":         ratio(s.CompletionCQEsDrainedTotal, s.CompletionDrainBatches),
		"total_enter_calls_per_operation":   ratio(s.EnterCalls, s.Completions),
		"submitted_entries":                 s.EnterSubmitted,
		"unknown_completion_ids":            s.UnknownIDs,
		"duplicate_cqes":                    s.DuplicateCQEs,
		"force_async":                       ring.ForceAsync(),
		"lock_submitter_thread":             ring.LockSubmitterThread(),
		"record_issuer_tids":                ring.RecordIssuerTIDs(),
		"setup_tid":                         setupTID,
		"setup_register_distinct_tids":      setupRegisterTIDs,
		"setup_register_tid_count":          len(setupRegisterTIDs),
		"submit_enter_distinct_tids":        submitEnterTIDs,
		"submit_enter_tid_count":            len(submitEnterTIDs),
		"wait_enter_distinct_tids":          waitEnterTIDs,
		"wait_enter_tid_count":              len(waitEnterTIDs),
		"issuer_tid_sets_truncated":         tidTruncated,
		"iowq_worker_config": map[string]any{
			"bounded_worker_limit_requested":             iowq.BoundedWorkerLimitRequested,
			"unbounded_worker_limit_requested":           iowq.UnboundedWorkerLimitRequested,
			"iowq_worker_limit_query_attempted":          iowq.IOWQWorkerLimitQueryAttempted,
			"iowq_worker_limit_registration_attempted":   iowq.IOWQWorkerLimitRegistrationAttempted,
			"iowq_worker_limit_registration_supported":   iowq.IOWQWorkerLimitRegistrationSupported,
			"previous_bounded_worker_max":                iowq.PreviousBoundedWorkerMax,
			"previous_unbounded_worker_max":              iowq.PreviousUnboundedWorkerMax,
			"requested_bounded_worker_max":               iowq.RequestedBoundedWorkerMax,
			"requested_unbounded_worker_max":             iowq.RequestedUnboundedWorkerMax,
			"registration_returned_bounded_worker_max":   iowq.RegistrationReturnedBoundedWorkerMax,
			"registration_returned_unbounded_worker_max": iowq.RegistrationReturnedUnboundedWorkerMax,
			"post_set_bounded_worker_max":                iowq.PostSetBoundedWorkerMax,
			"post_set_unbounded_worker_max":              iowq.PostSetUnboundedWorkerMax,
			"query_error":                                iowq.QueryError,
			"registration_error":                         iowq.RegistrationError,
			"post_set_query_error":                       iowq.PostSetQueryError,
		},
		"pins_remaining_at_end": s.PinnedOperations,
	}
}

func ratio(n, d uint64) *float64 {
	if d == 0 {
		return nil
	}
	v := float64(n) / float64(d)
	return &v
}

func checkURingReconciliation(values map[string]any) error {
	if values == nil {
		return errors.New("missing io_uring metrics")
	}
	u := func(name string) uint64 { v, _ := values[name].(uint64); return v }
	if u("operations_created") != u("operations_admitted")+u("operations_cleaned_pre_publish") ||
		u("operations_admitted") != u("operations_queued") ||
		u("operations_published") != u("submissions") ||
		u("submissions") != u("terminal_completions") ||
		u("operations_published") != u("submit_entries_consumed") ||
		u("submit_entries_consumed") > u("submit_entries_requested") ||
		u("completion_cqes_drained_total") != u("terminal_completions") ||
		u("pins_created") != u("pins_released") ||
		u("ring_outstanding_at_end") != 0 ||
		u("operation_table_entries_at_end") != 0 ||
		u("pins_remaining_at_end") != 0 ||
		u("unknown_completion_ids") != 0 ||
		u("duplicate_cqes") != 0 {
		return fmt.Errorf("io_uring reconciliation failed: %+v", values)
	}
	if closeErr, ok := values["close_error"].(string); ok && closeErr != "" {
		return fmt.Errorf("io_uring close failed: %s", closeErr)
	}
	return nil
}

func baseResult(c config, size int64, slots uint64) result {
	r := result{SchemaVersion: schemaVersion, Status: "failed", StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano), Limitations: []string{"No per-I/O allocation in this harness; latency timestamps are disabled unless -latency is set.", "Residency is a point-in-time mincore observation, not a promise about every ReadAt.", "Internal runtime-metric peaks are sampled and therefore lower bounds, not mathematical maxima."}}
	operation := "readat_random"
	if c.Operation == "read" {
		operation = "read_sequential"
	}
	r.Config = map[string]any{"backend": c.Backend, "operation": operation, "file_access": c.FileAccess, "buffer_bytes": c.BufferBytes, "concurrency": c.Concurrency, "max_outstanding": c.MaxOutstanding, "gomaxprocs": c.GOMAXPROCS, "warmup_ns": c.Warmup.Nanoseconds(), "duration_target_ns": c.Duration.Nanoseconds(), "fixed_operations": c.Operations, "offset_seed": c.Seed, "latency_mode": c.Latency, "uring_force_async": c.URingForceAsync, "uring_lock_submitter_thread": c.URingLockSubmitterThread, "uring_bounded_workers": c.URingBoundedWorkers, "uring_bounded_workers_set": c.URingBoundedWorkersSet, "uring_unbounded_workers": c.URingUnboundedWorkers, "uring_unbounded_workers_set": c.URingUnboundedWorkersSet, "runtime_sample_interval_ns": c.SampleInterval.Nanoseconds()}
	r.Environment = map[string]any{"go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "kernel_release": kernelRelease(), "online_cpus": runtime.NumCPU(), "harness_revision": c.HarnessRevision}
	r.Config["uring_record_issuer_tids"] = c.URingRecordIssuerTIDs
	r.Dataset = map[string]any{"path": c.File, "size_bytes": size, "read_slots": slots, "cache_state_declared": c.CacheState, "cache_precondition": c.CachePrecondition, "cache_residency_verified": false}
	r.Work = map[string]any{"operations": uint64(0), "bytes": uint64(0), "errors": []string{}, "short_reads": uint64(0)}
	r.Latency = map[string]any{"enabled": c.Latency, "service_definition": "semaphore acquired -> ReadAt terminal", "end_to_end_definition": "logical request begins -> ReadAt terminal", "service": latencySummary(nil), "end_to_end": latencySummary(nil)}
	r.Runtime.Peak = map[string]uint64{}
	return r
}

func failed(c config, err error) result { r := baseResult(c, 0, 0); r.Error = err.Error(); return r }
func emit(r result)                     { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); _ = e.Encode(r) }

func snapshotMetrics() metricsSnapshot {
	all := map[string]metrics.Description{}
	for _, d := range metrics.All() {
		all[d.Name] = d
	}
	samples := make([]metrics.Sample, 0, len(metricNames))
	names := make([]string, 0, len(metricNames))
	out := metricsSnapshot{Values: map[string]metricValue{}}
	for _, n := range metricNames {
		if _, ok := all[n]; ok {
			samples = append(samples, metrics.Sample{Name: n})
			names = append(names, n)
		} else {
			out.Values[n] = metricValue{}
		}
	}
	metrics.Read(samples)
	for i, n := range names {
		v := samples[i].Value
		mv := metricValue{Available: true}
		switch v.Kind() {
		case metrics.KindUint64:
			x := v.Uint64()
			mv.Kind = "uint64"
			mv.Uint64 = &x
		case metrics.KindFloat64:
			x := v.Float64()
			mv.Kind = "float64"
			mv.Float64 = &x
		case metrics.KindFloat64Histogram:
			h := v.Float64Histogram()
			bs := make([]string, len(h.Buckets))
			for j, b := range h.Buckets {
				bs[j] = strconv.FormatFloat(b, 'g', -1, 64)
			}
			mv.Kind = "histogram"
			mv.Histogram = &histogram{Buckets: bs, Counts: append([]uint64(nil), h.Counts...)}
		}
		out.Values[n] = mv
	}
	return out
}

func samplePeaks(interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup, peak *map[string]uint64) {
	defer wg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s := snapshotMetrics()
			for _, n := range []string{"/sched/threads/total:threads", "/sched/goroutines/not-in-go:goroutines", "/sched/goroutines/runnable:goroutines"} {
				if v := s.Values[n].Uint64; v != nil && *v > (*peak)[n] {
					(*peak)[n] = *v
				}
			}
		}
	}
}
func histogramDelta(a, b metricValue) *histogram {
	if a.Histogram == nil || b.Histogram == nil || len(a.Histogram.Counts) != len(b.Histogram.Counts) {
		return nil
	}
	h := &histogram{Buckets: append([]string(nil), b.Histogram.Buckets...), Counts: make([]uint64, len(b.Histogram.Counts))}
	for i := range h.Counts {
		h.Counts[i] = b.Histogram.Counts[i] - a.Histogram.Counts[i]
	}
	return h
}
func latencySummary(values []uint64) map[string]any {
	out := map[string]any{"samples": uint64(0), "p50_ns": nil, "p95_ns": nil, "p99_ns": nil, "max_ns": nil}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	out["samples"] = uint64(len(values))
	if len(values) == 0 {
		return out
	}
	at := func(p float64) uint64 { return values[int(math.Ceil(p*float64(len(values))))-1] }
	out["p50_ns"] = at(.50)
	out["p95_ns"] = at(.95)
	out["p99_ns"] = at(.99)
	out["max_ns"] = values[len(values)-1]
	return out
}
func readProcStatus(pid int) (procStatus, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return procStatus{}, err
	}
	var s procStatus
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch strings.TrimSuffix(f[0], ":") {
		case "Threads":
			s.Threads = n
		case "VmRSS":
			s.VmRSSKiB = n
		case "voluntary_ctxt_switches":
			s.VoluntaryCS = n
		case "nonvoluntary_ctxt_switches":
			s.InvoluntaryCS = n
		}
	}
	return s, nil
}
func readRusage() rusage {
	var u syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return rusage{UserNS: u.Utime.Sec*1e9 + u.Utime.Usec*1e3, SystemNS: u.Stime.Sec*1e9 + u.Stime.Usec*1e3, VoluntaryCS: u.Nvcsw, InvoluntaryCS: u.Nivcsw, MaxRSSKiB: u.Maxrss}
}
func kernelRelease() string {
	var u syscall.Utsname
	if syscall.Uname(&u) != nil {
		return "unknown"
	}
	b := make([]byte, 0, len(u.Release))
	for _, x := range u.Release {
		if x == 0 {
			break
		}
		b = append(b, byte(x))
	}
	return string(b)
}
