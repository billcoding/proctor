package model

import (
	"testing"
	"time"
)

func TestNormalizeKillActionsLegacy(t *testing.T) {
	got := NormalizeKillActions(nil, true)
	if len(got) != 2 || got[0] != KillActionKill || got[1] != KillActionAlert {
		t.Fatalf("legacy defaults: %v", got)
	}
	if got := NormalizeKillActions(nil, false); len(got) != 0 {
		t.Fatalf("disabled empty: %v", got)
	}
}

func TestInKillScheduleAllDay(t *testing.T) {
	p := Policy{KillBlacklisted: true, KillScheduleMode: KillScheduleAllDay}
	if !InKillSchedule(p, time.Now()) {
		t.Fatal("all_day should always be active")
	}
}

func TestInKillScheduleWindows(t *testing.T) {
	// Monday 10:00 local
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.Local) // Monday
	p := Policy{
		KillBlacklisted:  true,
		KillScheduleMode: KillScheduleWindows,
		KillScheduleWindows: []KillTimeWindow{{
			Weekdays: []int{1, 2, 3, 4, 5}, // Mon-Fri
			Start:    "08:00",
			End:      "17:30",
		}},
	}
	if !InKillSchedule(p, now) {
		t.Fatal("expected inside window")
	}
	sat := time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local)
	if InKillSchedule(p, sat) {
		t.Fatal("Saturday should be outside")
	}
	evening := time.Date(2026, 8, 10, 18, 0, 0, 0, time.Local)
	if InKillSchedule(p, evening) {
		t.Fatal("18:00 should be outside")
	}
}

func TestInKillScheduleOvernight(t *testing.T) {
	p := Policy{
		KillBlacklisted:  true,
		KillScheduleMode: KillScheduleWindows,
		KillScheduleWindows: []KillTimeWindow{{
			Weekdays: []int{0, 1, 2, 3, 4, 5, 6},
			Start:    "22:00",
			End:      "06:00",
		}},
	}
	night := time.Date(2026, 8, 10, 23, 0, 0, 0, time.Local)
	morning := time.Date(2026, 8, 11, 5, 0, 0, 0, time.Local)
	day := time.Date(2026, 8, 11, 12, 0, 0, 0, time.Local)
	if !InKillSchedule(p, night) || !InKillSchedule(p, morning) {
		t.Fatal("overnight window failed")
	}
	if InKillSchedule(p, day) {
		t.Fatal("noon should be outside overnight window")
	}
}

func TestFormatKillWarnMessage(t *testing.T) {
	s := FormatKillWarnMessage("进程 {name} #{pid} {sec}s", "steam", 42, 10)
	if s != "进程 steam #42 10s" {
		t.Fatalf("got %q", s)
	}
}
