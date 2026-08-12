package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billcoding/proctor/internal/common"
	"github.com/billcoding/proctor/internal/model"
	"github.com/shirou/gopsutil/v4/process"
)

// killScheduler periodically scans for policy-violating processes and
// applies warn / kill / alert actions on its own ticker (not heartbeat).
type killScheduler struct {
	runtime *Runtime

	mu       sync.Mutex
	cooldown map[string]time.Time // process name key → last action time
	inflight map[string]bool      // process name key currently in warn/kill pipeline
}

func newKillScheduler(rt *Runtime) *killScheduler {
	return &killScheduler{
		runtime:  rt,
		cooldown: map[string]time.Time{},
		inflight: map[string]bool{},
	}
}

func (s *killScheduler) loop(ctx context.Context) {
	interval := time.Duration(model.DefaultKillScanIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// First scan shortly after start so enforcement does not wait a full interval.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.scanOnce()
			interval = s.nextInterval()
			ticker.Reset(interval)
		case <-ticker.C:
			s.scanOnce()
			if next := s.nextInterval(); next != interval {
				ticker.Reset(next)
				interval = next
			}
		}
	}
}

func (s *killScheduler) nextInterval() time.Duration {
	p := s.runtime.enforcer.Policy()
	model.NormalizeKillPolicy(&p)
	return time.Duration(p.KillScanIntervalSec) * time.Second
}

func (s *killScheduler) scanOnce() {
	p := s.runtime.enforcer.Policy()
	if !p.Enabled || !p.KillBlacklisted {
		return
	}
	model.NormalizeKillPolicy(&p)
	if !model.InKillSchedule(p, time.Now()) {
		return
	}
	doKill, doWarn, doAlert := model.KillActionSet(p)
	if !doKill && !doWarn && !doAlert {
		return
	}

	violations, err := listViolatingProcesses(p)
	if err != nil {
		log.Printf("kill-scan: list processes: %v", err)
		return
	}
	if len(violations) == 0 {
		return
	}

	now := time.Now()
	for _, v := range violations {
		key := cooldownKey(v.Name)
		s.mu.Lock()
		if s.inflight[key] {
			s.mu.Unlock()
			continue
		}
		if last, ok := s.cooldown[key]; ok && now.Sub(last) < time.Duration(p.KillCooldownSec)*time.Second {
			s.mu.Unlock()
			continue
		}
		s.inflight[key] = true
		s.cooldown[key] = now
		s.mu.Unlock()

		vv := v
		pp := p
		go s.enforceOne(vv, pp, doKill, doWarn, doAlert)
	}
}

type procHit struct {
	PID     int32
	Name    string
	Cmdline string
	Reason  string
}

func (s *killScheduler) enforceOne(v procHit, p model.Policy, doKill, doWarn, doAlert bool) {
	key := cooldownKey(v.Name)
	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
	}()

	countdown := p.KillWarnCountdownSec
	if countdown < 0 {
		countdown = 0
	}

	if doAlert {
		s.runtime.queueAlert(model.Alert{
			ID:        common.NewID("al"),
			AgentID:   s.runtime.cfg.AgentID,
			Level:     "critical",
			Category:  "process",
			Message:   fmt.Sprintf("检测到违规进程(%s): %s (pid=%d)", v.Reason, v.Name, v.PID),
			Detail:    v.Cmdline,
			CreatedAt: time.Now().UTC(),
		})
	}

	if doWarn {
		msg := model.FormatKillWarnMessage(p.KillWarnMessage, v.Name, v.PID, countdown)
		log.Printf("kill-scan: warn process %d (%s): %s", v.PID, v.Name, msg)
		go func() {
			if _, err := showTeacherMessage(msg, false); err != nil {
				log.Printf("kill-scan: warn dialog failed: %v", err)
			}
		}()
	}

	if !doKill {
		return
	}

	// Warn mode: wait countdown then kill. Kill-only: end immediately.
	if doWarn && countdown > 0 {
		time.Sleep(time.Duration(countdown) * time.Second)
	}

	// Re-check process still matches (may have exited during countdown).
	alive, name := processStillNamed(v.PID, v.Name)
	if !alive {
		log.Printf("kill-scan: process %d (%s) already gone before kill", v.PID, v.Name)
		return
	}
	if err := KillProcess(v.PID); err != nil {
		log.Printf("kill-scan: kill %d (%s): %v", v.PID, name, err)
		return
	}
	log.Printf("kill-scan: killed process %d (%s) reason=%s", v.PID, name, v.Reason)
}

func cooldownKey(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	return n
}

func listViolatingProcesses(p model.Policy) ([]procHit, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}
	selfName := strings.ToLower(filepath.Base(os.Args[0]))
	bl := normalizeSet(p.ProcessBlacklist)
	wl := normalizeSet(p.ProcessWhitelist)

	var hits []procHit
	seenPID := map[int32]bool{}
	for _, proc := range procs {
		name, _ := proc.Name()
		if name == "" {
			continue
		}
		nameLower := strings.ToLower(name)
		if nameLower == selfName || strings.Contains(nameLower, "proctor-agent") {
			continue
		}
		if isProtectedProcess(name) {
			continue
		}

		violated := false
		reason := ""
		if p.ProcessWhitelistMode {
			if len(wl) == 0 {
				continue
			}
			if !matchName(name, wl) {
				violated = true
				reason = "不在白名单"
			}
		} else if matchName(name, bl) {
			violated = true
			reason = "黑名单"
		}
		if !violated || seenPID[proc.Pid] {
			continue
		}
		seenPID[proc.Pid] = true
		cmd, _ := proc.Cmdline()
		hits = append(hits, procHit{
			PID:     proc.Pid,
			Name:    name,
			Cmdline: trimCmd(cmd),
			Reason:  reason,
		})
	}
	return hits, nil
}

func processStillNamed(pid int32, expectName string) (bool, string) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return false, expectName
	}
	name, err := p.Name()
	if err != nil || name == "" {
		return false, expectName
	}
	// PID reuse guard: require same basename (case-insensitive, ignore .exe).
	if cooldownKey(name) != cooldownKey(expectName) {
		return false, name
	}
	return true, name
}
