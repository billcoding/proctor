package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/billcoding/proctor/internal/common"
	"github.com/billcoding/proctor/internal/config"
	"github.com/billcoding/proctor/internal/model"
)

// Version is set at build time via -ldflags.
var Version = "0.1.0"

// Runtime is the long-running agent loop used by the system service.
type Runtime struct {
	cfg        config.AgentConfig
	configPath string
	collector  *Collector
	enforcer   *Enforcer
	client     *Client
	updater    *Updater

	mu        sync.Mutex
	cancel    context.CancelFunc
	logCloser io.Closer
}

func NewRuntime(cfg config.AgentConfig, configPath string) (*Runtime, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	id, err := common.EnsureAgentID(cfg.DataDir, cfg.AgentID)
	if err != nil {
		return nil, err
	}
	cfg.AgentID = id

	client := NewClient(cfg.ServerURL, id, cfg.AgentToken, cfg.InsecureSkipVerify)
	rt := &Runtime{
		cfg:        cfg,
		configPath: configPath,
		collector:  NewCollector(cfg.TopNProcesses),
		enforcer:   NewEnforcer(),
		client:     client,
		updater:    NewUpdater(client, configPath),
	}
	if err := rt.setupLog(); err != nil {
		return nil, err
	}
	return rt, nil
}

func (r *Runtime) setupLog() error {
	// Default / empty / conventional relative path → cwd/logs/agent.log
	// (falls back to <exeDir>/logs when service cwd is unusable).
	// Any other log_file value is used as-is (absolute or relative).
	path := strings.TrimSpace(r.cfg.LogFile)
	if path == "" || path == "logs/agent.log" || path == filepath.FromSlash("logs/agent.log") {
		path = resolveDefaultLogPath()
	}
	w, err := openDailyLog(path)
	if err != nil {
		return fmt.Errorf("open log %s: %w", path, err)
	}
	r.logCloser = w
	// Keep stderr (console/service journal) and file.
	log.SetOutput(io.MultiWriter(os.Stderr, w))
	log.Printf("logging to %s (daily rotate)", path)
	return nil
}

func (r *Runtime) AgentID() string { return r.cfg.AgentID }

func (r *Runtime) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.loop(ctx)
	log.Printf("proctor-agent started id=%s server=%s", r.cfg.AgentID, r.cfg.ServerURL)
	return nil
}

func (r *Runtime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	log.Printf("proctor-agent stopped")
	if r.logCloser != nil {
		_ = r.logCloser.Close()
		r.logCloser = nil
	}
	return nil
}

func (r *Runtime) loop(ctx context.Context) {
	// Immediate first tick so registration is quick after service start.
	r.tick()
	interval := time.Duration(r.cfg.CollectIntervalSec) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick()
			p := r.enforcer.Policy()
			if p.CollectIntervalSec > 0 {
				next := time.Duration(p.CollectIntervalSec) * time.Second
				if next < 5*time.Second {
					next = 5 * time.Second
				}
				if next != interval {
					ticker.Reset(next)
					interval = next
				}
			}
			if p.ReportTopNProcesses > 0 {
				r.collector.TopN = p.ReportTopNProcesses
			}
		}
	}
}

func (r *Runtime) tick() {
	policy := r.enforcer.Policy()
	hb, err := r.collector.Collect(
		r.cfg.AgentID, r.cfg.StudentName, r.cfg.Classroom, Version, policy.ProcessBlacklist,
	)
	if err != nil {
		log.Printf("collect failed: %v", err)
		return
	}
	alerts := r.enforcer.Evaluate(&hb)
	hb.Alerts = alerts

	newPolicy, cmds, fsJobs, shells, err := r.client.Heartbeat(hb)
	if err != nil {
		log.Printf("heartbeat failed: %v", err)
		return
	}
	if newPolicy != nil && newPolicy.ID != "" {
		r.enforcer.SetPolicy(*newPolicy)
	}
	for _, cmd := range cmds {
		r.handleCommand(cmd)
	}
	for _, job := range fsJobs {
		r.handleFSJob(job)
	}
	if len(shells) > 0 {
		r.handleShellOffers(shells)
	}
	if r.cfg.AutoUpdate && r.updater != nil {
		if applied, err := r.updater.CheckAndApply(false); err != nil {
			log.Printf("auto-update failed: %v", err)
		} else if applied {
			log.Printf("auto-update applied; restarting")
		}
	}
}

// RunUpdateOnce manually checks and applies an update (CLI: proctor-agent update [version]).
// Empty target follows server latest; non-empty pins a published version.
func (r *Runtime) RunUpdateOnce(target string) error {
	if r.updater == nil {
		return fmt.Errorf("updater not initialized")
	}
	var applied bool
	var err error
	if strings.TrimSpace(target) != "" {
		applied, err = r.updater.ApplyVersion(target)
	} else {
		applied, err = r.updater.CheckAndApply(true)
	}
	if err != nil {
		return err
	}
	if !applied {
		log.Printf("no update applied (current=%s)", Version)
		return nil
	}
	// Give restart goroutine a moment before CLI returns.
	time.Sleep(1500 * time.Millisecond)
	return nil
}

func (r *Runtime) handleFSJob(job model.FSJob) {
	res := executeFSJob(job)
	if err := r.client.ReportFSJob(res); err != nil {
		log.Printf("report fs job failed: %v", err)
	}
}

func (r *Runtime) handleCommand(cmd model.Command) {
	result := model.CommandResult{
		CommandID: cmd.ID,
		AgentID:   r.cfg.AgentID,
		Status:    "done",
	}
	switch cmd.Type {
	case "kill_process":
		pidStr := cmd.Payload["pid"]
		var pid int32
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil || pid <= 0 {
			result.Status = "failed"
			result.Result = "invalid pid"
		} else if err := KillProcess(pid); err != nil {
			result.Status = "failed"
			result.Result = err.Error()
		} else {
			result.Result = fmt.Sprintf("killed pid=%d", pid)
		}
	case "refresh_policy":
		result.Result = "ok"
	case "message":
		msg := ""
		if cmd.Payload != nil {
			msg = cmd.Payload["text"]
		}
		allowReply := payloadBoolDefault(cmd.Payload, "reply", true)
		log.Printf("teacher message: %s reply=%v", msg, allowReply)
		// Dialog can block for minutes; never stall the heartbeat loop.
		go r.handleTeacherMessage(cmd, msg, allowReply)
		return
	case "ping":
		result.Result = "pong"
	case "shutdown":
		if !r.enforcer.Policy().AllowShutdown {
			result.Status = "failed"
			result.Result = "policy denies shutdown"
			break
		}
		result.Result = "shutting down"
		_ = r.client.ReportCommand(result)
		go func() {
			time.Sleep(800 * time.Millisecond)
			if err := systemShutdown(); err != nil {
				log.Printf("shutdown failed: %v", err)
			}
		}()
		return
	case "restart":
		if !r.enforcer.Policy().AllowShutdown {
			result.Status = "failed"
			result.Result = "policy denies restart"
			break
		}
		result.Result = "restarting"
		_ = r.client.ReportCommand(result)
		go func() {
			time.Sleep(800 * time.Millisecond)
			if err := systemRestart(); err != nil {
				log.Printf("restart failed: %v", err)
			}
		}()
		return
	case "update", "upgrade":
		target := ""
		if cmd.Payload != nil {
			target = strings.TrimSpace(cmd.Payload["version"])
		}
		if target != "" {
			result.Result = "updating to " + target
		} else {
			result.Result = "updating to latest"
		}
		_ = r.client.ReportCommand(result)
		go func() {
			time.Sleep(500 * time.Millisecond)
			if r.updater == nil {
				log.Printf("update command: updater not initialized")
				return
			}
			var applied bool
			var err error
			if target != "" {
				applied, err = r.updater.ApplyVersion(target)
			} else {
				applied, err = r.updater.CheckAndApply(true)
			}
			if err != nil {
				log.Printf("update command failed: %v", err)
				return
			}
			if !applied {
				log.Printf("update command: already at requested version (current=%s)", Version)
			}
		}()
		return
	default:
		result.Status = "failed"
		result.Result = "unsupported command: " + cmd.Type
	}
	if err := r.client.ReportCommand(result); err != nil {
		log.Printf("report command failed: %v", err)
	}
}

// handleTeacherMessage shows a confirm/reply dialog off the heartbeat path,
// then reports the student's ack or short reply as the command result.
func (r *Runtime) handleTeacherMessage(cmd model.Command, msg string, allowReply bool) {
	// Mark done immediately so PendingCommands won't redeliver while the dialog is open.
	_ = r.client.ReportCommand(model.CommandResult{
		CommandID: cmd.ID,
		AgentID:   r.cfg.AgentID,
		Status:    "done",
		Result:    "等待学生确认…",
	})
	result := model.CommandResult{
		CommandID: cmd.ID,
		AgentID:   r.cfg.AgentID,
		Status:    "done",
	}
	out, err := showTeacherMessage(msg, allowReply)
	if err != nil {
		result.Status = "failed"
		result.Result = err.Error()
		log.Printf("teacher message dialog failed: %v", err)
	} else {
		result.Result = out
		log.Printf("teacher message result: %s", out)
	}
	if err := r.client.ReportCommand(result); err != nil {
		log.Printf("report command failed: %v", err)
	}
}

func payloadBoolDefault(payload map[string]string, key string, def bool) bool {
	if payload == nil {
		return def
	}
	v, ok := payload[key]
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	default:
		return def
	}
}
