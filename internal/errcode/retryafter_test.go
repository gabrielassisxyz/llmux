package errcode

import (
	"testing"
	"time"
)

func TestCalculateRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		reopen time.Time
		want   int
	}{
		{"zero reopen", time.Time{}, 1},
		{"past reopen", now.Add(-1 * time.Second), 1},
		{"fractional round up", now.Add(500 * time.Millisecond), 1},
		{"fractional over 1s", now.Add(1200 * time.Millisecond), 2},
		{"exact 2s", now.Add(2 * time.Second), 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateRetryAfter(tt.reopen, now)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
