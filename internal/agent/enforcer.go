package agent

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/billcoding/proctor/internal/common"
	"github.com/billcoding/proctor/internal/model"
	"github.com/shirou/gopsutil/v4/process"
)

// Enforcer applies policy locally on the student machine.
type Enforcer struct {
	mu     sync.RWMutex
	policy model.Policy
}

func NewEnforcer() *Enforcer {
	return &Enforcer{
		policy: model.Policy{
			ID:                  "default",
			Name:                "default",
			Enabled:             true,
			KillBlacklisted:     true,
			AllowShutdown:       true,
			MaxCPUPercent:       95,
			MaxMemPercent:       95,
			MaxDiskPercent:      95,
			CollectIntervalSec:  5,
			ReportTopNProcesses: 30,
			ProcessBlacklist: []string{
				"steam", "epicgameslauncher", "discord", "qqmusic",
				"bilibili", "douyin", "tiktok", "minecraft", "wegame",
			},
			DomainBlacklist: []string{
				"tiktok.com", "douyin.com", "bilibili.com", "steampowered.com",
				"wegame.qq.com", "wegame.com",
			},
		},
	}
}

func (e *Enforcer) SetPolicy(p model.Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy = p
}

func (e *Enforcer) Policy() model.Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

func (e *Enforcer) Evaluate(hb *model.HeartbeatPayload) []model.Alert {
	p := e.Policy()
	if !p.Enabled {
		return nil
	}
	var alerts []model.Alert
	now := hb.Timestamp

	if p.MaxCPUPercent > 0 && hb.Resources.CPUPercent >= p.MaxCPUPercent {
		alerts = append(alerts, model.Alert{
			ID: common.NewID("al"), AgentID: hb.AgentID, Level: "warn", Category: "resource",
			Message: fmt.Sprintf("CPU 使用率过高: %.1f%%", hb.Resources.CPUPercent), CreatedAt: now,
		})
	}
	if p.MaxMemPercent > 0 && hb.Resources.MemPercent >= p.MaxMemPercent {
		alerts = append(alerts, model.Alert{
			ID: common.NewID("al"), AgentID: hb.AgentID, Level: "warn", Category: "resource",
			Message: fmt.Sprintf("内存使用率过高: %.1f%%", hb.Resources.MemPercent), CreatedAt: now,
		})
	}
	for _, d := range hb.Disks {
		if p.MaxDiskPercent > 0 && d.Percent >= p.MaxDiskPercent {
			alerts = append(alerts, model.Alert{
				ID: common.NewID("al"), AgentID: hb.AgentID, Level: "warn", Category: "disk",
				Message: fmt.Sprintf("磁盘 %s 使用率过高: %.1f%%", d.MountPoint, d.Percent), CreatedAt: now,
			})
		}
	}

	selfName := strings.ToLower(filepath.Base(os.Args[0]))
	bl := normalizeSet(p.ProcessBlacklist)
	wl := normalizeSet(p.ProcessWhitelist)

	for _, proc := range hb.Processes {
		nameLower := strings.ToLower(proc.Name)
		if nameLower == selfName || strings.Contains(nameLower, "proctor-agent") {
			continue
		}
		if isProtectedProcess(proc.Name) {
			continue
		}

		violated := false
		reason := ""
		if p.ProcessWhitelistMode {
			if len(wl) == 0 {
				continue
			}
			if !matchName(proc.Name, wl) {
				violated = true
				reason = "不在白名单"
			}
		} else if matchName(proc.Name, bl) {
			violated = true
			reason = "黑名单"
		}
		if !violated {
			continue
		}

		alerts = append(alerts, model.Alert{
			ID: common.NewID("al"), AgentID: hb.AgentID, Level: "critical", Category: "process",
			Message: fmt.Sprintf("检测到违规进程(%s): %s (pid=%d)", reason, proc.Name, proc.PID),
			Detail:  proc.Cmdline, CreatedAt: now,
		})
		if p.KillBlacklisted {
			if err := KillProcess(proc.PID); err != nil {
				log.Printf("kill process %d (%s): %v", proc.PID, proc.Name, err)
			} else {
				log.Printf("killed process %d (%s) reason=%s", proc.PID, proc.Name, reason)
			}
		}
	}

	if len(p.DomainBlacklist) > 0 {
		domains := normalizeSet(p.DomainBlacklist)
		seen := map[string]bool{}
		for _, n := range hb.Networks {
			if n.Status != "ESTABLISHED" {
				continue
			}
			matched, rule := matchDomain(n, domains)
			if !matched {
				continue
			}
			key := n.RAddr + "|" + rule
			if seen[key] {
				continue
			}
			seen[key] = true
			hostHint := n.RemoteHost
			if hostHint == "" {
				hostHint = n.RAddr
			}
			alerts = append(alerts, model.Alert{
				ID: common.NewID("al"), AgentID: hb.AgentID, Level: "warn", Category: "network",
				Message:   fmt.Sprintf("疑似访问受限域名 %s: %s (%s)", rule, hostHint, n.Process),
				Detail:    n.RAddr,
				CreatedAt: now,
			})
		}
	}
	return alerts
}

func matchDomain(n model.NetworkSnap, domains map[string]struct{}) (bool, string) {
	candidates := []string{strings.ToLower(n.RemoteHost), strings.ToLower(n.RAddr)}
	if host, _, err := net.SplitHostPort(n.RAddr); err == nil {
		candidates = append(candidates, strings.ToLower(host))
	}
	for d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		for _, c := range candidates {
			if c == "" {
				continue
			}
			if c == d || strings.HasSuffix(c, "."+d) || strings.Contains(c, d) {
				return true, d
			}
		}
	}
	return false, ""
}

func isProtectedProcess(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	protected := []string{
		"system", "idle", "smss", "csrss", "wininit", "services", "lsass", "svchost",
		"fontdrvhost", "dwm", "explorer", "runtimebroker", "searchhost", "shellexperiencehost",
		"systemd", "kthreadd", "ksoftirqd", "rcu_sched", "migration", "watchdog",
		"kernel_task", "launchd", "WindowServer", "loginwindow", "finder",
	}
	for _, p := range protected {
		if n == strings.ToLower(p) {
			return true
		}
	}
	// PID 0/1 style names vary; also protect short kernel-looking names on unix.
	if runtime.GOOS != "windows" && (strings.HasPrefix(n, "kworker") || strings.HasPrefix(n, "kthread")) {
		return true
	}
	return false
}

func KillProcess(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
