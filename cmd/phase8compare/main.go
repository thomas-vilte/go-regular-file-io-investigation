// phase8compare compares mechanism observations from a Phase 7 Environment B
// artifact directory and a Phase 8 second-host artifact directory. It reports
// raw medians and only applies simple directional checks where the mechanism
// statement itself is directional; plateau claims remain for human review.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type table map[string]map[string]string

func main() {
	hostB := flag.String("host-b", "", "Environment B Phase 7 artifact directory")
	hostC := flag.String("host-c", "", "Environment C Phase 8 artifact directory")
	output := flag.String("output", "", "optional output file; stdout when empty")
	flag.Parse()
	if *hostB == "" || *hostC == "" {
		fatal("-host-b and -host-c are required")
	}
	text, err := compare(*hostB, *hostC)
	if err != nil {
		fatal(err.Error())
	}
	if *output == "" {
		fmt.Print(text)
		return
	}
	if err := os.WriteFile(*output, []byte(text), 0644); err != nil {
		fatal(err.Error())
	}
}

func compare(hostB, hostC string) (string, error) {
	b, err := readTable(filepath.Join(hostB, "curve.csv"), "mode")
	if err != nil {
		return "", fmt.Errorf("Environment B curve.csv: %w", err)
	}
	c, err := readTable(filepath.Join(hostC, "resident-curve.csv"), "mode")
	if err != nil {
		return "", fmt.Errorf("Environment C resident-curve.csv: %w", err)
	}
	slow, err := readTable(filepath.Join(hostC, "slow-thread-pressure.csv"), "mode")
	if err != nil {
		return "", fmt.Errorf("Environment C slow-thread-pressure.csv: %w", err)
	}

	rows := [][]string{
		{"mechanism", "host B observation", "host C observation", "reproduced"},
		{
			"slow blocking thread pressure",
			"not captured by the supplied Phase 7 resident artifact",
			pair(slow, "blocking", "normal", "peak_process_tasks_median", "tasks"),
			"unclear: Phase 7 does not contain its slow-workload control",
		},
		{
			"normal uring bounded task population",
			pair(b, "blocking", "normal", "peak_tasks_median", "tasks"),
			pair(c, "blocking", "normal", "peak_process_tasks_median", "tasks"),
			directional(c, "blocking", "normal", "peak_process_tasks_median", "lower normal task peak"),
		},
		{
			"hot normal-uring limited CPU parallelism",
			pair(b, "blocking", "normal", "effective_cores_median", "effective cores"),
			pair(c, "blocking", "normal", "effective_cores_median", "effective cores"),
			directional(c, "blocking", "normal", "effective_cores_median", "lower normal CPU"),
		},
		{
			"force-async increases CPU parallelism",
			pair(b, "normal", "default", "effective_cores_median", "effective cores"),
			pair(c, "normal", "default", "effective_cores_median", "effective cores"),
			directional(c, "normal", "default", "effective_cores_median", "higher forced-async CPU"),
		},
		{
			"stable-issuer worker curve reaches plateau",
			curve(b, "MiB_s_median", "MiB/s"),
			curve(c, "MiB_s_median", "MiB/s"),
			"unclear: no plateau threshold is encoded",
		},
		{
			"tasks continue growing after throughput plateau",
			curve(b, "peak_tasks_median", "tasks"),
			curve(c, "peak_process_tasks_median", "tasks"),
			"unclear: interpret task and throughput curves together",
		},
	}
	var out strings.Builder
	for index, row := range rows {
		out.WriteString("| " + strings.Join(row, " | ") + " |\n")
		if index == 0 {
			out.WriteString("|---|---|---|---|\n")
		}
	}
	return out.String(), nil
}

func readTable(path, key string) (table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	for i, name := range header {
		index[name] = i
	}
	keyIndex, ok := index[key]
	if !ok {
		return nil, fmt.Errorf("missing %q column", key)
	}
	out := table{}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(row) != len(header) || row[keyIndex] == "" {
			continue
		}
		m := map[string]string{}
		for i, name := range header {
			m[name] = row[i]
		}
		// Phase 8 slow-thread-pressure.csv contains run and summary rows.
		if kind, exists := m["row_type"]; exists && kind != "summary" && filepath.Base(path) == "slow-thread-pressure.csv" {
			continue
		}
		out[row[keyIndex]] = m
	}
	return out, nil
}

func pair(t table, first, second, metric, unit string) string {
	return fmt.Sprintf("%s=%s %s; %s=%s %s", first, metricValue(t[first], metric), unit, second, metricValue(t[second], metric), unit)
}

func curve(t table, metric, unit string) string {
	parts := []string{}
	for _, mode := range []string{"U1", "U2", "U4", "U8", "U16", "default"} {
		if row, ok := t[mode]; ok {
			parts = append(parts, mode+"="+metricValue(row, metric)+" "+unit)
		}
	}
	if len(parts) == 0 {
		return "unavailable"
	}
	return strings.Join(parts, "; ")
}

func metricValue(row map[string]string, metric string) string {
	if row == nil || row[metric] == "" {
		return "N/A"
	}
	return row[metric]
}

func directional(t table, reference, observed, metric, meaning string) string {
	a, aerr := strconv.ParseFloat(metricValue(t[reference], metric), 64)
	b, berr := strconv.ParseFloat(metricValue(t[observed], metric), 64)
	if aerr != nil || berr != nil {
		return "unclear: missing metric"
	}
	if strings.HasPrefix(meaning, "lower") {
		if b < a {
			return "yes: " + meaning
		}
		return "no: " + meaning + " not observed"
	}
	if b > a {
		return "yes: " + meaning
	}
	return "no: " + meaning + " not observed"
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "phase8compare:", message)
	os.Exit(1)
}
