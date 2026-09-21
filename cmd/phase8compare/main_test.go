package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareSyntheticArtifacts(t *testing.T) {
	root := t.TempDir()
	b, c := filepath.Join(root, "b"), filepath.Join(root, "c")
	if err := os.MkdirAll(b, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c, 0755); err != nil {
		t.Fatal(err)
	}
	phase7 := "mode,MiB_s_median,effective_cores_median,peak_tasks_median\nblocking,10,4,20\nnormal,7,1,8\nU1,8,1,10\nU2,9,2,12\nU4,10,3,14\nU8,10,3,18\nU16,10,3,26\ndefault,10,3,100\n"
	resident := "row_type,mode,MiB_s_median,effective_cores_median,peak_process_tasks_median\nsummary,blocking,11,4,21\nsummary,normal,7,1,7\nsummary,U1,8,1,9\nsummary,U2,9,2,11\nsummary,U4,10,3,15\nsummary,U8,10,3,20\nsummary,U16,10,3,30\nsummary,default,10,3,110\n"
	slow := "row_type,mode,peak_process_tasks_median\nsummary,blocking,100\nsummary,normal,8\n"
	if err := os.WriteFile(filepath.Join(b, "curve.csv"), []byte(phase7), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c, "resident-curve.csv"), []byte(resident), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c, "slow-thread-pressure.csv"), []byte(slow), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := compare(b, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"normal uring bounded task population", "yes: lower normal task peak", "U16=10 MiB/s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}
