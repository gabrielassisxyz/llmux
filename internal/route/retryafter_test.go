package route

import (
	"testing"
	"time"
)

func TestParseRetryAfterDeltaSeconds(t *testing.T) {
	got := parseRetryAfter("120", time.Time{}, false)
	if got.gate != 120*time.Second {
		t.Errorf("gate = %v, want 120s", got.gate)
	}
	if !got.storedPresent || got.stored != 120*time.Second {
		t.Errorf("stored = %v, present = %v, want 120s, present", got.stored, got.storedPresent)
	}
}

func TestParseRetryAfterAbsent(t *testing.T) {
	got := parseRetryAfter("", time.Time{}, false)
	if got.gate != time.Second {
		t.Errorf("gate = %v, want 1s", got.gate)
	}
	if got.storedPresent {
		t.Errorf("storedPresent = true, want false for an absent header")
	}
}

func TestParseRetryAfterUnparseable(t *testing.T) {
	got := parseRetryAfter("not-a-real-value", time.Time{}, false)
	if got.gate != time.Second {
		t.Errorf("gate = %v, want 1s", got.gate)
	}
	if got.storedPresent {
		t.Errorf("storedPresent = true, want false for an unparseable header")
	}
}

func TestParseRetryAfterDeltaSecondsZeroStoresZeroGatesOneSecond(t *testing.T) {
	got := parseRetryAfter("0", time.Time{}, false)
	if got.gate != time.Second {
		t.Errorf("gate = %v, want 1s", got.gate)
	}
	if !got.storedPresent || got.stored != 0 {
		t.Errorf("stored = %v, present = %v, want 0, present (upstream said retry now)", got.stored, got.storedPresent)
	}
}

func TestParseRetryAfterAbsoluteFormDerivedAgainstDate(t *testing.T) {
	responseDate := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	target := responseDate.Add(90 * time.Second)
	header := target.Format(httpDateLayout)

	got := parseRetryAfter(header, responseDate, true)
	if got.gate != 90*time.Second {
		t.Errorf("gate = %v, want 90s", got.gate)
	}
	if !got.storedPresent || got.stored != 90*time.Second {
		t.Errorf("stored = %v, present = %v, want 90s, present", got.stored, got.storedPresent)
	}
}

func TestParseRetryAfterAbsoluteFormWithNoUsableDateStoresNothing(t *testing.T) {
	future := time.Date(2026, time.August, 13, 13, 0, 0, 0, time.UTC)
	header := future.Format(httpDateLayout)

	got := parseRetryAfter(header, time.Time{}, false)
	if got.gate != time.Second {
		t.Errorf("gate = %v, want 1s", got.gate)
	}
	if got.storedPresent {
		t.Errorf("storedPresent = true, want false: no Date header means nothing was derived")
	}
}

func TestParseRetryAfterAbsoluteFormAtOrBeforeDateStoresZero(t *testing.T) {
	responseDate := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	header := responseDate.Format(httpDateLayout)

	got := parseRetryAfter(header, responseDate, true)
	if got.gate != time.Second {
		t.Errorf("gate = %v, want 1s", got.gate)
	}
	if !got.storedPresent || got.stored != 0 {
		t.Errorf("stored = %v, present = %v, want 0, present (upstream's date is not after Date)", got.stored, got.storedPresent)
	}
}

func TestParseRetryAfterAbsoluteFormUnparseableDate(t *testing.T) {
	got := parseRetryAfter("not a valid http-date", time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC), true)
	if got.gate != time.Second {
		t.Errorf("gate = %v, want 1s", got.gate)
	}
	if got.storedPresent {
		t.Errorf("storedPresent = true, want false for an unparseable date")
	}
}

// TestParseRetryAfterIsImmuneToLocalWallClock proves the absolute form is
// derived only against the response's own Date, never the local wall
// clock: stepping what "now" would be locally must not change the result,
// since parseRetryAfter never reads the local clock at all.
func TestParseRetryAfterIsImmuneToLocalWallClock(t *testing.T) {
	responseDate := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	target := responseDate.Add(45 * time.Second)
	header := target.Format(httpDateLayout)

	got := parseRetryAfter(header, responseDate, true)
	if got.gate != 45*time.Second {
		t.Errorf("gate = %v, want 45s regardless of how far responseDate is from wall-clock now", got.gate)
	}
}

func TestDeriveRetryAfterDeltaSeconds(t *testing.T) {
	got := DeriveRetryAfter("30", time.Time{}, false)
	if !got.Present || got.Delay != 30*time.Second {
		t.Errorf("Delay = %v, Present = %v, want 30s, present", got.Delay, got.Present)
	}
}

func TestDeriveRetryAfterAbsentReportsNothingToHonor(t *testing.T) {
	got := DeriveRetryAfter("", time.Time{}, false)
	if got.Present {
		t.Errorf("Present = true for an absent header, want false: a 5xx has no fallback delay to impose")
	}
}

func TestDeriveRetryAfterUnparseableReportsNothingToHonor(t *testing.T) {
	got := DeriveRetryAfter("garbage", time.Time{}, false)
	if got.Present {
		t.Errorf("Present = true for an unparseable header, want false")
	}
}

func TestDeriveRetryAfterZeroDeltaReportsPresentZero(t *testing.T) {
	got := DeriveRetryAfter("0", time.Time{}, false)
	if !got.Present || got.Delay != 0 {
		t.Errorf("Delay = %v, Present = %v, want 0, present (upstream said retry now)", got.Delay, got.Present)
	}
}

func TestDeriveRetryAfterAbsoluteFormDerivedAgainstDate(t *testing.T) {
	responseDate := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	target := responseDate.Add(200 * time.Second)
	header := target.Format(httpDateLayout)

	got := DeriveRetryAfter(header, responseDate, true)
	if !got.Present || got.Delay != 200*time.Second {
		t.Errorf("Delay = %v, Present = %v, want 200s, present", got.Delay, got.Present)
	}
}
