// iouringprobe records whether this external experiment can create its minimal
// io_uring instance. It performs no I/O and makes no runtime changes.
package main

import (
	"encoding/json"
	"flag"
	"os"
	"runtime"
	"strings"

	"example.com/go-iouring-investigation/bench/internal/uring"
)

func main() {
	entries := flag.Uint("entries", 1024, "requested ring entries")
	revision := flag.String("harness-revision", "unknown", "harness source revision")
	flag.Parse()
	out := map[string]any{
		"operation":         "io_uring_setup_probe",
		"entries":           *entries,
		"go_version":        runtime.Version(),
		"goos":              runtime.GOOS,
		"goarch":            runtime.GOARCH,
		"harness_revision":  *revision,
		"io_uring_disabled": readTrim("/proc/sys/kernel/io_uring_disabled"),
		"seccomp_status":    seccompStatus(),
	}
	r, err := uring.New(uint32(*entries))
	if err != nil {
		out["status"] = "unavailable"
		out["error"] = err.Error()
	} else {
		out["status"] = "available"
		out["close_error"] = errorString(r.Close())
	}
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	_ = e.Encode(out)
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(b))
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func seccompStatus() map[string]string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "Seccomp:") || strings.HasPrefix(l, "Seccomp_filters:") || strings.HasPrefix(l, "NoNewPrivs:") {
			p := strings.Fields(l)
			if len(p) == 2 {
				out[strings.TrimSuffix(p[0], ":")] = p[1]
			}
		}
	}
	return out
}
