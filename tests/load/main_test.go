package main

import (
	"testing"
	"time"
)

func TestPercentileUsesNearestRankWithinSortedSamples(t *testing.T) {
	values := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond, 40 * time.Millisecond}
	if got := percentile(values, 0.50); got != 20*time.Millisecond {
		t.Fatalf("p50 = %s, want 20ms", got)
	}
	if got := percentile(values, 0.95); got != 40*time.Millisecond {
		t.Fatalf("p95 = %s, want 40ms", got)
	}
}
