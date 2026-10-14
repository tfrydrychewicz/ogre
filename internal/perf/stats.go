package perf

import (
	"fmt"
	"slices"
	"time"
)

// summary is the distribution of a set of durations.
type summary struct {
	n                  int
	p50, p95, p99, max time.Duration
}

// summarize returns the percentiles of d by the nearest-rank method: the
// 95th percentile is the smallest value that at least 95% of the values do
// not exceed. It never interpolates, so every figure reported is a time a
// request really took. d is not modified.
func summarize(d []time.Duration) summary {
	if len(d) == 0 {
		return summary{}
	}
	s := slices.Clone(d)
	slices.Sort(s)
	return summary{n: len(s), p50: percentile(s, 50), p95: percentile(s, 95), p99: percentile(s, 99), max: s[len(s)-1]}
}

// percentile is the nearest-rank percentile p of sorted, which is not empty.
func percentile(sorted []time.Duration, p int) time.Duration {
	rank := (p*len(sorted) + 99) / 100 // ceil(p/100 * n)
	return sorted[max(rank, 1)-1]
}

// ms formats d in milliseconds with a precision that suits its size.
func ms(d time.Duration) string {
	v := float64(d) / float64(time.Millisecond)
	switch {
	case v < 1:
		return fmt.Sprintf("%.3f", v)
	case v < 100:
		return fmt.Sprintf("%.2f", v)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// mib formats a byte count in MiB.
func mib(b int64) string {
	return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
}
