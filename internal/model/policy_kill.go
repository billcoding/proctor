package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KillActionKill  = "kill"
	KillActionWarn  = "warn"
	KillActionAlert = "alert"

	KillScheduleAllDay  = "all_day"
	KillScheduleWindows = "windows"

	DefaultKillScanIntervalSec  = 10
	MinKillScanIntervalSec      = 3
	DefaultKillWarnCountdownSec = 10
	DefaultKillCooldownSec      = 60
	DefaultKillWarnMessage      = "检测到违规进程「{name}」(pid={pid})，将在 {sec} 秒后结束。"
)

// NormalizeKillPolicy fills defaults for blacklist auto-enforcement fields.
// Safe to call repeatedly; does not change KillBlacklisted itself.
func NormalizeKillPolicy(p *Policy) {
	if p == nil {
		return
	}
	if p.KillScanIntervalSec <= 0 {
		p.KillScanIntervalSec = DefaultKillScanIntervalSec
	}
	if p.KillScanIntervalSec < MinKillScanIntervalSec {
		p.KillScanIntervalSec = MinKillScanIntervalSec
	}
	mode := strings.ToLower(strings.TrimSpace(p.KillScheduleMode))
	if mode != KillScheduleWindows {
		mode = KillScheduleAllDay
	}
	p.KillScheduleMode = mode

	if p.KillWarnCountdownSec <= 0 {
		p.KillWarnCountdownSec = DefaultKillWarnCountdownSec
	}
	if p.KillCooldownSec <= 0 {
		p.KillCooldownSec = DefaultKillCooldownSec
	}
	if strings.TrimSpace(p.KillWarnMessage) == "" {
		p.KillWarnMessage = DefaultKillWarnMessage
	}
	p.KillActions = NormalizeKillActions(p.KillActions, p.KillBlacklisted)
}

// NormalizeKillActions returns a de-duplicated action list.
// When empty and enabled is true, defaults to kill+alert (legacy behavior).
func NormalizeKillActions(actions []string, enabled bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range actions {
		a = strings.ToLower(strings.TrimSpace(a))
		switch a {
		case KillActionKill, KillActionWarn, KillActionAlert:
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	if len(out) == 0 && enabled {
		return []string{KillActionKill, KillActionAlert}
	}
	return out
}

// KillActionSet reports which enforcement actions are enabled.
func KillActionSet(p Policy) (doKill, doWarn, doAlert bool) {
	for _, a := range NormalizeKillActions(p.KillActions, p.KillBlacklisted) {
		switch a {
		case KillActionKill:
			doKill = true
		case KillActionWarn:
			doWarn = true
		case KillActionAlert:
			doAlert = true
		}
	}
	return
}

// InKillSchedule reports whether local time now falls in an active enforcement window.
func InKillSchedule(p Policy, now time.Time) bool {
	NormalizeKillPolicy(&p)
	if p.KillScheduleMode != KillScheduleWindows {
		return true
	}
	if len(p.KillScheduleWindows) == 0 {
		return false
	}
	wd := int(now.Weekday())
	mins := now.Hour()*60 + now.Minute()
	for _, w := range p.KillScheduleWindows {
		if !weekdayAllowed(w.Weekdays, wd) {
			continue
		}
		start, ok1 := parseHHMM(w.Start)
		end, ok2 := parseHHMM(w.End)
		if !ok1 || !ok2 {
			continue
		}
		if inTimeRange(mins, start, end) {
			return true
		}
	}
	return false
}

func weekdayAllowed(days []int, wd int) bool {
	if len(days) == 0 {
		return true
	}
	for _, d := range days {
		if d == wd {
			return true
		}
	}
	return false
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func inTimeRange(mins, start, end int) bool {
	if start == end {
		// Identical start/end → all day within selected weekdays.
		return true
	}
	if start < end {
		return mins >= start && mins < end
	}
	// Spans midnight.
	return mins >= start || mins < end
}

// FormatKillWarnMessage replaces {name}, {pid}, {sec} in the template.
func FormatKillWarnMessage(tmpl, name string, pid int32, sec int) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultKillWarnMessage
	}
	r := strings.NewReplacer(
		"{name}", name,
		"{pid}", fmt.Sprintf("%d", pid),
		"{sec}", fmt.Sprintf("%d", sec),
	)
	return r.Replace(tmpl)
}
