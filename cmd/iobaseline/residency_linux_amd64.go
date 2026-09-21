package main

// This file deliberately uses the kernel ABI directly rather than a wrapper:
// the harness is Linux/amd64-only and this is inspection, not an I/O backend.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const posixFadvDontneed = 4

type residency struct {
	Available     bool   `json:"available"`
	Label         string `json:"label"`
	PageSize      int    `json:"page_size"`
	Pages         int64  `json:"pages"`
	ResidentPages int64  `json:"resident_pages"`
	ResidentPPM   int64  `json:"resident_ppm"`
	Error         string `json:"error,omitempty"`
}

func applyCachePrecondition(r *result, f *os.File, size int64, c config) {
	r.Dataset["residency_before"] = inspectResidency(f, size)
	switch c.CachePrecondition {
	case "prewarm":
		r.Dataset["prewarm"] = prewarm(f, size)
		r.Dataset["residency_after_prewarm"] = inspectResidency(f, size)
	case "dontneed":
		if err := requireExperimentOwned(c.File); err != nil {
			r.Dataset["fadvise_dontneed"] = map[string]any{"requested": false, "error": err.Error()}
			return
		}
		err := fadviseDontneed(f, size)
		r.Dataset["fadvise_dontneed"] = map[string]any{"requested": true, "error": errorString(err)}
		r.Dataset["residency_after_dontneed"] = inspectResidency(f, size)
	}
}

func addResidencyAfterWorkload(r *result, f *os.File, size int64) {
	res := inspectResidency(f, size)
	r.Dataset["residency_after_workload"] = res
	r.Dataset["cache_residency_verified"] = res.Available
}

func inspectResidency(f *os.File, size int64) residency {
	page := os.Getpagesize()
	if size <= 0 {
		return residency{Label: "residency-unverified", Error: "empty file"}
	}
	pages := (size + int64(page) - 1) / int64(page)
	if pages > int64(^uint(0)>>1) {
		return residency{Label: "residency-unverified", Error: "file too large for mincore vector"}
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_NONE, syscall.MAP_SHARED)
	if err != nil {
		return residency{Label: "residency-unverified", Error: err.Error()}
	}
	defer syscall.Munmap(data)
	vec := make([]byte, int(pages))
	_, _, errno := syscall.Syscall(syscall.SYS_MINCORE,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&vec[0])))
	if errno != 0 {
		return residency{Label: "residency-unverified", Error: errno.Error()}
	}
	var resident int64
	for _, b := range vec {
		if b&1 != 0 {
			resident++
		}
	}
	ppm := resident * 1_000_000 / pages
	label := "residency-mixed"
	if ppm >= 950_000 {
		label = "residency-verified-mostly-resident"
	} else if ppm <= 50_000 {
		label = "residency-verified-mostly-nonresident"
	}
	return residency{Available: true, Label: label, PageSize: page, Pages: pages, ResidentPages: resident, ResidentPPM: ppm}
}

func prewarm(f *os.File, size int64) map[string]any {
	buf := make([]byte, 1<<20)
	var read int64
	for off := int64(0); off < size; {
		n := len(buf)
		if remaining := size - off; int64(n) > remaining {
			n = int(remaining)
		}
		got, err := f.ReadAt(buf[:n], off)
		read += int64(got)
		off += int64(got)
		if err != nil || got != n {
			return map[string]any{"attempted": true, "bytes_read": read, "error": fmt.Sprintf("%v (got %d expected %d)", err, got, n)}
		}
	}
	return map[string]any{"attempted": true, "bytes_read": read}
}

func fadviseDontneed(f *os.File, size int64) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_FADVISE64, f.Fd(), 0, uintptr(size), posixFadvDontneed, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func requireExperimentOwned(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// The harness is run from the bench project root; data/ is intentionally
	// ignored by Git and reserved for generated experiment inputs.
	root, err := filepath.Abs("data")
	if err != nil {
		return err
	}
	prefix := root + string(os.PathSeparator)
	if !strings.HasPrefix(abs, prefix) {
		return fmt.Errorf("refusing DONTNEED outside experiment-owned directory %s", root)
	}
	return nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
