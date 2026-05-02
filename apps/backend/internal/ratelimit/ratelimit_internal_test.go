package ratelimit

import (
	"testing"
	"time"
)

// White-box tests for the internal helpers that don't need a Redis trip to
// verify. Live in `package ratelimit` (not `_test`) so they can see the
// unexported windowMillisCeil.

func TestWindowMillisCeil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   time.Duration
		want int64
	}{
		{"exact 1ms", 1 * time.Millisecond, 1},
		{"500us rounds up to 1ms", 500 * time.Microsecond, 1},
		{"1ns rounds up to 1ms", 1 * time.Nanosecond, 1},
		{"5ms exact", 5 * time.Millisecond, 5},
		{"5ms + 1ns rounds up to 6", 5*time.Millisecond + time.Nanosecond, 6},
		{"1s", time.Second, 1000},
		{"1min", time.Minute, 60_000},
		{"zero stays zero", 0, 0},
		{"negative stays zero (caller-validated; defensive)", -time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := windowMillisCeil(tc.in)
			if got != tc.want {
				t.Fatalf("windowMillisCeil(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
