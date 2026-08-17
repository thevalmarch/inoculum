package presentation

import (
	"testing"
	"time"
)

func TestCompactDurationUsesCalmWholeSeconds(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{name: "zero", duration: 0, want: "0s"},
		{name: "sub-second", duration: 750 * time.Millisecond, want: "0s"},
		{name: "one second", duration: time.Second, want: "1s"},
		{name: "two point four", duration: 2400 * time.Millisecond, want: "2s"},
		{name: "two point five", duration: 2500 * time.Millisecond, want: "2s"},
		{name: "two point nine", duration: 2900 * time.Millisecond, want: "2s"},
		{name: "fifty-nine point nine", duration: 59900 * time.Millisecond, want: "59s"},
		{name: "one minute", duration: time.Minute, want: "1m"},
		{name: "one minute one second", duration: time.Minute + time.Second, want: "1m1s"},
		{name: "one minute thirty seconds", duration: time.Minute + 30*time.Second, want: "1m30s"},
		{name: "two minutes five seconds", duration: 2*time.Minute + 5*time.Second, want: "2m5s"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CompactDuration(test.duration); got != test.want {
				t.Fatalf("CompactDuration(%s) = %q, want %q", test.duration, got, test.want)
			}
		})
	}
}
