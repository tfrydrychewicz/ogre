package perf

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Machine describes where a run ran, for docs/perf.md: numbers mean little
// without it. Every field is best effort; "unknown" when it cannot be read.
type Machine struct {
	Date    string
	OS      string // GOOS/GOARCH and the release
	CPU     string
	Cores   int
	Memory  string
	Go      string
	Commit  string
	Battery string // macOS: whether it ran on battery, which slows CPUs down
}

func describeMachine() Machine {
	m := Machine{
		Date:  time.Now().Format("2006-01-02 15:04 MST"),
		OS:    runtime.GOOS + "/" + runtime.GOARCH,
		Cores: runtime.NumCPU(),
		Go:    runtime.Version(),
		CPU:   "unknown", Memory: "unknown", Commit: "unknown", Battery: "",
	}
	run := func(name string, args ...string) string {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	switch runtime.GOOS {
	case "darwin":
		if v := run("sw_vers", "-productVersion"); v != "" {
			m.OS += ", macOS " + v
		}
		if v := run("sysctl", "-n", "machdep.cpu.brand_string"); v != "" {
			m.CPU = v
		}
		if v, err := strconv.ParseInt(run("sysctl", "-n", "hw.memsize"), 10, 64); err == nil {
			m.Memory = strconv.FormatInt(v>>30, 10) + " GiB"
		}
		if v := run("pmset", "-g", "batt"); v != "" {
			first, _, _ := strings.Cut(v, "\n")
			m.Battery = strings.Trim(strings.TrimPrefix(first, "Now drawing from "), "'")
		}
	case "linux":
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
					m.CPU = strings.TrimSpace(v)
					break
				}
			}
		}
		if kb, err := procMemTotalKB(); err == nil {
			m.Memory = strconv.FormatInt(kb>>20, 10) + " GiB"
		}
		if v := run("uname", "-r"); v != "" {
			m.OS += ", Linux " + v
		}
	}
	if v := run("git", "describe", "--always", "--dirty"); v != "" {
		m.Commit = v
	}
	return m
}

func procMemTotalKB() (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			return strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
		}
	}
	return 0, os.ErrNotExist
}
