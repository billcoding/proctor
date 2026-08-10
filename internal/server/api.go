package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/billcoding/proctor/internal/common"
	"github.com/billcoding/proctor/internal/config"
	"github.com/billcoding/proctor/internal/model"
)

type API struct {
	store  *Store
	cfg    config.ServerConfig
	online time.Duration
	shell  *ShellHub
}

func NewAPI(store *Store, cfg config.ServerConfig) *API {
	return &API{
		store:  store,
		cfg:    cfg,
		online: time.Duration(cfg.OnlineAfter) * time.Second,
		shell:  NewShellHub(),
	}
}

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/agent/heartbeat", a.withAgentAuth(a.handleHeartbeat))
	mux.HandleFunc("/api/agent/command/result", a.withAgentAuth(a.handleCommandResult))
	mux.HandleFunc("/api/agent/fs/result", a.withAgentAuth(a.handleFSResult))
	mux.HandleFunc("/api/agent/update/check", a.withAgentAuth(a.handleUpdateCheck))
	mux.HandleFunc("/api/agent/update/download/", a.withAgentAuth(a.handleUpdateDownload))
	mux.HandleFunc("/api/agent/shell", a.handleAgentShell)

	mux.HandleFunc("/api/stats", a.withAuth(a.handleStats))
	mux.HandleFunc("/api/agents", a.withAuth(a.handleAgents))
	mux.HandleFunc("/api/agents/", a.handleAgentsOrShell)
	mux.HandleFunc("/api/alerts", a.withAuth(a.handleAlerts))
	mux.HandleFunc("/api/alerts/", a.withAuth(a.handleAlertSub))
	mux.HandleFunc("/api/policies", a.withAuth(a.handlePolicies))
	mux.HandleFunc("/api/policies/", a.withAuth(a.handlePolicySub))
	mux.HandleFunc("/api/commands", a.withAuth(a.handleCommands))
	mux.HandleFunc("/api/commands/", a.withAuth(a.handleCommandSub))
	mux.HandleFunc("/api/fs/jobs/", a.withAuth(a.handleFSJobGet))
	mux.HandleFunc("/api/update/latest", a.withAuth(a.handleUpdateLatest))
	mux.HandleFunc("/api/updates", a.withAuth(a.handleUpdates))
	mux.HandleFunc("/api/updates/", a.withAuth(a.handleUpdatesSub))
}

// handleAgentsOrShell routes WebSocket shell separately (auth via query token).
func (a *API) handleAgentsOrShell(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/shell") {
		a.handleTeacherShell(w, r)
		return
	}
	a.withAuth(a.handleAgentSub)(w, r)
}

func (a *API) withAgentAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.AgentToken != "" {
			tok := r.Header.Get("X-Agent-Token")
			if tok == "" {
				tok = r.URL.Query().Get("agent_token")
			}
			if tok != a.cfg.AgentToken {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized agent"})
				return
			}
		}
		next(w, r)
	}
}

func (a *API) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.AdminToken != "" {
			tok := r.Header.Get("X-Admin-Token")
			if tok == "" {
				tok = r.URL.Query().Get("token")
			}
			if tok != a.cfg.AdminToken {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
				return
			}
		}
		next(w, r)
	}
}

func (a *API) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	var hb model.HeartbeatPayload
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if hb.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "agent_id required"})
		return
	}
	if err := a.store.UpsertHeartbeat(hb); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = a.store.AddAlerts(hb.Alerts)

	policy, err := a.store.PolicyForAgent(hb.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	cmds, err := a.store.PendingCommands(hb.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	fsJobs, err := a.store.PendingFSJobs(hb.AgentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	shells := a.shell.PendingForAgent(hb.AgentID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "policy": policy, "commands": cmds, "fs_jobs": fsJobs, "shell_sessions": shells,
	})
}

func (a *API) handleFSResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	var res model.FSJobResult
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if res.JobID == "" || res.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "job_id and agent_id required"})
		return
	}
	if err := a.store.CompleteFSJob(res); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleFSJobGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/fs/jobs/"), "/")
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false})
		return
	}
	job, err := a.store.GetFSJob(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not found"})
		return
	}
	// Don't echo write content back to console polls.
	job.Content = ""
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": job})
}

func (a *API) handleCommandResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	var res model.CommandResult
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := a.store.CompleteCommand(res); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := a.store.Stats(a.online)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": st})
}

func (a *API) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		return
	}
	list, err := a.store.ListAgents(a.online)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agents": list})
}

func (a *API) handleAgentSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false})
		return
	}
	// POST /api/agents/update — batch upgrade
	if parts[0] == "update" {
		a.handleAgentsBatchUpdate(w, r)
		return
	}
	id := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			info, hb, err := a.store.GetAgent(id, a.online)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not found"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "agent": info, "heartbeat": hb,
				"policy_id": a.store.AgentPolicyID(id),
			})
		case http.MethodDelete:
			if err := a.store.DeleteAgent(id); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		case http.MethodPatch:
			var body struct {
				StudentName string `json:"student_name"`
				Classroom   string `json:"classroom"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			if err := a.store.UpdateAgentMeta(id, body.StudentName, body.Classroom); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
		}
		return
	}

	switch parts[1] {
	case "command":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
			return
		}
		var body struct {
			Type    string            `json:"type"`
			Payload map[string]string `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		cmd := model.Command{
			ID: common.NewID("cmd"), AgentID: id, Type: body.Type, Payload: body.Payload,
			Status: "pending", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := a.store.EnqueueCommand(cmd); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": cmd})
	case "update":
		a.handleAgentUpdate(w, r, id)
	case "policy":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
			return
		}
		var body struct {
			PolicyID string `json:"policy_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := a.store.AssignPolicy(id, body.PolicyID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "fs":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		var body struct {
			Op      string `json:"op"`
			Path    string `json:"path"`
			Dest    string `json:"dest"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		switch body.Op {
		case "roots", "list", "stat", "read", "write", "mkdir", "delete", "rename":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unsupported fs op"})
			return
		}
		if body.Op != "roots" && strings.TrimSpace(body.Path) == "" && body.Op != "list" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "path required"})
			return
		}
		job := model.FSJob{
			ID: common.NewID("fs"), AgentID: id, Op: body.Op, Path: body.Path, Dest: body.Dest,
			Content: body.Content, Status: "pending",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := a.store.EnqueueFSJob(job); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		job.Content = ""
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": job})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false})
	}
}

func (a *API) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	limit, err := a.store.GetAlertRetentionLimit()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Retention is per-agent; list shows a recent aggregate up to perAgent×agents.
	list, err := a.store.ListAlerts(a.store.alertListLimit(limit))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "alerts": list, "limit": limit})
}

func (a *API) handleAlertSub(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/alerts/"), "/")
	if path == "retention" {
		a.handleAlertRetention(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	if strings.HasSuffix(path, "/ack") {
		id := strings.Trim(strings.TrimSuffix(path, "/ack"), "/")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "alert id required"})
			return
		}
		if err := a.store.AckAlert(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"ok": false})
}

func (a *API) handleAlertRetention(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit, err := a.store.GetAlertRetentionLimit()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "limit": limit, "min": minAlertRetention, "max": maxAlertRetention,
		})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Limit int `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		deleted, err := a.store.SetAlertRetentionLimit(body.Limit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "limit": body.Limit, "deleted": deleted,
			"min": minAlertRetention, "max": maxAlertRetention,
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
	}
}

func (a *API) handlePolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := a.store.ListPolicies()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policies": list})
	case http.MethodPost:
		var p model.Policy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if p.ID == "" {
			p.ID = common.NewID("pol")
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		if err := a.store.SavePolicy(p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policy": p})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
	}
}

func (a *API) handlePolicySub(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/policies/"), "/")
	switch r.Method {
	case http.MethodGet:
		p, err := a.store.GetPolicy(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policy": p})
	case http.MethodPut:
		var p model.Policy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		p.ID = id
		if err := a.store.SavePolicy(p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "policy": p})
	case http.MethodDelete:
		if err := a.store.DeletePolicy(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false})
	}
}

func (a *API) handleCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
		return
	}
	days, err := a.store.GetCommandRetentionDays()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, err := a.store.ListCommands(100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commands": list, "days": days})
}

func (a *API) handleCommandSub(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/commands/"), "/")
	if path == "retention" {
		a.handleCommandRetention(w, r)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"ok": false})
}

func (a *API) handleCommandRetention(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		days, err := a.store.GetCommandRetentionDays()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "days": days, "min": minCommandRetentionDays, "max": maxCommandRetentionDays,
		})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Days int `json:"days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		deleted, err := a.store.SetCommandRetentionDays(body.Days)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "days": body.Days, "deleted": deleted,
			"min": minCommandRetentionDays, "max": maxCommandRetentionDays,
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method"})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
