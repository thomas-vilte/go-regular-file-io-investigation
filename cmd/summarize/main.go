// summarize prints a stable TSV view of individual iobaseline JSON results.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	dir := flag.String("dir", "results", "result directory")
	flag.Parse()
	files, err := filepath.Glob(filepath.Join(*dir, "*.json"))
	if err != nil {
		panic(err)
	}
	sort.Strings(files)
	fmt.Println("file\tstatus\tbuffer\tconcurrency\tgomaxprocs\tops_s\tMiB_s\toperations\tp50_ns\tp95_ns\tp99_ns\tmax_ns\tgo_threads_peak\tnot_in_go_peak\trunnable_peak\tprocess_threads_peak\tprocess_rss_peak_kib\tproc_vol_cs_delta\tproc_invol_cs_delta\tgc_cycles_delta")
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			panic(err)
		}
		var x map[string]any
		if err := json.Unmarshal(b, &x); err != nil {
			panic(err)
		}
		cfg := obj(x, "config")
		work := obj(x, "work")
		lat := obj(x, "latency")
		rt := obj(x, "runtime")
		proc := obj(x, "process")
		obs := obj(proc, "external_observer")
		peak := obj(rt, "peak_observed_by_internal_sampler")
		rb := obj(proc, "rusage_before")
		re := obj(proc, "rusage_end")
		before := obj(obj(rt, "ready"), "values")
		end := obj(obj(rt, "end"), "values")
		fmt.Printf("%s\t%v\t%.0f\t%.0f\t%.0f\t%.3f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\n", filepath.Base(p), x["status"], num(cfg["buffer_bytes"]), num(cfg["concurrency"]), num(cfg["gomaxprocs"]), num(work["throughput_ops_per_s"]), num(work["throughput_bytes_per_s"])/(1024*1024), num(work["operations"]), num(lat["p50_ns"]), num(lat["p95_ns"]), num(lat["p99_ns"]), num(lat["max_ns"]), num(peak["/sched/threads/total:threads"]), num(peak["/sched/goroutines/not-in-go:goroutines"]), num(peak["/sched/goroutines/runnable:goroutines"]), num(obs["max_observed_threads"]), num(obs["max_observed_rss_kib"]), num(re["VoluntaryCS"])-num(rb["VoluntaryCS"]), num(re["InvoluntaryCS"])-num(rb["InvoluntaryCS"]), metricUint(end, "/gc/cycles/total:gc-cycles")-metricUint(before, "/gc/cycles/total:gc-cycles"))
	}
}
func obj(v map[string]any, k string) map[string]any {
	if x, ok := v[k].(map[string]any); ok {
		return x
	}
	return map[string]any{}
}
func num(v any) float64 {
	if x, ok := v.(float64); ok {
		return x
	}
	return 0
}
func metricUint(v map[string]any, n string) float64 { return num(obj(v, n)["uint64"]) }
