package perf

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// errNoProcessStats means this system offers no way the run knows to read
// another process's memory or CPU time. The run goes on without those rows.
var errNoProcessStats = errors.New("process statistics are not available on " + runtime.GOOS)

// residentBytes returns the resident set size of process pid: the memory it
// occupies in RAM, which is what an instance's 512 MB (NFR-5) limits.
func residentBytes(pid int) (int64, error) {
	if runtime.GOOS == "linux" {
		return procStatusKB(pid, "VmRSS:")
	}
	if runtime.GOOS == "windows" {
		return 0, errNoProcessStats
	}
	// macOS and the BSDs: ps reports RSS in KiB.
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("ps -o rss= -p %d: %w", pid, err)
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ps -o rss=: unexpected output %q", out)
	}
	return kb * 1024, nil
}

func procStatusKB(pid int, field string) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, field); ok {
			kb, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("/proc/%d/status: %s %q", pid, field, rest)
			}
			return kb * 1024, nil
		}
	}
	return 0, fmt.Errorf("/proc/%d/status has no %s", pid, field)
}

// cpuTime returns the CPU time, user and system, that process pid has used.
// Its growth over a phase divided by the phase's length is the share of one
// core the server needed.
func cpuTime(pid int) (time.Duration, error) {
	switch runtime.GOOS {
	case "linux":
		// ps on Linux reports whole seconds only; /proc has clock ticks.
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return 0, err
		}
		// The command name, in parentheses, may hold spaces: count fields
		// after its closing parenthesis. utime and stime are fields 14 and
		// 15 of the line, so 12 and 13 after it.
		s := string(data)
		fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(fields) < 13 {
			return 0, fmt.Errorf("/proc/%d/stat: too few fields", pid)
		}
		utime, err1 := strconv.ParseInt(fields[11], 10, 64)
		stime, err2 := strconv.ParseInt(fields[12], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("/proc/%d/stat: unreadable times", pid)
		}
		// USER_HZ is 100 on every Linux the run is likely to meet.
		return time.Duration(utime+stime) * 10 * time.Millisecond, nil
	case "windows":
		return 0, errNoProcessStats
	}
	out, err := exec.Command("ps", "-o", "time=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("ps -o time= -p %d: %w", pid, err)
	}
	return parsePSTime(strings.TrimSpace(string(out)))
}

// parsePSTime reads ps's cumulative CPU time, "[[dd-]hh:]mm:ss[.ss]":
// "0:01.23" on macOS, "01:02:03" or "1-01:02:03" elsewhere.
func parsePSTime(s string) (time.Duration, error) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("ps time %q", s)
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("ps time %q", s)
	}
	secs, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, fmt.Errorf("ps time %q", s)
	}
	total := time.Duration(secs * float64(time.Second))
	unit := time.Minute
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("ps time %q", s)
		}
		total += time.Duration(n) * unit
		unit *= 60
	}
	return total + time.Duration(days)*24*time.Hour, nil
}
